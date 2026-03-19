package lndclient

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/lnrpc"
	"lightningos-light/lnrpc/routerrpc"
	"lightningos-light/lnrpc/signrpc"
	"lightningos-light/lnrpc/walletrpc"
	"lightningos-light/lnrpc/wtclientrpc"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const recentOnchainWindowBlocks int64 = 20160

type Client struct {
	cfg             *config.Config
	logger          *log.Logger
	statusMu        sync.Mutex
	statusCached    bool
	statusCache     Status
	statusErr       error
	statusNextFetch time.Time
	infoCache       infoSnapshot
	infoCacheAt     time.Time
	infoCacheValid  bool
	channelStateMu  sync.Mutex
	channelInactive map[string]time.Time
	nodeAliasMu     sync.Mutex
	nodeAliasCache  map[string]nodeAliasCacheEntry
}

type nodeAliasCacheEntry struct {
	alias     string
	expiresAt time.Time
}

type DerivedKey struct {
	PublicKey string
	Family    int32
	Index     int32
}

type ChanPointShimParams struct {
	CapacitySat   int64
	PendingChanID []byte
	FundingTxID   string
	FundingVout   uint32
	LocalKey      DerivedKey
	RemoteKeyHex  string
	Musig2        bool
}

type OpenChannelWithShimParams struct {
	PubkeyHex         string
	CapacitySat       int64
	LocalFundingSat   int64
	PushSat           int64
	CloseAddress      string
	Private           bool
	SatPerVbyte       int64
	CommitmentType    lnrpc.CommitmentType
	ZeroConf          bool
	ScidAlias         bool
	ChanPointShimArgs ChanPointShimParams
}

type OutputScriptSendResult struct {
	TxID     string
	Vout     uint32
	RawTxHex string
}

type SendOutputScriptParams struct {
	SatPerVbyte      int64
	OutputScriptHex  string
	AmountSat        int64
	Label            string
	MinConfs         int32
	SpendUnconfirmed bool
}

type ComputedInputScript struct {
	Witness   [][]byte
	SigScript []byte
}

type ComputeInputScriptParams struct {
	RawTxHex         string
	InputIndex       uint32
	OutputScriptHex  string
	OutputSat        int64
	Key              DerivedKey
	WitnessScriptHex string
	SighashType      uint32
	SignMethod       signrpc.SignMethod
}

type SignOutputRawParams struct {
	RawTxHex         string
	InputIndex       uint32
	OutputScriptHex  string
	OutputSat        int64
	Key              DerivedKey
	WitnessScriptHex string
	SighashType      uint32
	SignMethod       signrpc.SignMethod
}

func New(cfg *config.Config, logger *log.Logger) *Client {
	return &Client{
		cfg:            cfg,
		logger:         logger,
		nodeAliasCache: make(map[string]nodeAliasCacheEntry),
	}
}

const (
	statusCacheOK                = 30 * time.Second
	statusCacheErr               = 45 * time.Second
	statusCacheTimeout           = 60 * time.Second
	maxGRPCMsgSize               = 32 * 1024 * 1024
	defaultConnectPeerTimeoutSec = uint64(8)
	nodeAliasCacheTTL            = 30 * time.Minute
	nodeAliasNotFoundCacheTTL    = 5 * time.Minute
)

func normalizePubkeyCacheKey(pubkey string) string {
	return strings.ToLower(strings.TrimSpace(pubkey))
}

func (c *Client) getNodeAliasFromCache(pubkey string) (string, bool) {
	key := normalizePubkeyCacheKey(pubkey)
	if key == "" {
		return "", false
	}

	now := time.Now()
	c.nodeAliasMu.Lock()
	defer c.nodeAliasMu.Unlock()

	entry, ok := c.nodeAliasCache[key]
	if !ok {
		return "", false
	}
	if now.After(entry.expiresAt) {
		delete(c.nodeAliasCache, key)
		return "", false
	}
	return entry.alias, true
}

func (c *Client) setNodeAliasCache(pubkey string, alias string, ttl time.Duration) {
	key := normalizePubkeyCacheKey(pubkey)
	if key == "" || ttl <= 0 {
		return
	}

	c.nodeAliasMu.Lock()
	defer c.nodeAliasMu.Unlock()

	if c.nodeAliasCache == nil {
		c.nodeAliasCache = make(map[string]nodeAliasCacheEntry)
	}
	c.nodeAliasCache[key] = nodeAliasCacheEntry{
		alias:     strings.TrimSpace(alias),
		expiresAt: time.Now().Add(ttl),
	}
}

func (c *Client) lookupNodeAliasWithClient(ctx context.Context, client lnrpc.LightningClient, pubkey string) string {
	trimmed := strings.TrimSpace(pubkey)
	if trimmed == "" {
		return ""
	}

	if alias, ok := c.getNodeAliasFromCache(trimmed); ok {
		return alias
	}

	info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: trimmed, IncludeChannels: false})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			c.setNodeAliasCache(trimmed, "", nodeAliasNotFoundCacheTTL)
		}
		return ""
	}

	node := info.GetNode()
	if node == nil {
		c.setNodeAliasCache(trimmed, "", nodeAliasNotFoundCacheTTL)
		return ""
	}

	alias := strings.TrimSpace(node.Alias)
	if alias == "" {
		c.setNodeAliasCache(trimmed, "", nodeAliasNotFoundCacheTTL)
		return ""
	}

	c.setNodeAliasCache(trimmed, alias, nodeAliasCacheTTL)
	return alias
}

func (c *Client) LookupNodeAlias(ctx context.Context, pubkey string) string {
	trimmed := strings.TrimSpace(pubkey)
	if trimmed == "" {
		return ""
	}

	if alias, ok := c.getNodeAliasFromCache(trimmed); ok {
		return alias
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return ""
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	return c.lookupNodeAliasWithClient(ctx, client, trimmed)
}

func (c *Client) ResetMissionControl(ctx context.Context) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := routerrpc.NewRouterClient(conn)
	_, err = client.ResetMissionControl(ctx, &routerrpc.ResetMissionControlRequest{})
	return err
}

func (c *Client) UpdateMissionControlHalfLife(ctx context.Context, halfLifeSec int64) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	if halfLifeSec < 0 {
		halfLifeSec = 0
	}
	client := routerrpc.NewRouterClient(conn)
	resp, err := client.GetMissionControlConfig(ctx, &routerrpc.GetMissionControlConfigRequest{})
	if err != nil {
		return err
	}
	cfg := resp.GetConfig()
	if cfg == nil {
		cfg = &routerrpc.MissionControlConfig{}
	}
	next := uint64(halfLifeSec)
	cfg.HalfLifeSeconds = next
	if apriori := cfg.GetApriori(); apriori != nil {
		apriori.HalfLifeSeconds = next
	}
	if bimodal := cfg.GetBimodal(); bimodal != nil {
		bimodal.DecayTime = next
	}
	_, err = client.SetMissionControlConfig(ctx, &routerrpc.SetMissionControlConfigRequest{Config: cfg})
	return err
}

func (c *Client) LookupPayment(ctx context.Context, paymentHash string, lookback time.Duration) (*lnrpc.Payment, error) {
	trimmed := strings.ToLower(strings.TrimSpace(paymentHash))
	if trimmed == "" {
		return nil, nil
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true,
		Reversed:          true,
		MaxPayments:       200,
	}
	if lookback > 0 {
		start := time.Now().Add(-lookback).Unix()
		if start > 0 {
			req.CreationDateStart = uint64(start)
		}
	}
	resp, err := client.ListPayments(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, pay := range resp.Payments {
		if pay == nil {
			continue
		}
		hash := strings.ToLower(strings.TrimSpace(pay.PaymentHash))
		if hash != "" && hash == trimmed {
			return pay, nil
		}
	}
	return nil, nil
}

const failedPaymentsPageSize = 5000
const failedPaymentsMaxPages = 200000

func (c *Client) CountFailedPayments(ctx context.Context) (int, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	var count int
	var indexOffset uint64
	var lastOffset uint64
	var pages int

	for {
		if pages >= failedPaymentsMaxPages {
			break
		}
		pages++

		req := &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: true,
			Reversed:          true,
			IndexOffset:       indexOffset,
			MaxPayments:       failedPaymentsPageSize,
		}
		resp, err := client.ListPayments(ctx, req)
		if err != nil {
			return 0, err
		}
		if resp == nil || len(resp.Payments) == 0 {
			break
		}

		minIndex := uint64(0)
		for _, pay := range resp.Payments {
			if pay == nil {
				continue
			}
			if pay.Status == lnrpc.Payment_FAILED {
				count++
			}
			if pay.PaymentIndex > 0 && (minIndex == 0 || pay.PaymentIndex < minIndex) {
				minIndex = pay.PaymentIndex
			}
		}

		if len(resp.Payments) < failedPaymentsPageSize {
			break
		}

		nextOffset := uint64(0)
		if resp.FirstIndexOffset != 0 {
			nextOffset = resp.FirstIndexOffset
		} else if minIndex != 0 {
			nextOffset = minIndex
		}
		if nextOffset == 0 || nextOffset == indexOffset || nextOffset == lastOffset {
			break
		}
		lastOffset = nextOffset
		indexOffset = nextOffset
	}

	return count, nil
}

func (c *Client) CleanFailedPayments(ctx context.Context) (int, error) {
	failedCount, err := c.CountFailedPayments(ctx)
	if err != nil {
		return 0, err
	}
	if failedCount == 0 {
		return 0, nil
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	_, err = client.DeleteAllPayments(ctx, &lnrpc.DeleteAllPaymentsRequest{
		FailedPaymentsOnly: true,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok {
			msg := strings.ToLower(strings.TrimSpace(st.Message()))
			if st.Code() == codes.Unimplemented || strings.Contains(msg, "unimplemented") {
				return 0, ErrFailedPaymentsCleanupUnsupported
			}
		}
		return 0, err
	}

	return failedCount, nil
}

type macaroonCredential struct {
	macaroon string
}

type BalanceSummary struct {
	OnchainSat                 int64
	LightningSat               int64
	OnchainConfirmedSat        int64
	OnchainUnconfirmedSat      int64
	LightningLocalSat          int64
	LightningUnsettledLocalSat int64
	Warnings                   []string
}

type WalletBalanceDetails struct {
	TotalSat              int64
	ConfirmedSat          int64
	UnconfirmedSat        int64
	LockedSat             int64
	ReservedAnchorSat     int64
	EstimatedSpendableSat int64
}

type ChannelPolicy struct {
	ChannelPoint      string
	BaseFeeMsat       int64
	FeeRatePpm        int64
	TimeLockDelta     int64
	MinHtlcMsat       uint64
	MaxHtlcMsat       uint64
	InboundBaseMsat   int64
	InboundFeeRatePpm int64
}

type UpdateChannelPolicyParams struct {
	ChannelPoint         string
	ApplyAll             bool
	BaseFeeMsat          int64
	FeeRatePpm           int64
	TimeLockDelta        int64
	InboundEnabled       bool
	InboundBaseMsat      int64
	InboundFeeRatePpm    int64
	MaxHtlcMsat          *uint64
	MinHtlcMsat          *uint64
	MinHtlcMsatSpecified bool
}

type infoSnapshot struct {
	SyncedToChain bool
	SyncedToGraph bool
	BlockHeight   int64
	Version       string
	Pubkey        string
	URI           string
	URIs          []string
}

type DecodedInvoice struct {
	AmountSat   int64
	AmountMsat  int64
	Memo        string
	Destination string
	PaymentHash string
	Expiry      int64
	Timestamp   int64
}

type CreatedInvoice struct {
	PaymentRequest string
	PaymentHash    string
	PaymentAddr    []byte
}

var ErrFailedPaymentsCleanupUnsupported = errors.New("lnd does not support deleting failed payments")

func (m macaroonCredential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{"macaroon": m.macaroon}, nil
}

func (m macaroonCredential) RequireTransportSecurity() bool {
	return true
}

func (c *Client) dial(ctx context.Context, withMacaroon bool) (*grpc.ClientConn, error) {
	tlsCert, err := os.ReadFile(c.cfg.LND.TLSCertPath)
	if err != nil {
		return nil, err
	}
	certPool := x509.NewCertPool()
	if ok := certPool.AppendCertsFromPEM(tlsCert); !ok {
		return nil, fmt.Errorf("failed to parse LND TLS cert")
	}

	creds := credentials.NewClientTLSFromCert(certPool, "")
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxGRPCMsgSize)),
	}

	if withMacaroon {
		macBytes, err := os.ReadFile(c.cfg.LND.AdminMacaroonPath)
		if err != nil {
			return nil, err
		}
		macCred := macaroonCredential{hex.EncodeToString(macBytes)}
		opts = append(opts, grpc.WithPerRPCCredentials(macCred))
	}

	return grpc.DialContext(ctx, c.cfg.LND.GRPCHost, opts...)
}

func (c *Client) DialLightning(ctx context.Context) (*grpc.ClientConn, error) {
	return c.dial(ctx, true)
}

func (c *Client) GetStatus(ctx context.Context) (Status, error) {
	now := time.Now()
	c.statusMu.Lock()
	if c.statusCached && now.Before(c.statusNextFetch) {
		status := c.statusCache
		err := c.statusErr
		c.statusMu.Unlock()
		return status, err
	}
	c.statusMu.Unlock()

	status, err := c.getStatusUncached(ctx)

	ttl := statusCacheOK
	if err != nil {
		ttl = statusCacheErr
		if isTimeoutError(err) {
			ttl = statusCacheTimeout
		}
	}

	c.statusMu.Lock()
	c.statusCache = status
	c.statusErr = err
	c.statusCached = true
	c.statusNextFetch = time.Now().Add(ttl)
	c.statusMu.Unlock()

	return status, err
}

func (c *Client) CachedPubkey() string {
	c.statusMu.Lock()
	cached := c.infoCache
	valid := c.infoCacheValid
	c.statusMu.Unlock()

	if !valid {
		return ""
	}
	return cached.Pubkey
}

func (c *Client) GetBalances(ctx context.Context) (BalanceSummary, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return BalanceSummary{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	summary := BalanceSummary{}
	walletOK := false
	channelOK := false
	var firstErr error

	wallet, err := client.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{})
	if err != nil {
		if isWalletLocked(err) {
			return summary, err
		}
		if firstErr == nil {
			firstErr = err
		}
		summary.Warnings = append(summary.Warnings, "On-chain balance unavailable")
	} else {
		summary.OnchainSat = wallet.TotalBalance
		summary.OnchainConfirmedSat = wallet.ConfirmedBalance
		summary.OnchainUnconfirmedSat = wallet.UnconfirmedBalance
		walletOK = true
	}

	channelBal, err := client.ChannelBalance(ctx, &lnrpc.ChannelBalanceRequest{})
	if err != nil {
		if isWalletLocked(err) {
			return summary, err
		}
		if firstErr == nil {
			firstErr = err
		}
		summary.Warnings = append(summary.Warnings, "Lightning balance unavailable")
	} else {
		summary.LightningSat = channelBal.Balance
		summary.LightningLocalSat = channelBal.Balance
		if local := channelBal.GetLocalBalance(); local != nil {
			summary.LightningLocalSat = int64(local.GetSat())
		}
		if unsettled := channelBal.GetUnsettledLocalBalance(); unsettled != nil {
			summary.LightningUnsettledLocalSat = int64(unsettled.GetSat())
		}
		channelOK = true
	}

	if !walletOK && !channelOK && firstErr != nil {
		return summary, firstErr
	}
	return summary, nil
}

func (c *Client) GetWalletBalanceDetails(ctx context.Context) (WalletBalanceDetails, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return WalletBalanceDetails{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{})
	if err != nil {
		return WalletBalanceDetails{}, err
	}
	if resp == nil {
		return WalletBalanceDetails{}, errors.New("wallet balance unavailable")
	}

	estimated := resp.GetTotalBalance() - resp.GetLockedBalance() - resp.GetReservedBalanceAnchorChan()
	if estimated < 0 {
		estimated = 0
	}

	return WalletBalanceDetails{
		TotalSat:              resp.GetTotalBalance(),
		ConfirmedSat:          resp.GetConfirmedBalance(),
		UnconfirmedSat:        resp.GetUnconfirmedBalance(),
		LockedSat:             resp.GetLockedBalance(),
		ReservedAnchorSat:     resp.GetReservedBalanceAnchorChan(),
		EstimatedSpendableSat: estimated,
	}, nil
}

func (c *Client) DecodeInvoice(ctx context.Context, payReq string) (DecodedInvoice, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return DecodedInvoice{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.DecodePayReq(ctx, &lnrpc.PayReqString{PayReq: payReq})
	if err != nil {
		return DecodedInvoice{}, err
	}

	return DecodedInvoice{
		AmountSat:   resp.NumSatoshis,
		AmountMsat:  resp.NumMsat,
		Memo:        resp.Description,
		Destination: resp.Destination,
		PaymentHash: strings.ToLower(resp.PaymentHash),
		Expiry:      resp.Expiry,
		Timestamp:   resp.Timestamp,
	}, nil
}

func (c *Client) ExportAllChannelBackups(ctx context.Context) ([]byte, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.ExportAllChannelBackups(ctx, &lnrpc.ChanBackupExportRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.MultiChanBackup == nil {
		return nil, errors.New("channel backup unavailable")
	}
	data := resp.MultiChanBackup.MultiChanBackup
	if len(data) == 0 {
		return nil, errors.New("channel backup empty")
	}
	return data, nil
}

func (c *Client) VerifyChannelBackup(ctx context.Context, backup []byte) ([]string, error) {
	if len(backup) == 0 {
		return nil, errors.New("channel backup empty")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.VerifyChanBackup(ctx, &lnrpc.ChanBackupSnapshot{
		MultiChanBackup: &lnrpc.MultiChanBackup{MultiChanBackup: backup},
	})
	if err != nil {
		return nil, err
	}
	return resp.GetChanPoints(), nil
}

func (c *Client) RestoreChannelBackups(ctx context.Context, backup []byte) (uint32, error) {
	if len(backup) == 0 {
		return 0, errors.New("channel backup empty")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.RestoreChannelBackups(ctx, &lnrpc.RestoreChanBackupRequest{
		Backup: &lnrpc.RestoreChanBackupRequest_MultiChanBackup{
			MultiChanBackup: backup,
		},
	})
	if err != nil {
		return 0, err
	}
	if resp == nil {
		return 0, errors.New("restore backup response unavailable")
	}
	return resp.GetNumRestored(), nil
}

func (c *Client) GetChannelPolicy(ctx context.Context, channelPoint string) (ChannelPolicy, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return ChannelPolicy{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
	if err != nil {
		return ChannelPolicy{}, err
	}

	var selected *lnrpc.Channel
	for _, ch := range channels.Channels {
		if ch.ChannelPoint == channelPoint {
			selected = ch
			break
		}
	}
	if selected == nil {
		return ChannelPolicy{}, errors.New("channel not found")
	}

	edge, err := client.GetChanInfo(ctx, &lnrpc.ChanInfoRequest{ChanId: selected.ChanId})
	if err != nil {
		return ChannelPolicy{}, err
	}

	policy := edge.Node1Policy
	if selected.RemotePubkey != "" {
		if edge.Node1Pub == selected.RemotePubkey {
			policy = edge.Node2Policy
		} else if edge.Node2Pub == selected.RemotePubkey {
			policy = edge.Node1Policy
		}
	}
	if policy == nil {
		return ChannelPolicy{}, errors.New("channel policy unavailable")
	}

	return ChannelPolicy{
		ChannelPoint:      channelPoint,
		BaseFeeMsat:       policy.FeeBaseMsat,
		FeeRatePpm:        policy.FeeRateMilliMsat,
		TimeLockDelta:     int64(policy.TimeLockDelta),
		MinHtlcMsat:       maxInt64ToUint64(policy.MinHtlc),
		MaxHtlcMsat:       policy.MaxHtlcMsat,
		InboundBaseMsat:   int64(policy.InboundFeeBaseMsat),
		InboundFeeRatePpm: int64(policy.InboundFeeRateMilliMsat),
	}, nil
}

func (c *Client) getStatusUncached(ctx context.Context) (Status, error) {
	now := time.Now()
	conn, err := c.dial(ctx, true)
	if err != nil {
		return Status{WalletState: "unknown"}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	status := Status{WalletState: "unknown"}
	var primaryErr error
	var cachedInfo infoSnapshot
	var cachedAt time.Time
	var cachedValid bool

	c.statusMu.Lock()
	cachedInfo = c.infoCache
	cachedAt = c.infoCacheAt
	cachedValid = c.infoCacheValid
	c.statusMu.Unlock()

	infoCtx, infoCancel := context.WithTimeout(ctx, 5*time.Second)
	info, err := client.GetInfo(infoCtx, &lnrpc.GetInfoRequest{})
	infoCancel()
	if err != nil {
		primaryErr = err
		if isWalletLocked(err) {
			status.WalletState = "locked"
		}
	} else {
		uris := uniqueStrings(info.Uris)
		status.ServiceActive = true
		status.WalletState = "unlocked"
		status.SyncedToChain = info.SyncedToChain
		status.SyncedToGraph = info.SyncedToGraph
		status.BlockHeight = int64(info.BlockHeight)
		status.Version = info.Version
		status.Pubkey = info.IdentityPubkey
		status.InfoKnown = true
		status.InfoStale = false
		status.InfoAgeSeconds = 0
		status.URIs = uris
		if len(uris) > 0 {
			status.URI = uris[0]
		}

		c.statusMu.Lock()
		c.infoCache = infoSnapshot{
			SyncedToChain: status.SyncedToChain,
			SyncedToGraph: status.SyncedToGraph,
			BlockHeight:   status.BlockHeight,
			Version:       status.Version,
			Pubkey:        status.Pubkey,
			URI:           status.URI,
			URIs:          append([]string(nil), status.URIs...),
		}
		c.infoCacheAt = now
		c.infoCacheValid = true
		c.statusMu.Unlock()
	}

	if !status.InfoKnown && cachedValid {
		status.SyncedToChain = cachedInfo.SyncedToChain
		status.SyncedToGraph = cachedInfo.SyncedToGraph
		status.BlockHeight = cachedInfo.BlockHeight
		status.Version = cachedInfo.Version
		status.Pubkey = cachedInfo.Pubkey
		status.URIs = uniqueStrings(cachedInfo.URIs)
		if len(status.URIs) == 0 {
			trimmedURI := strings.TrimSpace(cachedInfo.URI)
			if trimmedURI != "" {
				status.URIs = []string{trimmedURI}
			}
		}
		if len(status.URIs) > 0 {
			status.URI = status.URIs[0]
		} else {
			status.URI = strings.TrimSpace(cachedInfo.URI)
		}
		status.InfoKnown = true
		status.InfoStale = true
		status.InfoAgeSeconds = int64(now.Sub(cachedAt).Seconds())
	}

	channelsCtx, channelsCancel := context.WithTimeout(ctx, 5*time.Second)
	channels, err := client.ListChannels(channelsCtx, &lnrpc.ListChannelsRequest{})
	channelsCancel()
	if err == nil {
		active := 0
		inactive := 0
		for _, ch := range channels.Channels {
			if ch.Active {
				active++
			} else {
				inactive++
			}
		}
		status.ChannelsActive = active
		status.ChannelsInactive = inactive
		if status.WalletState == "unknown" {
			status.WalletState = "unlocked"
		}
	}

	walletCtx, walletCancel := context.WithTimeout(ctx, 5*time.Second)
	wallet, err := client.WalletBalance(walletCtx, &lnrpc.WalletBalanceRequest{})
	walletCancel()
	if err == nil {
		status.OnchainSat = wallet.TotalBalance
		if status.WalletState == "unknown" {
			status.WalletState = "unlocked"
		}
	}

	channelBalCtx, channelBalCancel := context.WithTimeout(ctx, 5*time.Second)
	channelBal, err := client.ChannelBalance(channelBalCtx, &lnrpc.ChannelBalanceRequest{})
	channelBalCancel()
	if err == nil {
		status.LightningSat = channelBal.Balance
		if status.WalletState == "unknown" {
			status.WalletState = "unlocked"
		}
	}

	return status, primaryErr
}

func (c *Client) GenSeed(ctx context.Context, seedPassphrase string) ([]string, error) {
	conn, err := c.dial(ctx, false)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewWalletUnlockerClient(conn)

	req := &lnrpc.GenSeedRequest{}
	if strings.TrimSpace(seedPassphrase) != "" {
		req.AezeedPassphrase = []byte(seedPassphrase)
	}
	resp, err := client.GenSeed(ctx, req)
	if err != nil {
		return nil, err
	}

	return resp.CipherSeedMnemonic, nil
}

func (c *Client) InitWallet(ctx context.Context, walletPassword string, seedWords []string) error {
	conn, err := c.dial(ctx, false)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewWalletUnlockerClient(conn)

	_, err = client.InitWallet(ctx, &lnrpc.InitWalletRequest{
		WalletPassword:     []byte(walletPassword),
		CipherSeedMnemonic: seedWords,
	})
	return err
}

func (c *Client) UnlockWallet(ctx context.Context, walletPassword string) error {
	conn, err := c.dial(ctx, false)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewWalletUnlockerClient(conn)

	_, err = client.UnlockWallet(ctx, &lnrpc.UnlockWalletRequest{WalletPassword: []byte(walletPassword)})
	return err
}

func (c *Client) CreateInvoice(ctx context.Context, amountSat int64, memo string, expirySeconds int64) (CreatedInvoice, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return CreatedInvoice{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	if expirySeconds <= 0 {
		expirySeconds = 3600
	}

	resp, err := client.AddInvoice(ctx, &lnrpc.Invoice{
		Memo:   memo,
		Value:  amountSat,
		Expiry: expirySeconds,
	})
	if err != nil {
		return CreatedInvoice{}, err
	}

	return CreatedInvoice{
		PaymentRequest: resp.PaymentRequest,
		PaymentHash:    strings.ToLower(hex.EncodeToString(resp.RHash)),
		PaymentAddr:    resp.PaymentAddr,
	}, nil
}

func (c *Client) NewAddress(ctx context.Context) (string, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	resp, err := client.NewAddress(ctx, &lnrpc.NewAddressRequest{
		Type: lnrpc.AddressType_WITNESS_PUBKEY_HASH,
	})
	if err != nil {
		return "", err
	}

	return resp.Address, nil
}

func (c *Client) PayInvoice(ctx context.Context, paymentRequest string, outgoingChanID uint64) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	req := &lnrpc.SendRequest{PaymentRequest: paymentRequest}
	if outgoingChanID > 0 {
		req.OutgoingChanId = outgoingChanID
	}
	res, err := client.SendPaymentSync(ctx, req)
	return sendPaymentSyncError(res, err)
}

func sendPaymentSyncError(res *lnrpc.SendResponse, err error) error {
	if err != nil {
		return err
	}
	if res != nil {
		if msg := strings.TrimSpace(res.PaymentError); msg != "" {
			return errors.New(msg)
		}
	}
	return nil
}

func (c *Client) SendCoins(ctx context.Context, address string, amountSat int64, satPerVbyte int64, sendAll bool) (string, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	req := &lnrpc.SendCoinsRequest{
		Addr:    address,
		SendAll: sendAll,
	}
	if !sendAll {
		req.Amount = amountSat
	}
	if satPerVbyte > 0 {
		req.SatPerVbyte = uint64(satPerVbyte)
	}

	resp, err := client.SendCoins(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return resp.Txid, nil
}

func (c *Client) SweepOutpoint(ctx context.Context, txid string, vout uint32, satPerVbyte int64) (string, string, error) {
	trimmedTxid := strings.ToLower(strings.TrimSpace(txid))
	if len(trimmedTxid) != 64 {
		return "", "", errors.New("invalid txid")
	}
	if _, err := hex.DecodeString(trimmedTxid); err != nil {
		return "", "", errors.New("invalid txid")
	}
	if satPerVbyte < 0 {
		return "", "", errors.New("sat_per_vbyte must be zero or positive")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	addrResp, err := client.NewAddress(ctx, &lnrpc.NewAddressRequest{
		Type: lnrpc.AddressType_WITNESS_PUBKEY_HASH,
	})
	if err != nil {
		return "", "", err
	}
	address := strings.TrimSpace(addrResp.GetAddress())
	if address == "" {
		return "", "", errors.New("failed to derive recovery address")
	}

	req := &lnrpc.SendCoinsRequest{
		Addr:    address,
		SendAll: true,
		Outpoints: []*lnrpc.OutPoint{
			{
				TxidStr:     trimmedTxid,
				OutputIndex: vout,
			},
		},
		SpendUnconfirmed: true,
		MinConfs:         0,
	}
	if satPerVbyte > 0 {
		req.SatPerVbyte = uint64(satPerVbyte)
	}

	resp, err := client.SendCoins(ctx, req)
	if err != nil {
		return "", address, err
	}
	if resp == nil {
		return "", address, nil
	}
	return strings.TrimSpace(resp.GetTxid()), address, nil
}

func (c *Client) ListRecent(ctx context.Context, limit int) ([]RecentActivity, error) {
	if limit <= 0 {
		limit = 20
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	invoices, invErr := client.ListInvoices(ctx, &lnrpc.ListInvoiceRequest{Reversed: true, NumMaxInvoices: uint64(limit)})
	payments, payErr := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true,
		MaxPayments:       uint64(limit),
		Reversed:          true,
	})
	pubkey := strings.TrimSpace(c.CachedPubkey())
	if pubkey == "" {
		if info, infoErr := client.GetInfo(ctx, &lnrpc.GetInfoRequest{}); infoErr == nil && info != nil {
			pubkey = strings.TrimSpace(info.IdentityPubkey)
		}
	}

	channelByID := map[uint64]ChannelInfo{}
	if channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true}); err == nil && channels != nil {
		channelByID = make(map[uint64]ChannelInfo, len(channels.Channels))
		for _, ch := range channels.Channels {
			if ch == nil || ch.ChanId == 0 {
				continue
			}
			channelByID[ch.ChanId] = ChannelInfo{
				ChannelID:    ch.ChanId,
				ChannelPoint: strings.TrimSpace(ch.ChannelPoint),
				RemotePubkey: strings.TrimSpace(ch.RemotePubkey),
				PeerAlias:    strings.TrimSpace(ch.PeerAlias),
			}
		}
	}

	applyRecentChannel := func(item *RecentActivity, chanID uint64, hopPubkey string) {
		if item == nil || chanID == 0 {
			return
		}
		item.ChannelID = chanID
		if info, ok := channelByID[chanID]; ok {
			item.ChannelPoint = info.ChannelPoint
			item.ChannelAlias = strings.TrimSpace(info.PeerAlias)
			if item.ChannelAlias == "" {
				item.ChannelAlias = shortRecentPubKey(info.RemotePubkey)
			}
		}
		if item.ChannelAlias == "" {
			item.ChannelAlias = shortRecentPubKey(hopPubkey)
		}
	}

	rebalanceHashes := map[string]struct{}{}
	if payErr == nil {
		for _, pay := range payments.Payments {
			if pay == nil || pay.Status != lnrpc.Payment_SUCCEEDED {
				continue
			}
			if isSelfPayment(ctx, pubkey, client, pay) {
				hash := strings.ToLower(strings.TrimSpace(pay.PaymentHash))
				if hash != "" {
					rebalanceHashes[hash] = struct{}{}
				}
			}
		}
	}

	var items []RecentActivity
	if invErr == nil {
		for _, inv := range invoices.Invoices {
			if inv.State != lnrpc.Invoice_SETTLED {
				continue
			}
			if isRebalanceMemo(inv.Memo) {
				continue
			}
			hash := ""
			if len(inv.RHash) > 0 {
				hash = hex.EncodeToString(inv.RHash)
			}
			if hash != "" {
				if _, ok := rebalanceHashes[strings.ToLower(strings.TrimSpace(hash))]; ok {
					continue
				}
			}
			item := RecentActivity{
				Type:        "invoice",
				Network:     "lightning",
				Direction:   "in",
				AmountSat:   inv.Value,
				Memo:        inv.Memo,
				Timestamp:   time.Unix(inv.CreationDate, 0).UTC(),
				Status:      inv.State.String(),
				Keysend:     inv.IsKeysend,
				PaymentHash: hash,
			}
			for _, htlc := range inv.Htlcs {
				if htlc == nil || htlc.ChanId == 0 {
					continue
				}
				applyRecentChannel(&item, htlc.ChanId, "")
				break
			}
			items = append(items, item)
		}
	}
	if payErr == nil {
		for _, pay := range payments.Payments {
			if pay.Status != lnrpc.Payment_SUCCEEDED {
				continue
			}
			if isSelfPayment(ctx, pubkey, client, pay) {
				continue
			}
			isKeysend := isKeysendPayment(pay)
			item := RecentActivity{
				Type:        "payment",
				Network:     "lightning",
				Direction:   "out",
				AmountSat:   pay.ValueSat,
				Memo:        pay.PaymentRequest,
				Timestamp:   time.Unix(pay.CreationDate, 0).UTC(),
				Status:      pay.Status.String(),
				Keysend:     isKeysend,
				PaymentHash: strings.ToLower(pay.PaymentHash),
			}
			if route := recentRouteFromPayment(pay); route != nil && len(route.Hops) > 0 {
				firstHop := route.Hops[0]
				applyRecentChannel(&item, firstHop.ChanId, firstHop.PubKey)
			}
			items = append(items, item)
		}
	}

	return items, nil
}

func recentRouteFromPayment(pay *lnrpc.Payment) *lnrpc.Route {
	if pay == nil {
		return nil
	}
	for _, attempt := range pay.Htlcs {
		if attempt == nil || attempt.Route == nil {
			continue
		}
		if attempt.Status == lnrpc.HTLCAttempt_SUCCEEDED {
			return attempt.Route
		}
	}
	for _, attempt := range pay.Htlcs {
		if attempt != nil && attempt.Route != nil {
			return attempt.Route
		}
	}
	return nil
}

func shortRecentPubKey(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

func isRebalanceMemo(memo string) bool {
	normalized := strings.ToLower(strings.TrimSpace(memo))
	if normalized == "" {
		return false
	}
	return strings.HasPrefix(normalized, "rebalance:") || strings.HasPrefix(normalized, "rebalance attempt")
}

func isSelfPayment(ctx context.Context, pubkey string, client lnrpc.LightningClient, pay *lnrpc.Payment) bool {
	if pay == nil || pubkey == "" {
		return false
	}

	trimmed := strings.TrimSpace(pay.PaymentRequest)
	if trimmed != "" {
		decoded, err := client.DecodePayReq(ctx, &lnrpc.PayReqString{PayReq: trimmed})
		if err == nil && decoded != nil && strings.EqualFold(decoded.Destination, pubkey) {
			return true
		}
	}

	route := rebalanceRouteFromPayment(pay)
	if route == nil {
		return false
	}
	hops := route.GetHops()
	if len(hops) == 0 {
		return false
	}
	lastHop := strings.TrimSpace(hops[len(hops)-1].PubKey)
	if lastHop == "" {
		return false
	}
	return strings.EqualFold(lastHop, pubkey)
}

func rebalanceRouteFromPayment(pay *lnrpc.Payment) *lnrpc.Route {
	if pay == nil {
		return nil
	}
	for _, attempt := range pay.Htlcs {
		if attempt == nil || attempt.Route == nil {
			continue
		}
		if attempt.Status == lnrpc.HTLCAttempt_SUCCEEDED {
			return attempt.Route
		}
	}
	for _, attempt := range pay.Htlcs {
		if attempt != nil && attempt.Route != nil {
			return attempt.Route
		}
	}
	return nil
}

func hasKeysendRecord(records map[uint64][]byte) bool {
	if len(records) == 0 {
		return false
	}
	if _, ok := records[KeysendPreimageRecord]; ok {
		return true
	}
	if _, ok := records[KeysendMessageRecord]; ok {
		return true
	}
	return false
}

func isKeysendPayment(pay *lnrpc.Payment) bool {
	if pay == nil {
		return false
	}
	if hasKeysendRecord(pay.FirstHopCustomRecords) {
		return true
	}
	for _, attempt := range pay.Htlcs {
		if attempt == nil || attempt.Route == nil {
			continue
		}
		for _, hop := range attempt.Route.Hops {
			if hop == nil {
				continue
			}
			if hasKeysendRecord(hop.CustomRecords) {
				return true
			}
		}
	}
	return false
}

func (c *Client) ListOnchain(ctx context.Context, limit int) ([]RecentActivity, error) {
	if limit <= 0 {
		limit = 20
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	var startHeight int32
	if info, infoErr := client.GetInfo(ctx, &lnrpc.GetInfoRequest{}); infoErr == nil && info != nil && info.BlockHeight > 0 {
		height := int64(info.BlockHeight)
		if height > recentOnchainWindowBlocks {
			startHeight = int32(height - recentOnchainWindowBlocks)
		}
	}
	req := &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     startHeight,
		EndHeight:       -1,
	}
	resp, err := client.GetTransactions(ctx, req)
	if err != nil {
		return nil, err
	}

	items := make([]RecentActivity, 0, len(resp.Transactions))
	for _, tx := range resp.Transactions {
		if tx == nil {
			continue
		}
		if tx.Amount == 0 {
			continue
		}
		amount := tx.Amount
		if amount == 0 {
			continue
		}
		direction := "in"
		if amount < 0 {
			direction = "out"
			amount = amount * -1
		}
		status := "PENDING"
		if tx.NumConfirmations > 0 {
			status = "CONFIRMED"
		}
		items = append(items, RecentActivity{
			Type:      "onchain",
			Network:   "onchain",
			Direction: direction,
			AmountSat: amount,
			Memo:      tx.Label,
			Timestamp: time.Unix(tx.TimeStamp, 0).UTC(),
			Status:    status,
			Txid:      tx.TxHash,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})
	if len(items) > limit {
		items = items[:limit]
	}

	return items, nil
}

func (c *Client) ListOnchainTransactions(ctx context.Context, limit int) ([]OnchainTransaction, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     0,
		EndHeight:       -1,
	}
	resp, err := client.GetTransactions(ctx, req)
	if err != nil {
		return nil, err
	}

	items := make([]OnchainTransaction, 0, len(resp.Transactions))
	for _, tx := range resp.Transactions {
		if tx == nil {
			continue
		}
		amount := tx.Amount
		direction := "in"
		if amount < 0 {
			direction = "out"
			amount = amount * -1
		}
		addresses := make([]string, 0, len(tx.OutputDetails))
		if len(tx.OutputDetails) > 0 {
			for _, out := range tx.OutputDetails {
				if out == nil {
					continue
				}
				if out.Address != "" {
					addresses = append(addresses, out.Address)
				}
			}
		}
		if len(addresses) == 0 && len(tx.DestAddresses) > 0 {
			addresses = append(addresses, tx.DestAddresses...)
		}
		items = append(items, OnchainTransaction{
			Txid:          tx.TxHash,
			Direction:     direction,
			AmountSat:     amount,
			FeeSat:        tx.TotalFees,
			Confirmations: tx.NumConfirmations,
			BlockHeight:   tx.BlockHeight,
			Timestamp:     time.Unix(tx.TimeStamp, 0).UTC(),
			Label:         tx.Label,
			Addresses:     uniqueStrings(addresses),
		})
	}

	return items, nil
}

func (c *Client) ListOnchainUtxos(ctx context.Context, minConfs int32, maxConfs int32) ([]OnchainUtxo, error) {
	if minConfs < 0 {
		minConfs = 0
	}
	if maxConfs < 0 {
		maxConfs = 0
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.ListUnspentRequest{
		MinConfs: minConfs,
		MaxConfs: maxConfs,
	}
	resp, err := client.ListUnspent(ctx, req)
	if err != nil {
		return nil, err
	}

	items := make([]OnchainUtxo, 0, len(resp.Utxos))
	for _, utxo := range resp.Utxos {
		if utxo == nil {
			continue
		}
		out := utxo.GetOutpoint()
		txid := ""
		vout := uint32(0)
		if out != nil {
			txid = out.TxidStr
			if txid == "" {
				txid = txidFromBytes(out.TxidBytes)
			}
			vout = out.OutputIndex
		}
		outpoint := ""
		if txid != "" {
			outpoint = fmt.Sprintf("%s:%d", txid, vout)
		}
		items = append(items, OnchainUtxo{
			Outpoint:      outpoint,
			Txid:          txid,
			Vout:          vout,
			Address:       utxo.Address,
			AddressType:   addressTypeLabel(utxo.AddressType),
			AmountSat:     utxo.AmountSat,
			Confirmations: utxo.Confirmations,
			PkScript:      utxo.PkScript,
		})
	}

	return items, nil
}

func (c *Client) IsOutpointUnspent(ctx context.Context, txid string, vout uint32) (bool, error) {
	targetTxid, err := normalizeTxidHex(txid)
	if err != nil {
		return false, err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.ListUnspent(ctx, &lnrpc.ListUnspentRequest{
		MinConfs: 0,
		MaxConfs: 2147483647,
	})
	if err != nil {
		return false, err
	}

	for _, utxo := range resp.GetUtxos() {
		if utxo == nil {
			continue
		}
		out := utxo.GetOutpoint()
		if out == nil {
			continue
		}
		utxoTxid := strings.TrimSpace(out.GetTxidStr())
		if utxoTxid == "" {
			utxoTxid = txidFromBytes(out.GetTxidBytes())
		}
		if !strings.EqualFold(targetTxid, strings.TrimSpace(utxoTxid)) {
			continue
		}
		if out.GetOutputIndex() == vout {
			return true, nil
		}
	}

	return false, nil
}

func (c *Client) FindSpendingTransactionByOutpoint(ctx context.Context, txid string, vout uint32) (string, bool, error) {
	targetTxid, err := normalizeTxidHex(txid)
	if err != nil {
		return "", false, err
	}
	targetOutpoint := fmt.Sprintf("%s:%d", targetTxid, vout)
	targetHash, err := chainhash.NewHashFromStr(targetTxid)
	if err != nil {
		return "", false, err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     0,
		EndHeight:       -1,
	})
	if err != nil {
		return "", false, err
	}

	for _, tx := range resp.GetTransactions() {
		if tx == nil {
			continue
		}
		for _, prev := range tx.GetPreviousOutpoints() {
			if prev == nil {
				continue
			}
			outpoint := strings.ToLower(strings.TrimSpace(prev.GetOutpoint()))
			if outpoint == targetOutpoint {
				return strings.TrimSpace(tx.GetTxHash()), true, nil
			}
		}

		rawTxHex := strings.TrimSpace(tx.GetRawTxHex())
		if rawTxHex == "" {
			continue
		}
		rawTx, decodeErr := hex.DecodeString(rawTxHex)
		if decodeErr != nil || len(rawTx) == 0 {
			continue
		}
		var msgTx wire.MsgTx
		if err := msgTx.Deserialize(bytes.NewReader(rawTx)); err != nil {
			continue
		}
		for _, in := range msgTx.TxIn {
			if in == nil {
				continue
			}
			prev := in.PreviousOutPoint
			if prev.Index != vout {
				continue
			}
			if !prev.Hash.IsEqual(targetHash) {
				continue
			}
			hash := strings.TrimSpace(tx.GetTxHash())
			if hash == "" {
				hash = msgTx.TxHash().String()
			}
			return hash, true, nil
		}
	}

	return "", false, nil
}

func (c *Client) ListChannels(ctx context.Context) ([]ChannelInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	resp, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	inactiveSinceByPoint := c.snapshotInactiveSince(resp.Channels, now)
	channelAliasByID := make(map[uint64]string, len(resp.Channels))
	for _, listed := range resp.Channels {
		if listed == nil || listed.ChanId == 0 {
			continue
		}
		if alias := strings.TrimSpace(listed.PeerAlias); alias != "" {
			channelAliasByID[listed.ChanId] = alias
		}
	}

	channels := make([]ChannelInfo, 0, len(resp.Channels))
	for _, ch := range resp.Channels {
		if ch == nil {
			continue
		}
		var baseFeeMsat *int64
		var feeRatePpm *int64
		var inboundFeeRatePpm *int64
		var peerFeeRatePpm *int64
		var peerBaseMsat *int64
		localDisabled := isLocalChanDisabledFlags(ch.ChanStatusFlags)
		localReserveSat := ch.LocalChanReserveSat
		if localReserveSat <= 0 && ch.LocalConstraints != nil {
			if reserve := int64(ch.LocalConstraints.GetChanReserveSat()); reserve > 0 {
				localReserveSat = reserve
			}
		}

		if edge, err := client.GetChanInfo(ctx, &lnrpc.ChanInfoRequest{ChanId: ch.ChanId}); err == nil && edge != nil {
			localPolicy := edge.Node1Policy
			remotePolicy := edge.Node2Policy
			if ch.RemotePubkey != "" {
				if edge.Node1Pub == ch.RemotePubkey {
					localPolicy = edge.Node2Policy
					remotePolicy = edge.Node1Policy
				} else if edge.Node2Pub == ch.RemotePubkey {
					localPolicy = edge.Node1Policy
					remotePolicy = edge.Node2Policy
				}
			}
			if localPolicy != nil {
				base := int64(localPolicy.FeeBaseMsat)
				rate := int64(localPolicy.FeeRateMilliMsat)
				inbound := int64(localPolicy.InboundFeeRateMilliMsat)
				baseFeeMsat = &base
				feeRatePpm = &rate
				inboundFeeRatePpm = &inbound
				if localPolicy.Disabled {
					localDisabled = true
				}
			}
			if remotePolicy != nil {
				peerRate := int64(remotePolicy.FeeRateMilliMsat)
				peerBase := int64(remotePolicy.FeeBaseMsat)
				peerFeeRatePpm = &peerRate
				peerBaseMsat = &peerBase
			}
		}

		pendingHtlcs := make([]ChannelPendingHtlcInfo, 0, len(ch.PendingHtlcs))
		for _, htlc := range ch.PendingHtlcs {
			if htlc == nil {
				continue
			}
			forwardingChannelID := htlc.ForwardingChannel
			pendingHtlcs = append(pendingHtlcs, ChannelPendingHtlcInfo{
				Incoming:            htlc.Incoming,
				PeerAlias:           channelAliasByID[forwardingChannelID],
				AmountSat:           htlc.Amount,
				ExpirationHeight:    htlc.ExpirationHeight,
				HtlcIndex:           htlc.HtlcIndex,
				ForwardingChannelID: forwardingChannelID,
				LockedIn:            htlc.LockedIn,
			})
		}
		sort.Slice(pendingHtlcs, func(i, j int) bool {
			if pendingHtlcs[i].ExpirationHeight == pendingHtlcs[j].ExpirationHeight {
				return pendingHtlcs[i].HtlcIndex < pendingHtlcs[j].HtlcIndex
			}
			return pendingHtlcs[i].ExpirationHeight < pendingHtlcs[j].ExpirationHeight
		})

		inactiveSinceUnix := int64(0)
		inactiveDurationSec := int64(0)
		if !ch.Active {
			key := normalizeChannelPointKey(ch.ChannelPoint)
			if since, ok := inactiveSinceByPoint[key]; ok && !since.IsZero() {
				inactiveSinceUnix = since.Unix()
				if now.After(since) {
					inactiveDurationSec = int64(now.Sub(since).Seconds())
				}
			}
		}

		channels = append(channels, ChannelInfo{
			ChannelPoint:        ch.ChannelPoint,
			ChannelID:           ch.ChanId,
			RemotePubkey:        ch.RemotePubkey,
			PeerAlias:           ch.PeerAlias,
			Initiator:           ch.Initiator,
			Active:              ch.Active,
			InactiveSinceUnix:   inactiveSinceUnix,
			InactiveDurationSec: inactiveDurationSec,
			ChanStatusFlags:     ch.ChanStatusFlags,
			LocalDisabled:       localDisabled,
			Private:             ch.Private,
			CapacitySat:         ch.Capacity,
			LocalBalanceSat:     ch.LocalBalance,
			RemoteBalanceSat:    ch.RemoteBalance,
			LocalChanReserveSat: localReserveSat,
			UnsettledBalanceSat: ch.UnsettledBalance,
			PendingHtlcCount:    len(ch.PendingHtlcs),
			PendingHtlcs:        pendingHtlcs,
			BaseFeeMsat:         baseFeeMsat,
			FeeRatePpm:          feeRatePpm,
			InboundFeeRatePpm:   inboundFeeRatePpm,
			PeerFeeRatePpm:      peerFeeRatePpm,
			PeerBaseMsat:        peerBaseMsat,
		})
	}

	return channels, nil
}

func (c *Client) ListPendingChannels(ctx context.Context) ([]PendingChannelInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.PendingChannels(ctx, &lnrpc.PendingChannelsRequest{})
	if err != nil {
		return nil, err
	}

	aliasMap := map[string]string{}
	if channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true}); err == nil {
		for _, ch := range channels.Channels {
			if ch.RemotePubkey != "" && ch.PeerAlias != "" {
				aliasMap[ch.RemotePubkey] = ch.PeerAlias
			}
		}
	}

	resolveAlias := func(pubkey string) string {
		if pubkey == "" {
			return ""
		}
		if alias := aliasMap[pubkey]; alias != "" {
			return alias
		}
		if alias := c.lookupNodeAliasWithClient(ctx, client, pubkey); alias != "" {
			aliasMap[pubkey] = alias
			return alias
		}
		return ""
	}
	pending := []PendingChannelInfo{}
	for _, item := range resp.PendingOpenChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		ch := item.Channel
		pending = append(pending, PendingChannelInfo{
			ChannelPoint:             ch.ChannelPoint,
			RemotePubkey:             ch.RemoteNodePub,
			PeerAlias:                resolveAlias(ch.RemoteNodePub),
			CapacitySat:              ch.Capacity,
			LocalBalanceSat:          ch.LocalBalance,
			RemoteBalanceSat:         ch.RemoteBalance,
			Status:                   "opening",
			ConfirmationsUntilActive: item.ConfirmationsUntilActive,
			ConfirmationHeight:       item.ConfirmationHeight,
			Private:                  ch.Private,
		})
	}

	for _, item := range resp.PendingClosingChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		ch := item.Channel
		pending = append(pending, PendingChannelInfo{
			ChannelPoint:     ch.ChannelPoint,
			RemotePubkey:     ch.RemoteNodePub,
			PeerAlias:        resolveAlias(ch.RemoteNodePub),
			CapacitySat:      ch.Capacity,
			LocalBalanceSat:  ch.LocalBalance,
			RemoteBalanceSat: ch.RemoteBalance,
			Status:           "closing",
			ClosingTxid:      strings.TrimSpace(item.ClosingTxid),
			Private:          ch.Private,
		})
	}

	for _, item := range resp.PendingForceClosingChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		ch := item.Channel
		pending = append(pending, PendingChannelInfo{
			ChannelPoint:      ch.ChannelPoint,
			RemotePubkey:      ch.RemoteNodePub,
			PeerAlias:         resolveAlias(ch.RemoteNodePub),
			CapacitySat:       ch.Capacity,
			LocalBalanceSat:   ch.LocalBalance,
			RemoteBalanceSat:  ch.RemoteBalance,
			Status:            "force_closing",
			ClosingTxid:       strings.TrimSpace(item.ClosingTxid),
			BlocksTilMaturity: item.BlocksTilMaturity,
			LimboBalance:      item.LimboBalance,
			Private:           ch.Private,
		})
	}

	for _, item := range resp.WaitingCloseChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		ch := item.Channel
		pending = append(pending, PendingChannelInfo{
			ChannelPoint:     ch.ChannelPoint,
			RemotePubkey:     ch.RemoteNodePub,
			PeerAlias:        resolveAlias(ch.RemoteNodePub),
			CapacitySat:      ch.Capacity,
			LocalBalanceSat:  ch.LocalBalance,
			RemoteBalanceSat: ch.RemoteBalance,
			Status:           "waiting_close",
			ClosingTxid:      strings.TrimSpace(item.ClosingTxid),
			LimboBalance:     item.LimboBalance,
			Private:          ch.Private,
		})
	}

	return pending, nil
}

func (c *Client) ListClosedChannels(ctx context.Context) ([]ClosedChannelInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.ClosedChannels(ctx, &lnrpc.ClosedChannelsRequest{})
	if err != nil {
		return nil, err
	}

	txTimeByID := make(map[string]string)
	if txResp, txErr := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     0,
		EndHeight:       -1,
	}); txErr == nil && txResp != nil {
		for _, tx := range txResp.GetTransactions() {
			if tx == nil {
				continue
			}
			txid := strings.ToLower(strings.TrimSpace(tx.GetTxHash()))
			if txid == "" || tx.GetTimeStamp() <= 0 {
				continue
			}
			txTimeByID[txid] = time.Unix(tx.GetTimeStamp(), 0).UTC().Format(time.RFC3339)
		}
	}

	aliasMap := map[string]string{}
	items := make([]ClosedChannelInfo, 0, len(resp.GetChannels()))
	for _, ch := range resp.GetChannels() {
		if ch == nil {
			continue
		}
		remotePubkey := strings.TrimSpace(ch.GetRemotePubkey())
		peerAlias := aliasMap[remotePubkey]
		if peerAlias == "" && remotePubkey != "" {
			peerAlias = c.lookupNodeAliasWithClient(ctx, client, remotePubkey)
			if peerAlias != "" {
				aliasMap[remotePubkey] = peerAlias
			}
		}
		resolutions := make([]ClosedChannelResolutionInfo, 0, len(ch.GetResolutions()))
		for _, res := range ch.GetResolutions() {
			if res == nil {
				continue
			}
			resolutions = append(resolutions, ClosedChannelResolutionInfo{
				ResolutionType: int32(res.GetResolutionType()),
				SweepTxid:      strings.ToLower(strings.TrimSpace(res.GetSweepTxid())),
			})
		}
		items = append(items, ClosedChannelInfo{
			ChannelPoint:         strings.TrimSpace(ch.GetChannelPoint()),
			ChanID:               ch.GetChanId(),
			ClosedAt:             txTimeByID[strings.ToLower(strings.TrimSpace(ch.GetClosingTxHash()))],
			ClosingTxHash:        strings.ToLower(strings.TrimSpace(ch.GetClosingTxHash())),
			RemotePubkey:         remotePubkey,
			PeerAlias:            peerAlias,
			CapacitySat:          ch.GetCapacity(),
			SettledBalanceSat:    ch.GetSettledBalance(),
			TimeLockedBalanceSat: ch.GetTimeLockedBalance(),
			CloseType:            int32(ch.GetCloseType()),
			CloseTypeLabel:       ch.GetCloseType().String(),
			OpenInitiator:        int32(ch.GetOpenInitiator()),
			OpenInitiatorLabel:   ch.GetOpenInitiator().String(),
			CloseInitiator:       int32(ch.GetCloseInitiator()),
			CloseInitiatorLabel:  ch.GetCloseInitiator().String(),
			CloseHeight:          ch.GetCloseHeight(),
			Resolutions:          resolutions,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CloseHeight == items[j].CloseHeight {
			return items[i].ChanID > items[j].ChanID
		}
		return items[i].CloseHeight > items[j].CloseHeight
	})

	return items, nil
}

func (c *Client) ListPeers(ctx context.Context) ([]PeerInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	resp, err := client.ListPeers(ctx, &lnrpc.ListPeersRequest{LatestError: true})
	if err != nil {
		return nil, err
	}

	aliasMap := map[string]string{}
	if channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true}); err == nil {
		for _, ch := range channels.Channels {
			if ch.RemotePubkey != "" && ch.PeerAlias != "" {
				aliasMap[ch.RemotePubkey] = ch.PeerAlias
			}
		}
	}

	peers := make([]PeerInfo, 0, len(resp.Peers))
	for _, peer := range resp.Peers {
		alias := aliasMap[peer.PubKey]
		if alias == "" {
			alias = c.lookupNodeAliasWithClient(ctx, client, peer.PubKey)
		}
		lastErr := ""
		lastErrTime := int64(0)
		if len(peer.Errors) > 0 {
			if last := peer.Errors[len(peer.Errors)-1]; last != nil {
				lastErr = last.Error
				lastErrTime = int64(last.Timestamp)
			}
		}
		peers = append(peers, PeerInfo{
			PubKey:        peer.PubKey,
			Alias:         alias,
			Address:       peer.Address,
			Inbound:       peer.Inbound,
			BytesSent:     peer.BytesSent,
			BytesRecv:     peer.BytesRecv,
			SatSent:       peer.SatSent,
			SatRecv:       peer.SatRecv,
			PingTime:      peer.PingTime,
			SyncType:      peer.SyncType.String(),
			LastError:     lastErr,
			LastErrorTime: lastErrTime,
		})
	}

	return peers, nil
}

func (c *Client) ListWatchtowers(ctx context.Context, includeSessions bool, excludeExhaustedSessions bool) ([]WatchtowerInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := wtclientrpc.NewWatchtowerClientClient(conn)
	resp, err := client.ListTowers(ctx, &wtclientrpc.ListTowersRequest{
		IncludeSessions:          includeSessions,
		ExcludeExhaustedSessions: excludeExhaustedSessions,
	})
	if err != nil {
		return nil, err
	}

	towers := make([]WatchtowerInfo, 0, len(resp.GetTowers()))
	for _, item := range resp.GetTowers() {
		if item == nil {
			continue
		}

		activeSessionCandidate := item.GetActiveSessionCandidate()
		numSessions := int(item.GetNumSessions())
		if sessionInfo := item.GetSessionInfo(); len(sessionInfo) > 0 {
			activeSessionCandidate = false
			numSessions = 0
			for _, sess := range sessionInfo {
				if sess == nil {
					continue
				}
				numSessions += int(sess.GetNumSessions())
				if sess.GetActiveSessionCandidate() {
					activeSessionCandidate = true
				}
			}
		}

		towers = append(towers, WatchtowerInfo{
			Pubkey:                 strings.ToLower(hex.EncodeToString(item.GetPubkey())),
			Addresses:              uniqueStrings(item.GetAddresses()),
			ActiveSessionCandidate: activeSessionCandidate,
			NumSessions:            numSessions,
		})
	}

	sort.Slice(towers, func(i, j int) bool {
		return towers[i].Pubkey < towers[j].Pubkey
	})

	return towers, nil
}

func (c *Client) AddWatchtower(ctx context.Context, pubkeyHex string, address string) error {
	pubkeyHex = strings.TrimSpace(pubkeyHex)
	if pubkeyHex == "" {
		return errors.New("pubkey required")
	}
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("address required")
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := wtclientrpc.NewWatchtowerClientClient(conn)
	_, err = client.AddTower(ctx, &wtclientrpc.AddTowerRequest{
		Pubkey:  pubkey,
		Address: address,
	})
	return err
}

func (c *Client) RemoveWatchtower(ctx context.Context, pubkeyHex string, address string) error {
	pubkeyHex = strings.TrimSpace(pubkeyHex)
	if pubkeyHex == "" {
		return errors.New("pubkey required")
	}

	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey hex")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := wtclientrpc.NewWatchtowerClientClient(conn)
	_, err = client.RemoveTower(ctx, &wtclientrpc.RemoveTowerRequest{
		Pubkey:  pubkey,
		Address: strings.TrimSpace(address),
	})
	return err
}

func (c *Client) ConnectPeer(ctx context.Context, pubkey string, host string, perm bool) error {
	return c.ConnectPeerWithTimeout(ctx, pubkey, host, perm, defaultConnectPeerTimeoutSec)
}

func (c *Client) ConnectPeerWithTimeout(ctx context.Context, pubkey string, host string, perm bool, timeoutSec uint64) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	if timeoutSec == 0 {
		timeoutSec = defaultConnectPeerTimeoutSec
	}
	_, err = client.ConnectPeer(ctx, &lnrpc.ConnectPeerRequest{
		Addr: &lnrpc.LightningAddress{
			Pubkey: pubkey,
			Host:   host,
		},
		Perm:    perm,
		Timeout: timeoutSec,
	})
	return err
}

func (c *Client) DisconnectPeer(ctx context.Context, pubkey string) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	_, err = client.DisconnectPeer(ctx, &lnrpc.DisconnectPeerRequest{PubKey: pubkey})
	return err
}

func (c *Client) GetNodeDetails(ctx context.Context, pubkey string) (NodeDetails, error) {
	trimmed := strings.TrimSpace(pubkey)
	if trimmed == "" {
		return NodeDetails{}, errors.New("pubkey required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return NodeDetails{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: trimmed, IncludeChannels: false})
	if err != nil {
		return NodeDetails{}, err
	}
	node := info.GetNode()
	if node == nil {
		return NodeDetails{}, errors.New("node not found")
	}

	addresses := make([]NodeAddress, 0, len(node.Addresses))
	for _, item := range node.Addresses {
		if item == nil {
			continue
		}
		addresses = append(addresses, NodeAddress{
			Network: item.Network,
			Addr:    item.Addr,
		})
	}

	return NodeDetails{
		PubKey:    trimmed,
		Alias:     node.Alias,
		Addresses: addresses,
	}, nil
}

func (c *Client) GetPeerNeighborRecommendations(ctx context.Context, sourcePubkey string, excludePubkeys map[string]struct{}, limit int) ([]PeerNeighborRecommendation, error) {
	trimmed := strings.TrimSpace(sourcePubkey)
	if trimmed == "" {
		return nil, errors.New("source pubkey required")
	}

	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: trimmed, IncludeChannels: true})
	if err != nil {
		return nil, err
	}

	recommendations := buildPeerNeighborRecommendations(trimmed, info.GetChannels(), excludePubkeys)
	if len(recommendations) > limit {
		recommendations = recommendations[:limit]
	}

	for i := range recommendations {
		nodeInfo, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: recommendations[i].PubKey, IncludeChannels: false})
		if err != nil || nodeInfo == nil || nodeInfo.GetNode() == nil {
			continue
		}
		node := nodeInfo.GetNode()
		if alias := strings.TrimSpace(node.GetAlias()); alias != "" {
			recommendations[i].Alias = alias
		}
		host := selectPreferredNodeAddress(node.GetAddresses())
		if host != "" {
			recommendations[i].Host = host
			recommendations[i].PeerAddress = recommendations[i].PubKey + "@" + host
		}
	}

	return recommendations, nil
}

type peerNeighborAggregate struct {
	PubKey             string
	ChannelCount       int
	TotalCapacitySat   int64
	LargestCapacitySat int64
	InboundBaseMsat    int64
	InboundFeeRatePpm  int64
	OutboundBaseMsat   int64
	OutboundFeeRatePpm int64
	HasInboundPolicy   bool
	HasOutboundPolicy  bool
}

func buildPeerNeighborRecommendations(sourcePubkey string, edges []*lnrpc.ChannelEdge, excludePubkeys map[string]struct{}) []PeerNeighborRecommendation {
	normalizedSource := normalizePubkeyCacheKey(sourcePubkey)
	if normalizedSource == "" {
		return nil
	}

	excluded := make(map[string]struct{}, len(excludePubkeys)+1)
	for pubkey := range excludePubkeys {
		key := normalizePubkeyCacheKey(pubkey)
		if key == "" {
			continue
		}
		excluded[key] = struct{}{}
	}
	excluded[normalizedSource] = struct{}{}

	aggregates := make(map[string]*peerNeighborAggregate)
	for _, edge := range edges {
		if edge == nil || edge.GetCapacity() <= 0 {
			continue
		}

		node1 := normalizePubkeyCacheKey(edge.GetNode1Pub())
		node2 := normalizePubkeyCacheKey(edge.GetNode2Pub())

		var (
			otherPubkey string
			otherPolicy *lnrpc.RoutingPolicy
		)
		switch normalizedSource {
		case node1:
			otherPubkey = node2
			otherPolicy = edge.GetNode2Policy()
		case node2:
			otherPubkey = node1
			otherPolicy = edge.GetNode1Policy()
		default:
			continue
		}

		if otherPubkey == "" {
			continue
		}
		if _, skip := excluded[otherPubkey]; skip {
			continue
		}
		if otherPolicy == nil || otherPolicy.GetDisabled() {
			continue
		}

		item := aggregates[otherPubkey]
		if item == nil {
			item = &peerNeighborAggregate{PubKey: otherPubkey}
			aggregates[otherPubkey] = item
		}

		item.ChannelCount++
		item.TotalCapacitySat += edge.GetCapacity()
		if edge.GetCapacity() > item.LargestCapacitySat {
			item.LargestCapacitySat = edge.GetCapacity()
		}

		inboundRate := int64(otherPolicy.GetInboundFeeRateMilliMsat())
		if !item.HasInboundPolicy || inboundRate < item.InboundFeeRatePpm {
			item.InboundFeeRatePpm = inboundRate
			item.InboundBaseMsat = int64(otherPolicy.GetInboundFeeBaseMsat())
			item.HasInboundPolicy = true
		}

		outboundRate := int64(otherPolicy.GetFeeRateMilliMsat())
		if !item.HasOutboundPolicy || outboundRate < item.OutboundFeeRatePpm {
			item.OutboundFeeRatePpm = outboundRate
			item.OutboundBaseMsat = otherPolicy.GetFeeBaseMsat()
			item.HasOutboundPolicy = true
		}
	}

	list := make([]PeerNeighborRecommendation, 0, len(aggregates))
	for _, item := range aggregates {
		if item == nil || item.ChannelCount <= 0 {
			continue
		}
		list = append(list, PeerNeighborRecommendation{
			PubKey:             item.PubKey,
			ChannelCount:       item.ChannelCount,
			TotalCapacitySat:   item.TotalCapacitySat,
			LargestCapacitySat: item.LargestCapacitySat,
			InboundBaseMsat:    item.InboundBaseMsat,
			InboundFeeRatePpm:  item.InboundFeeRatePpm,
			OutboundBaseMsat:   item.OutboundBaseMsat,
			OutboundFeeRatePpm: item.OutboundFeeRatePpm,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].InboundFeeRatePpm != list[j].InboundFeeRatePpm {
			return list[i].InboundFeeRatePpm < list[j].InboundFeeRatePpm
		}
		if list[i].TotalCapacitySat != list[j].TotalCapacitySat {
			return list[i].TotalCapacitySat > list[j].TotalCapacitySat
		}
		if list[i].LargestCapacitySat != list[j].LargestCapacitySat {
			return list[i].LargestCapacitySat > list[j].LargestCapacitySat
		}
		if list[i].ChannelCount != list[j].ChannelCount {
			return list[i].ChannelCount > list[j].ChannelCount
		}
		if list[i].OutboundFeeRatePpm != list[j].OutboundFeeRatePpm {
			return list[i].OutboundFeeRatePpm < list[j].OutboundFeeRatePpm
		}
		return list[i].PubKey < list[j].PubKey
	})

	return list
}

func selectPreferredNodeAddress(addresses []*lnrpc.NodeAddress) string {
	fallback := ""
	for _, item := range addresses {
		if item == nil {
			continue
		}
		addr := strings.TrimSpace(item.GetAddr())
		if addr == "" {
			continue
		}
		if fallback == "" {
			fallback = addr
		}
		if !strings.Contains(strings.ToLower(addr), ".onion") {
			return addr
		}
	}
	return fallback
}

func (c *Client) OpenChannel(ctx context.Context, pubkeyHex string, localFundingSat int64, closeAddress string, private bool, satPerVbyte int64) (string, error) {
	return c.OpenChannelWithPush(ctx, pubkeyHex, localFundingSat, 0, closeAddress, private, satPerVbyte)
}

func (c *Client) OpenChannelWithPush(ctx context.Context, pubkeyHex string, localFundingSat int64, pushSat int64, closeAddress string, private bool, satPerVbyte int64) (string, error) {
	pubkeyHex = strings.TrimSpace(pubkeyHex)
	if pubkeyHex == "" {
		return "", errors.New("pubkey required")
	}
	if localFundingSat <= 0 {
		return "", errors.New("local funding must be positive")
	}
	if pushSat < 0 {
		return "", errors.New("push amount must be zero or positive")
	}
	if pushSat > localFundingSat {
		return "", errors.New("push amount cannot exceed local funding")
	}
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return "", fmt.Errorf("invalid pubkey hex")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.OpenChannelRequest{
		NodePubkey:         pubkey,
		LocalFundingAmount: localFundingSat,
		PushSat:            pushSat,
		Private:            private,
	}
	if satPerVbyte > 0 {
		req.SatPerVbyte = uint64(satPerVbyte)
	}
	if strings.TrimSpace(closeAddress) != "" {
		req.CloseAddress = strings.TrimSpace(closeAddress)
	}
	resp, err := client.OpenChannelSync(ctx, req)
	if err != nil {
		return "", err
	}

	return channelPointString(resp), nil
}

func (c *Client) DeriveNextKey(ctx context.Context, family int32) (DerivedKey, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return DerivedKey{}, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	key, err := client.DeriveNextKey(ctx, &walletrpc.KeyReq{KeyFamily: family})
	if err != nil {
		return DerivedKey{}, err
	}
	if key == nil {
		return DerivedKey{}, errors.New("empty derived key")
	}

	loc := key.GetKeyLoc()
	out := DerivedKey{
		PublicKey: hex.EncodeToString(key.GetRawKeyBytes()),
	}
	if loc != nil {
		out.Family = loc.GetKeyFamily()
		out.Index = loc.GetKeyIndex()
	}
	return out, nil
}

func (c *Client) SendOutputScript(ctx context.Context, params SendOutputScriptParams) (OutputScriptSendResult, error) {
	if params.AmountSat <= 0 {
		return OutputScriptSendResult{}, errors.New("amount must be positive")
	}
	scriptHex := strings.TrimSpace(params.OutputScriptHex)
	if scriptHex == "" {
		return OutputScriptSendResult{}, errors.New("output script required")
	}
	outputScript, err := hex.DecodeString(scriptHex)
	if err != nil || len(outputScript) == 0 {
		return OutputScriptSendResult{}, errors.New("invalid output script")
	}

	satPerVByte := params.SatPerVbyte
	if satPerVByte <= 0 {
		satPerVByte = 1
	}
	satPerKw := satPerVByte * 250

	conn, err := c.dial(ctx, true)
	if err != nil {
		return OutputScriptSendResult{}, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.SendOutputs(ctx, &walletrpc.SendOutputsRequest{
		SatPerKw: satPerKw,
		Outputs: []*signrpc.TxOut{
			{
				Value:    params.AmountSat,
				PkScript: outputScript,
			},
		},
		Label:            strings.TrimSpace(params.Label),
		MinConfs:         params.MinConfs,
		SpendUnconfirmed: params.SpendUnconfirmed,
	})
	if err != nil {
		return OutputScriptSendResult{}, err
	}
	if resp == nil || len(resp.GetRawTx()) == 0 {
		return OutputScriptSendResult{}, errors.New("empty send outputs transaction")
	}

	rawTx := resp.GetRawTx()
	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return OutputScriptSendResult{}, err
	}

	vout := -1
	for idx, out := range tx.TxOut {
		if out == nil {
			continue
		}
		if out.Value != params.AmountSat {
			continue
		}
		if !bytes.Equal(out.PkScript, outputScript) {
			continue
		}
		vout = idx
		break
	}
	if vout < 0 {
		return OutputScriptSendResult{}, errors.New("output not found in send outputs transaction")
	}

	return OutputScriptSendResult{
		TxID:     tx.TxHash().String(),
		Vout:     uint32(vout),
		RawTxHex: hex.EncodeToString(rawTx),
	}, nil
}

func (c *Client) ComputeInputScript(ctx context.Context, params ComputeInputScriptParams) (ComputedInputScript, error) {
	txHex := strings.TrimSpace(params.RawTxHex)
	if txHex == "" {
		return ComputedInputScript{}, errors.New("raw tx required")
	}
	rawTx, err := hex.DecodeString(txHex)
	if err != nil || len(rawTx) == 0 {
		return ComputedInputScript{}, errors.New("invalid raw tx")
	}
	if params.OutputSat <= 0 {
		return ComputedInputScript{}, errors.New("output sat must be positive")
	}

	outputScriptHex := strings.TrimSpace(params.OutputScriptHex)
	if outputScriptHex == "" {
		return ComputedInputScript{}, errors.New("output script required")
	}
	outputScript, err := hex.DecodeString(outputScriptHex)
	if err != nil || len(outputScript) == 0 {
		return ComputedInputScript{}, errors.New("invalid output script")
	}

	keyRaw, err := hex.DecodeString(strings.TrimSpace(params.Key.PublicKey))
	if err != nil || len(keyRaw) == 0 {
		return ComputedInputScript{}, errors.New("invalid signing key")
	}

	desc := &signrpc.SignDescriptor{
		KeyDesc: &signrpc.KeyDescriptor{
			RawKeyBytes: keyRaw,
			KeyLoc: &signrpc.KeyLocator{
				KeyFamily: params.Key.Family,
				KeyIndex:  params.Key.Index,
			},
		},
		Output: &signrpc.TxOut{
			Value:    params.OutputSat,
			PkScript: outputScript,
		},
		Sighash:    params.SighashType,
		InputIndex: int32(params.InputIndex),
		SignMethod: params.SignMethod,
	}
	if desc.Sighash == 0 {
		desc.Sighash = 1
	}

	if witnessScriptHex := strings.TrimSpace(params.WitnessScriptHex); witnessScriptHex != "" {
		witnessScript, err := hex.DecodeString(witnessScriptHex)
		if err != nil {
			return ComputedInputScript{}, errors.New("invalid witness script")
		}
		desc.WitnessScript = witnessScript
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return ComputedInputScript{}, err
	}
	defer conn.Close()

	client := signrpc.NewSignerClient(conn)
	resp, err := client.ComputeInputScript(ctx, &signrpc.SignReq{
		RawTxBytes: rawTx,
		SignDescs: []*signrpc.SignDescriptor{
			desc,
		},
	})
	if err != nil {
		return ComputedInputScript{}, err
	}
	if resp == nil || len(resp.GetInputScripts()) == 0 || resp.GetInputScripts()[0] == nil {
		return ComputedInputScript{}, errors.New("empty computed input script")
	}

	computed := resp.GetInputScripts()[0]
	out := ComputedInputScript{
		Witness:   make([][]byte, 0, len(computed.GetWitness())),
		SigScript: append([]byte(nil), computed.GetSigScript()...),
	}
	for _, item := range computed.GetWitness() {
		out.Witness = append(out.Witness, append([]byte(nil), item...))
	}

	return out, nil
}

func (c *Client) SignOutputRaw(ctx context.Context, params SignOutputRawParams) ([]byte, error) {
	txHex := strings.TrimSpace(params.RawTxHex)
	if txHex == "" {
		return nil, errors.New("raw tx required")
	}
	rawTx, err := hex.DecodeString(txHex)
	if err != nil || len(rawTx) == 0 {
		return nil, errors.New("invalid raw tx")
	}
	if params.OutputSat <= 0 {
		return nil, errors.New("output sat must be positive")
	}

	outputScriptHex := strings.TrimSpace(params.OutputScriptHex)
	if outputScriptHex == "" {
		return nil, errors.New("output script required")
	}
	outputScript, err := hex.DecodeString(outputScriptHex)
	if err != nil || len(outputScript) == 0 {
		return nil, errors.New("invalid output script")
	}

	keyRaw, err := hex.DecodeString(strings.TrimSpace(params.Key.PublicKey))
	if err != nil || len(keyRaw) == 0 {
		return nil, errors.New("invalid signing key")
	}

	desc := &signrpc.SignDescriptor{
		KeyDesc: &signrpc.KeyDescriptor{
			RawKeyBytes: keyRaw,
			KeyLoc: &signrpc.KeyLocator{
				KeyFamily: params.Key.Family,
				KeyIndex:  params.Key.Index,
			},
		},
		Output: &signrpc.TxOut{
			Value:    params.OutputSat,
			PkScript: outputScript,
		},
		Sighash:    params.SighashType,
		InputIndex: int32(params.InputIndex),
		SignMethod: params.SignMethod,
	}
	if desc.Sighash == 0 {
		desc.Sighash = 1
	}

	if witnessScriptHex := strings.TrimSpace(params.WitnessScriptHex); witnessScriptHex != "" {
		witnessScript, err := hex.DecodeString(witnessScriptHex)
		if err != nil {
			return nil, errors.New("invalid witness script")
		}
		desc.WitnessScript = witnessScript
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := signrpc.NewSignerClient(conn)
	resp, err := client.SignOutputRaw(ctx, &signrpc.SignReq{
		RawTxBytes: rawTx,
		SignDescs: []*signrpc.SignDescriptor{
			desc,
		},
	})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.GetRawSigs()) == 0 || len(resp.GetRawSigs()[0]) == 0 {
		return nil, errors.New("empty raw signature")
	}

	return append([]byte(nil), resp.GetRawSigs()[0]...), nil
}

func (c *Client) PublishTransaction(ctx context.Context, txHex string, label string) error {
	raw, err := hex.DecodeString(strings.TrimSpace(txHex))
	if err != nil {
		return errors.New("invalid tx hex")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.PublishTransaction(ctx, &walletrpc.Transaction{
		TxHex: raw,
		Label: strings.TrimSpace(label),
	})
	if err != nil {
		return err
	}
	if resp != nil && strings.TrimSpace(resp.GetPublishError()) != "" {
		return errors.New(strings.TrimSpace(resp.GetPublishError()))
	}
	return nil
}

func (c *Client) ListPendingSweeps(ctx context.Context) ([]PendingSweepInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.PendingSweeps(ctx, &walletrpc.PendingSweepsRequest{})
	if err != nil {
		return nil, err
	}

	items := make([]PendingSweepInfo, 0, len(resp.GetPendingSweeps()))
	for _, pending := range resp.GetPendingSweeps() {
		if pending == nil {
			continue
		}
		txid, vout, outpoint := walletKitOutpointInfo(pending.GetOutpoint())
		items = append(items, PendingSweepInfo{
			Outpoint:             outpoint,
			Txid:                 txid,
			Vout:                 vout,
			WitnessType:          strings.TrimSpace(pending.GetWitnessType().String()),
			AmountSat:            int64(pending.GetAmountSat()),
			BroadcastAttempts:    pending.GetBroadcastAttempts(),
			NextBroadcastHeight:  pending.GetNextBroadcastHeight(),
			SatPerVbyte:          int64(pending.GetSatPerVbyte()),
			RequestedSatPerVbyte: int64(pending.GetRequestedSatPerVbyte()),
			Immediate:            pending.GetImmediate(),
			BudgetSat:            int64(pending.GetBudget()),
			DeadlineHeight:       pending.GetDeadlineHeight(),
		})
	}

	return items, nil
}

func (c *Client) ListSweeps(ctx context.Context) ([]SweepHistoryInfo, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := walletrpc.NewWalletKitClient(conn)
	resp, err := client.ListSweeps(ctx, &walletrpc.ListSweepsRequest{})
	if err != nil {
		return nil, err
	}

	txids := resp.GetTransactionIds()
	if txids == nil {
		return nil, nil
	}

	items := make([]SweepHistoryInfo, 0, len(txids.GetTransactionIds()))
	for _, txid := range txids.GetTransactionIds() {
		trimmed := strings.ToLower(strings.TrimSpace(txid))
		if trimmed == "" {
			continue
		}
		items = append(items, SweepHistoryInfo{Txid: trimmed})
	}
	return items, nil
}

func (c *Client) BumpFee(ctx context.Context, params BumpFeeParams) error {
	outpoint, err := parseOutPoint(params.Outpoint)
	if err != nil {
		return err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := &walletrpc.BumpFeeRequest{
		Outpoint:  outpoint,
		Immediate: params.Immediate,
	}
	if params.TargetConf > 0 {
		req.TargetConf = params.TargetConf
	}
	if params.SatPerVbyte > 0 {
		req.SatPerVbyte = uint64(params.SatPerVbyte)
	}
	if params.BudgetSat > 0 {
		req.Budget = uint64(params.BudgetSat)
	}

	client := walletrpc.NewWalletKitClient(conn)
	_, err = client.BumpFee(ctx, req)
	return err
}

func (c *Client) RegisterChanPointShim(ctx context.Context, params ChanPointShimParams) error {
	if params.CapacitySat <= 0 {
		return errors.New("capacity must be positive")
	}
	if len(params.PendingChanID) == 0 {
		return errors.New("pending channel id required")
	}
	txid := strings.TrimSpace(params.FundingTxID)
	if txid == "" {
		return errors.New("funding tx id required")
	}
	fundingTxidBytes, err := reversedTxidBytes(txid)
	if err != nil {
		return err
	}

	localRaw, err := hex.DecodeString(strings.TrimSpace(params.LocalKey.PublicKey))
	if err != nil || len(localRaw) == 0 {
		return errors.New("invalid local key")
	}
	remoteRaw, err := hex.DecodeString(strings.TrimSpace(params.RemoteKeyHex))
	if err != nil || len(remoteRaw) == 0 {
		return errors.New("invalid remote key")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	_, err = client.FundingStateStep(ctx, &lnrpc.FundingTransitionMsg{
		Trigger: &lnrpc.FundingTransitionMsg_ShimRegister{
			ShimRegister: &lnrpc.FundingShim{
				Shim: &lnrpc.FundingShim_ChanPointShim{
					ChanPointShim: &lnrpc.ChanPointShim{
						Amt: params.CapacitySat,
						ChanPoint: &lnrpc.ChannelPoint{
							FundingTxid: &lnrpc.ChannelPoint_FundingTxidBytes{FundingTxidBytes: fundingTxidBytes},
							OutputIndex: params.FundingVout,
						},
						LocalKey: &lnrpc.KeyDescriptor{
							RawKeyBytes: localRaw,
							KeyLoc: &lnrpc.KeyLocator{
								KeyFamily: params.LocalKey.Family,
								KeyIndex:  params.LocalKey.Index,
							},
						},
						RemoteKey:     remoteRaw,
						PendingChanId: params.PendingChanID,
						Musig2:        params.Musig2,
					},
				},
			},
		},
	})
	return err
}

func (c *Client) CancelFundingShim(ctx context.Context, pendingChanID []byte) error {
	if len(pendingChanID) == 0 {
		return errors.New("pending channel id required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	_, err = client.FundingStateStep(ctx, &lnrpc.FundingTransitionMsg{
		Trigger: &lnrpc.FundingTransitionMsg_ShimCancel{
			ShimCancel: &lnrpc.FundingShimCancel{
				PendingChanId: pendingChanID,
			},
		},
	})
	return err
}

func (c *Client) OpenChannelWithShim(ctx context.Context, params OpenChannelWithShimParams) (string, error) {
	pubkeyHex := strings.TrimSpace(params.PubkeyHex)
	if pubkeyHex == "" {
		return "", errors.New("pubkey required")
	}
	if params.CapacitySat <= 0 {
		return "", errors.New("capacity must be positive")
	}
	localFundingSat := params.LocalFundingSat
	if localFundingSat <= 0 {
		localFundingSat = params.CapacitySat
	}
	if localFundingSat > params.CapacitySat {
		return "", errors.New("local funding cannot exceed capacity")
	}
	if params.PushSat < 0 {
		return "", errors.New("push amount cannot be negative")
	}
	if params.PushSat > localFundingSat {
		return "", errors.New("push amount cannot exceed local funding")
	}
	pubkey, err := hex.DecodeString(pubkeyHex)
	if err != nil {
		return "", errors.New("invalid pubkey hex")
	}
	remoteRaw, err := hex.DecodeString(strings.TrimSpace(params.ChanPointShimArgs.RemoteKeyHex))
	if err != nil || len(remoteRaw) == 0 {
		return "", errors.New("invalid remote key")
	}
	localRaw, err := hex.DecodeString(strings.TrimSpace(params.ChanPointShimArgs.LocalKey.PublicKey))
	if err != nil || len(localRaw) == 0 {
		return "", errors.New("invalid local key")
	}
	if len(params.ChanPointShimArgs.PendingChanID) == 0 {
		return "", errors.New("pending channel id required")
	}
	fundingTxidBytes, err := reversedTxidBytes(params.ChanPointShimArgs.FundingTxID)
	if err != nil {
		return "", err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	req := &lnrpc.OpenChannelRequest{
		NodePubkey:         pubkey,
		LocalFundingAmount: localFundingSat,
		PushSat:            params.PushSat,
		Private:            params.Private,
		FundingShim: &lnrpc.FundingShim{
			Shim: &lnrpc.FundingShim_ChanPointShim{
				ChanPointShim: &lnrpc.ChanPointShim{
					Amt: params.ChanPointShimArgs.CapacitySat,
					ChanPoint: &lnrpc.ChannelPoint{
						FundingTxid: &lnrpc.ChannelPoint_FundingTxidBytes{FundingTxidBytes: fundingTxidBytes},
						OutputIndex: params.ChanPointShimArgs.FundingVout,
					},
					LocalKey: &lnrpc.KeyDescriptor{
						RawKeyBytes: localRaw,
						KeyLoc: &lnrpc.KeyLocator{
							KeyFamily: params.ChanPointShimArgs.LocalKey.Family,
							KeyIndex:  params.ChanPointShimArgs.LocalKey.Index,
						},
					},
					RemoteKey:     remoteRaw,
					PendingChanId: params.ChanPointShimArgs.PendingChanID,
					Musig2:        params.ChanPointShimArgs.Musig2,
				},
			},
		},
	}
	if params.CommitmentType != 0 {
		req.CommitmentType = params.CommitmentType
	}
	if params.ZeroConf {
		req.ZeroConf = true
	}
	if params.ScidAlias {
		req.ScidAlias = true
	}
	if params.SatPerVbyte > 0 {
		req.SatPerVbyte = uint64(params.SatPerVbyte)
	}
	if strings.TrimSpace(params.CloseAddress) != "" {
		req.CloseAddress = strings.TrimSpace(params.CloseAddress)
	}

	client := lnrpc.NewLightningClient(conn)
	stream, err := client.OpenChannel(ctx, req)
	if err != nil {
		return "", err
	}
	for {
		update, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			return "", recvErr
		}
		if update == nil {
			continue
		}
		if pending := update.GetChanPending(); pending != nil {
			txid := txidFromBytes(pending.GetTxid())
			if txid == "" {
				return "", errors.New("missing pending channel txid")
			}
			_ = stream.CloseSend()
			return fmt.Sprintf("%s:%d", txid, pending.GetOutputIndex()), nil
		}
		if opened := update.GetChanOpen(); opened != nil {
			point := channelPointString(opened.GetChannelPoint())
			if point != "" {
				_ = stream.CloseSend()
				return point, nil
			}
		}
	}

	return "", errors.New("channel open pending update unavailable")
}

func (c *Client) BatchOpenChannel(ctx context.Context, channels []BatchOpenChannelParams, satPerVbyte int64) ([]BatchOpenChannelResult, error) {
	if len(channels) == 0 {
		return nil, errors.New("channels required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reqChannels := make([]*lnrpc.BatchOpenChannel, 0, len(channels))
	for _, item := range channels {
		pubkeyHex := strings.TrimSpace(item.PubkeyHex)
		if pubkeyHex == "" {
			return nil, errors.New("pubkey required")
		}
		pubkey, err := hex.DecodeString(pubkeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid pubkey hex")
		}
		reqItem := &lnrpc.BatchOpenChannel{
			NodePubkey:         pubkey,
			LocalFundingAmount: item.LocalFundingSat,
			Private:            item.Private,
		}
		if strings.TrimSpace(item.CloseAddress) != "" {
			reqItem.CloseAddress = strings.TrimSpace(item.CloseAddress)
		}
		reqChannels = append(reqChannels, reqItem)
	}

	req := &lnrpc.BatchOpenChannelRequest{
		Channels: reqChannels,
	}
	if satPerVbyte > 0 {
		req.SatPerVbyte = satPerVbyte
	}

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.BatchOpenChannel(ctx, req)
	if err != nil {
		return nil, err
	}

	results := make([]BatchOpenChannelResult, 0, len(resp.GetPendingChannels()))
	for _, pending := range resp.GetPendingChannels() {
		if pending == nil {
			continue
		}
		txid := txidFromBytes(pending.GetTxid())
		result := BatchOpenChannelResult{
			Txid:        txid,
			OutputIndex: pending.GetOutputIndex(),
		}
		if txid != "" {
			result.ChannelPoint = fmt.Sprintf("%s:%d", txid, pending.GetOutputIndex())
		}
		results = append(results, result)
	}

	return results, nil
}

func (c *Client) CloseChannel(ctx context.Context, channelPoint string, force bool, satPerVbyte int64) (string, error) {
	maxFeePerVbyte := int64(0)
	if !force && satPerVbyte > 0 {
		maxFeePerVbyte = satPerVbyte
	}

	closingTxid, err := c.closeChannelOnce(ctx, channelPoint, force, satPerVbyte, maxFeePerVbyte)
	if err != nil && isChannelClosingInProgressError(err) {
		err = nil
	}
	if err != nil {
		return "", err
	}

	if closingTxid == "" {
		closingTxid = c.lookupPendingClosingTxid(ctx, channelPoint)
	}
	if closingTxid == "" {
		recoveredTxid, _, recoverErr := c.RecoverWaitingCloseTx(ctx, channelPoint)
		if recoverErr == nil {
			closingTxid = strings.TrimSpace(recoveredTxid)
		}
	}
	return closingTxid, nil
}

func (c *Client) closeChannelOnce(ctx context.Context, channelPoint string, force bool, satPerVbyte int64, maxFeePerVbyte int64) (string, error) {
	cp, err := parseChannelPoint(channelPoint)
	if err != nil {
		return "", err
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	req := &lnrpc.CloseChannelRequest{
		ChannelPoint: cp,
		Force:        force,
	}
	if !force && satPerVbyte > 0 {
		req.SatPerVbyte = uint64(satPerVbyte)
	}
	if !force && maxFeePerVbyte > 0 {
		req.MaxFeePerVbyte = uint64(maxFeePerVbyte)
	}
	stream, err := client.CloseChannel(ctx, req)
	if err != nil {
		return "", err
	}

	closingTxid := ""
	for {
		update, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if isTimeoutError(recvErr) || errors.Is(recvErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				break
			}
			return "", recvErr
		}
		if update == nil {
			continue
		}
		if pending := update.GetClosePending(); pending != nil {
			if txid := txidFromBytes(pending.GetTxid()); txid != "" {
				closingTxid = txid
				break
			}
		}
		if closed := update.GetChanClose(); closed != nil {
			if txid := txidFromBytes(closed.GetClosingTxid()); txid != "" {
				closingTxid = txid
				break
			}
		}
	}

	return closingTxid, nil
}

func (c *Client) lookupPendingClosingTxid(ctx context.Context, channelPoint string) string {
	point := strings.ToLower(strings.TrimSpace(channelPoint))
	if point == "" {
		return ""
	}
	conn, err := c.dial(ctx, true)
	if err != nil {
		return ""
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.PendingChannels(ctx, &lnrpc.PendingChannelsRequest{})
	if err != nil {
		return ""
	}
	matchPoint := func(itemPoint string) bool {
		return strings.EqualFold(strings.TrimSpace(itemPoint), point)
	}
	for _, item := range resp.PendingClosingChannels {
		if item == nil || item.Channel == nil || !matchPoint(item.Channel.ChannelPoint) {
			continue
		}
		if txid := strings.TrimSpace(item.ClosingTxid); txid != "" {
			return txid
		}
	}
	for _, item := range resp.PendingForceClosingChannels {
		if item == nil || item.Channel == nil || !matchPoint(item.Channel.ChannelPoint) {
			continue
		}
		if txid := strings.TrimSpace(item.ClosingTxid); txid != "" {
			return txid
		}
	}
	for _, item := range resp.WaitingCloseChannels {
		if item == nil || item.Channel == nil || !matchPoint(item.Channel.ChannelPoint) {
			continue
		}
		if txid := strings.TrimSpace(item.ClosingTxid); txid != "" {
			return txid
		}
	}
	return ""
}

func (c *Client) RecoverWaitingCloseTx(ctx context.Context, channelPoint string) (string, bool, error) {
	entry, err := c.lookupWaitingCloseEntry(ctx, channelPoint, true)
	if err != nil {
		return "", false, err
	}
	if entry == nil {
		return "", false, nil
	}

	if txid := strings.TrimSpace(entry.GetClosingTxid()); txid != "" {
		return txid, false, nil
	}

	txHex := strings.TrimSpace(entry.GetClosingTxHex())
	if txHex == "" {
		if txid := c.lookupPendingClosingTxid(ctx, channelPoint); txid != "" {
			return txid, false, nil
		}
		return "", false, nil
	}

	publishErr := c.PublishTransaction(ctx, txHex, "channel-close-recover")
	if publishErr != nil && !isAlreadyPublishedTxError(publishErr) {
		if txid := c.lookupPendingClosingTxid(ctx, channelPoint); txid != "" {
			return txid, true, nil
		}
		return "", true, publishErr
	}

	if txid := c.lookupPendingClosingTxid(ctx, channelPoint); txid != "" {
		return txid, true, nil
	}
	if txid := txidFromRawTxHex(txHex); txid != "" {
		return txid, true, nil
	}

	return "", true, nil
}

func (c *Client) lookupWaitingCloseEntry(ctx context.Context, channelPoint string, includeRawTx bool) (*lnrpc.PendingChannelsResponse_WaitingCloseChannel, error) {
	point := strings.ToLower(strings.TrimSpace(channelPoint))
	if point == "" {
		return nil, errors.New("channel_point required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.PendingChannels(ctx, &lnrpc.PendingChannelsRequest{IncludeRawTx: includeRawTx})
	if err != nil {
		return nil, err
	}

	for _, item := range resp.GetWaitingCloseChannels() {
		if item == nil || item.GetChannel() == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(item.GetChannel().GetChannelPoint()), point) {
			return item, nil
		}
	}
	return nil, nil
}

func (c *Client) UpdateChannelFees(ctx context.Context, channelPoint string, applyAll bool, baseFeeMsat int64, feeRatePpm int64, timeLockDelta int64, inboundEnabled bool, inboundBaseMsat int64, inboundFeeRatePpm int64) error {
	return c.UpdateChannelPolicy(ctx, UpdateChannelPolicyParams{
		ChannelPoint:      channelPoint,
		ApplyAll:          applyAll,
		BaseFeeMsat:       baseFeeMsat,
		FeeRatePpm:        feeRatePpm,
		TimeLockDelta:     timeLockDelta,
		InboundEnabled:    inboundEnabled,
		InboundBaseMsat:   inboundBaseMsat,
		InboundFeeRatePpm: inboundFeeRatePpm,
	})
}

func (c *Client) UpdateChannelPolicy(ctx context.Context, params UpdateChannelPolicyParams) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := &lnrpc.PolicyUpdateRequest{
		BaseFeeMsat:   params.BaseFeeMsat,
		FeeRatePpm:    uint32(params.FeeRatePpm),
		TimeLockDelta: uint32(params.TimeLockDelta),
	}
	if params.InboundEnabled {
		if params.InboundBaseMsat < math.MinInt32 || params.InboundBaseMsat > math.MaxInt32 {
			return fmt.Errorf("inbound base fee out of range")
		}
		if params.InboundFeeRatePpm < math.MinInt32 || params.InboundFeeRatePpm > math.MaxInt32 {
			return fmt.Errorf("inbound fee rate out of range")
		}
		req.InboundFee = &lnrpc.InboundFee{
			BaseFeeMsat: int32(params.InboundBaseMsat),
			FeeRatePpm:  int32(params.InboundFeeRatePpm),
		}
	}
	if params.MaxHtlcMsat != nil {
		req.MaxHtlcMsat = *params.MaxHtlcMsat
	}
	if params.MinHtlcMsat != nil {
		req.MinHtlcMsat = *params.MinHtlcMsat
	}
	req.MinHtlcMsatSpecified = params.MinHtlcMsatSpecified

	if params.ApplyAll {
		req.Scope = &lnrpc.PolicyUpdateRequest_Global{Global: true}
	} else {
		cp, err := parseChannelPoint(params.ChannelPoint)
		if err != nil {
			return err
		}
		req.Scope = &lnrpc.PolicyUpdateRequest_ChanPoint{ChanPoint: cp}
	}

	client := lnrpc.NewLightningClient(conn)
	_, err = client.UpdateChannelPolicy(ctx, req)
	return err
}

func (c *Client) UpdateChanStatus(ctx context.Context, channelPoint string, enable bool) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	cp, err := parseChannelPoint(channelPoint)
	if err != nil {
		return err
	}

	action := routerrpc.ChanStatusAction_ENABLE
	if !enable {
		action = routerrpc.ChanStatusAction_DISABLE
	}

	client := routerrpc.NewRouterClient(conn)
	_, err = client.UpdateChanStatus(ctx, &routerrpc.UpdateChanStatusRequest{
		ChanPoint: cp,
		Action:    action,
	})
	return err
}

func isWalletLocked(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "wallet locked") || strings.Contains(msg, "unlock")
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline exceeded")
}

func isChannelClosingInProgressError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "already in the process of closure") ||
		strings.Contains(value, "channel is being closed") ||
		strings.Contains(value, "channel shutdown already initiated") ||
		strings.Contains(value, "already pending channel close")
}

func isAlreadyPublishedTxError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "already have transaction") ||
		strings.Contains(value, "already in mempool") ||
		strings.Contains(value, "txn-already-known") ||
		strings.Contains(value, "already known") ||
		strings.Contains(value, "transaction already in block chain") ||
		strings.Contains(value, "already exists")
}

func isLocalChanDisabledFlags(flags string) bool {
	trimmed := strings.TrimSpace(flags)
	if trimmed == "" {
		return false
	}
	normalized := strings.ToLower(trimmed)
	split := func(r rune) bool {
		switch r {
		case '|', ',', ';', ' ':
			return true
		default:
			return false
		}
	}
	tokens := strings.FieldsFunc(normalized, split)
	if len(tokens) == 0 {
		tokens = []string{normalized}
	}
	for _, token := range tokens {
		tok := strings.TrimSpace(token)
		if tok == "" {
			continue
		}
		if strings.Contains(tok, "localchandisabled") || strings.Contains(tok, "local_chan_disabled") {
			return true
		}
		if strings.Contains(tok, "disabled") && !strings.Contains(tok, "remote") {
			if strings.Contains(tok, "local") || strings.Contains(tok, "chanstatusdisabled") || tok == "disabled" {
				return true
			}
		}
	}
	return false
}

func channelPointString(cp *lnrpc.ChannelPoint) string {
	if cp == nil {
		return ""
	}
	txid := cp.GetFundingTxidStr()
	if txid == "" {
		txid = txidFromBytes(cp.GetFundingTxidBytes())
	}
	if txid == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", txid, cp.OutputIndex)
}

func txidFromBytes(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	rev := make([]byte, len(raw))
	for i := range raw {
		rev[len(raw)-1-i] = raw[i]
	}
	return hex.EncodeToString(rev)
}

func walletKitOutpointInfo(out *lnrpc.OutPoint) (string, uint32, string) {
	if out == nil {
		return "", 0, ""
	}
	txid := strings.ToLower(strings.TrimSpace(out.GetTxidStr()))
	if txid == "" {
		txid = txidFromBytes(out.GetTxidBytes())
	}
	if txid == "" {
		return "", out.GetOutputIndex(), ""
	}
	return txid, out.GetOutputIndex(), fmt.Sprintf("%s:%d", txid, out.GetOutputIndex())
}

func txidFromRawTxHex(txHex string) string {
	trimmed := strings.TrimSpace(txHex)
	if trimmed == "" {
		return ""
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return ""
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return ""
	}
	hash := tx.TxHash()
	return strings.ToLower(strings.TrimSpace(hash.String()))
}

func normalizeTxidHex(value string) (string, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if len(trimmed) != 64 {
		return "", errors.New("invalid txid")
	}
	if _, err := hex.DecodeString(trimmed); err != nil {
		return "", errors.New("invalid txid")
	}
	return trimmed, nil
}

func reversedTxidBytes(txid string) ([]byte, error) {
	normalized, err := normalizeTxidHex(txid)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, errors.New("invalid txid")
	}
	for i := 0; i < len(raw)/2; i++ {
		raw[i], raw[len(raw)-1-i] = raw[len(raw)-1-i], raw[i]
	}
	return raw, nil
}

func parseChannelPoint(point string) (*lnrpc.ChannelPoint, error) {
	trimmed := strings.TrimSpace(point)
	if trimmed == "" {
		return nil, errors.New("channel_point required")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return nil, errors.New("channel_point must be txid:index")
	}
	idx, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, errors.New("invalid channel_point index")
	}
	return &lnrpc.ChannelPoint{
		FundingTxid: &lnrpc.ChannelPoint_FundingTxidStr{FundingTxidStr: parts[0]},
		OutputIndex: uint32(idx),
	}, nil
}

func parseOutPoint(point string) (*lnrpc.OutPoint, error) {
	trimmed := strings.TrimSpace(point)
	if trimmed == "" {
		return nil, errors.New("outpoint required")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return nil, errors.New("outpoint must be txid:index")
	}
	txid, err := normalizeTxidHex(parts[0])
	if err != nil {
		return nil, err
	}
	idx, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, errors.New("invalid outpoint index")
	}
	return &lnrpc.OutPoint{
		TxidStr:     txid,
		OutputIndex: uint32(idx),
	}, nil
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func maxInt64ToUint64(v int64) uint64 {
	if v <= 0 {
		return 0
	}
	return uint64(v)
}

func addressTypeLabel(addrType lnrpc.AddressType) string {
	switch addrType {
	case lnrpc.AddressType_WITNESS_PUBKEY_HASH:
		return "p2wkh"
	case lnrpc.AddressType_NESTED_PUBKEY_HASH:
		return "np2wkh"
	case lnrpc.AddressType_TAPROOT_PUBKEY:
		return "p2tr"
	default:
		label := strings.ToLower(addrType.String())
		label = strings.ReplaceAll(label, "unused_", "")
		label = strings.ReplaceAll(label, "_", "-")
		return label
	}
}

type Status struct {
	ServiceActive    bool
	WalletState      string
	SyncedToChain    bool
	SyncedToGraph    bool
	BlockHeight      int64
	Version          string
	Pubkey           string
	URI              string
	URIs             []string
	InfoKnown        bool
	InfoStale        bool
	InfoAgeSeconds   int64
	ChannelsActive   int
	ChannelsInactive int
	OnchainSat       int64
	LightningSat     int64
}

type ChannelPendingHtlcInfo struct {
	Incoming            bool   `json:"incoming"`
	PeerAlias           string `json:"peer_alias,omitempty"`
	AmountSat           int64  `json:"amount_sat"`
	ExpirationHeight    uint32 `json:"expiration_height"`
	HtlcIndex           uint64 `json:"htlc_index,omitempty"`
	ForwardingChannelID uint64 `json:"forwarding_channel_id,omitempty"`
	LockedIn            bool   `json:"locked_in,omitempty"`
}

type ChannelMovement7d struct {
	ForwardCount          int   `json:"forward_count"`
	ForwardAmountSat      int64 `json:"forward_amount_sat"`
	ForwardInCount        int   `json:"forward_in_count"`
	ForwardInAmountSat    int64 `json:"forward_in_amount_sat"`
	ForwardOutCount       int   `json:"forward_out_count"`
	ForwardOutAmountSat   int64 `json:"forward_out_amount_sat"`
	RebalanceCount        int   `json:"rebalance_count"`
	RebalanceAmountSat    int64 `json:"rebalance_amount_sat"`
	RebalanceInCount      int   `json:"rebalance_in_count"`
	RebalanceInAmountSat  int64 `json:"rebalance_in_amount_sat"`
	RebalanceOutCount     int   `json:"rebalance_out_count"`
	RebalanceOutAmountSat int64 `json:"rebalance_out_amount_sat"`
	LightningOutCount     int   `json:"lightning_out_count"`
	LightningOutAmountSat int64 `json:"lightning_out_amount_sat"`
	LightningInCount      int   `json:"lightning_in_count"`
	LightningInAmountSat  int64 `json:"lightning_in_amount_sat"`
}

type ChannelInfo struct {
	ChannelPoint        string                   `json:"channel_point"`
	ChannelID           uint64                   `json:"channel_id"`
	RemotePubkey        string                   `json:"remote_pubkey"`
	PeerAlias           string                   `json:"peer_alias"`
	Initiator           bool                     `json:"initiator"`
	Active              bool                     `json:"active"`
	InactiveSinceUnix   int64                    `json:"inactive_since_unix,omitempty"`
	InactiveDurationSec int64                    `json:"inactive_duration_sec,omitempty"`
	ChanStatusFlags     string                   `json:"chan_status_flags,omitempty"`
	LocalDisabled       bool                     `json:"local_disabled,omitempty"`
	Private             bool                     `json:"private"`
	CapacitySat         int64                    `json:"capacity_sat"`
	LocalBalanceSat     int64                    `json:"local_balance_sat"`
	RemoteBalanceSat    int64                    `json:"remote_balance_sat"`
	LocalChanReserveSat int64                    `json:"local_chan_reserve_sat,omitempty"`
	UnsettledBalanceSat int64                    `json:"unsettled_balance_sat,omitempty"`
	PendingHtlcCount    int                      `json:"pending_htlc_count,omitempty"`
	PendingHtlcs        []ChannelPendingHtlcInfo `json:"pending_htlcs,omitempty"`
	BaseFeeMsat         *int64                   `json:"base_fee_msat,omitempty"`
	FeeRatePpm          *int64                   `json:"fee_rate_ppm,omitempty"`
	InboundFeeRatePpm   *int64                   `json:"inbound_fee_rate_ppm,omitempty"`
	PeerFeeRatePpm      *int64                   `json:"peer_fee_rate_ppm,omitempty"`
	PeerBaseMsat        *int64                   `json:"peer_base_msat,omitempty"`
	ClassLabel          string                   `json:"class_label,omitempty"`
	OutPpm7d            *int                     `json:"out_ppm7d,omitempty"`
	RebalPpm7d          *int                     `json:"rebal_ppm7d,omitempty"`
	ForwardFee7dSat     *int64                   `json:"forward_fee_7d_sat,omitempty"`
	RebalFee7dSat       *int64                   `json:"rebal_fee_7d_sat,omitempty"`
	ProfitFee7dSat      *int64                   `json:"profit_fee_7d_sat,omitempty"`
	Movement7d          *ChannelMovement7d       `json:"movement_7d,omitempty"`
}

func normalizeChannelPointKey(point string) string {
	return strings.ToLower(strings.TrimSpace(point))
}

func (c *Client) snapshotInactiveSince(channels []*lnrpc.Channel, now time.Time) map[string]time.Time {
	c.channelStateMu.Lock()
	defer c.channelStateMu.Unlock()

	if c.channelInactive == nil {
		c.channelInactive = make(map[string]time.Time)
	}

	seen := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		key := normalizeChannelPointKey(ch.ChannelPoint)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		if ch.Active {
			delete(c.channelInactive, key)
			continue
		}
		if _, ok := c.channelInactive[key]; !ok {
			c.channelInactive[key] = now
		}
	}

	for key := range c.channelInactive {
		if _, ok := seen[key]; !ok {
			delete(c.channelInactive, key)
		}
	}

	out := make(map[string]time.Time, len(c.channelInactive))
	for key, ts := range c.channelInactive {
		out[key] = ts
	}
	return out
}

type PeerInfo struct {
	PubKey        string `json:"pub_key"`
	Alias         string `json:"alias"`
	Address       string `json:"address"`
	Inbound       bool   `json:"inbound"`
	BytesSent     uint64 `json:"bytes_sent"`
	BytesRecv     uint64 `json:"bytes_recv"`
	SatSent       int64  `json:"sat_sent"`
	SatRecv       int64  `json:"sat_recv"`
	PingTime      int64  `json:"ping_time"`
	SyncType      string `json:"sync_type"`
	LastError     string `json:"last_error"`
	LastErrorTime int64  `json:"last_error_time,omitempty"`
}

type WatchtowerInfo struct {
	Pubkey                 string   `json:"pubkey"`
	Addresses              []string `json:"addresses,omitempty"`
	ActiveSessionCandidate bool     `json:"active_session_candidate"`
	NumSessions            int      `json:"num_sessions"`
}

type NodeAddress struct {
	Network string `json:"network"`
	Addr    string `json:"addr"`
}

type NodeDetails struct {
	PubKey    string        `json:"pub_key"`
	Alias     string        `json:"alias"`
	Addresses []NodeAddress `json:"addresses,omitempty"`
}

type PeerNeighborRecommendation struct {
	PubKey             string `json:"pub_key"`
	Alias              string `json:"alias,omitempty"`
	Host               string `json:"host,omitempty"`
	PeerAddress        string `json:"peer_address,omitempty"`
	ChannelCount       int    `json:"channel_count"`
	TotalCapacitySat   int64  `json:"total_capacity_sat"`
	LargestCapacitySat int64  `json:"largest_capacity_sat"`
	InboundBaseMsat    int64  `json:"inbound_base_msat"`
	InboundFeeRatePpm  int64  `json:"inbound_fee_rate_ppm"`
	OutboundBaseMsat   int64  `json:"outbound_base_msat"`
	OutboundFeeRatePpm int64  `json:"outbound_fee_rate_ppm"`
}

type BatchOpenChannelParams struct {
	PubkeyHex       string
	LocalFundingSat int64
	Private         bool
	CloseAddress    string
}

type BatchOpenChannelResult struct {
	ChannelPoint string `json:"channel_point,omitempty"`
	Txid         string `json:"txid,omitempty"`
	OutputIndex  uint32 `json:"output_index"`
}

type PendingChannelInfo struct {
	ChannelPoint             string `json:"channel_point"`
	RemotePubkey             string `json:"remote_pubkey"`
	PeerAlias                string `json:"peer_alias,omitempty"`
	CapacitySat              int64  `json:"capacity_sat"`
	LocalBalanceSat          int64  `json:"local_balance_sat"`
	RemoteBalanceSat         int64  `json:"remote_balance_sat"`
	Status                   string `json:"status"`
	ClosingTxid              string `json:"closing_txid,omitempty"`
	BlocksTilMaturity        int32  `json:"blocks_til_maturity,omitempty"`
	LimboBalance             int64  `json:"limbo_balance,omitempty"`
	ConfirmationsUntilActive uint32 `json:"confirmations_until_active,omitempty"`
	ConfirmationHeight       uint32 `json:"confirmation_height,omitempty"`
	Private                  bool   `json:"private"`
}

type ClosedChannelResolutionInfo struct {
	ResolutionType int32  `json:"resolution_type"`
	SweepTxid      string `json:"sweep_txid,omitempty"`
}

type ClosedChannelInfo struct {
	ChannelPoint         string                        `json:"channel_point,omitempty"`
	ChanID               uint64                        `json:"chan_id"`
	ClosedAt             string                        `json:"closed_at,omitempty"`
	ClosingTxHash        string                        `json:"closing_tx_hash,omitempty"`
	RemotePubkey         string                        `json:"remote_pubkey,omitempty"`
	PeerAlias            string                        `json:"peer_alias,omitempty"`
	CapacitySat          int64                         `json:"capacity_sat"`
	SettledBalanceSat    int64                         `json:"settled_balance_sat"`
	TimeLockedBalanceSat int64                         `json:"time_locked_balance_sat"`
	CloseType            int32                         `json:"close_type"`
	CloseTypeLabel       string                        `json:"close_type_label,omitempty"`
	OpenInitiator        int32                         `json:"open_initiator"`
	OpenInitiatorLabel   string                        `json:"open_initiator_label,omitempty"`
	CloseInitiator       int32                         `json:"close_initiator"`
	CloseInitiatorLabel  string                        `json:"close_initiator_label,omitempty"`
	CloseHeight          uint32                        `json:"close_height"`
	Resolutions          []ClosedChannelResolutionInfo `json:"resolutions,omitempty"`
}

type PendingSweepInfo struct {
	Outpoint             string `json:"outpoint"`
	Txid                 string `json:"txid"`
	Vout                 uint32 `json:"vout"`
	WitnessType          string `json:"witness_type,omitempty"`
	AmountSat            int64  `json:"amount_sat"`
	BroadcastAttempts    uint32 `json:"broadcast_attempts"`
	NextBroadcastHeight  uint32 `json:"next_broadcast_height,omitempty"`
	SatPerVbyte          int64  `json:"sat_per_vbyte"`
	RequestedSatPerVbyte int64  `json:"requested_sat_per_vbyte"`
	Immediate            bool   `json:"immediate"`
	BudgetSat            int64  `json:"budget_sat"`
	DeadlineHeight       uint32 `json:"deadline_height,omitempty"`
}

type SweepHistoryInfo struct {
	Txid string `json:"txid"`
}

type BumpFeeParams struct {
	Outpoint    string
	SatPerVbyte int64
	TargetConf  uint32
	Immediate   bool
	BudgetSat   int64
}

type RecentActivity struct {
	Type         string    `json:"type"`
	Network      string    `json:"network,omitempty"`
	Direction    string    `json:"direction,omitempty"`
	AmountSat    int64     `json:"amount_sat"`
	Memo         string    `json:"memo"`
	Timestamp    time.Time `json:"timestamp"`
	Status       string    `json:"status"`
	Txid         string    `json:"txid,omitempty"`
	Keysend      bool      `json:"keysend,omitempty"`
	ChannelID    uint64    `json:"channel_id,omitempty"`
	ChannelPoint string    `json:"channel_point,omitempty"`
	ChannelAlias string    `json:"channel_alias,omitempty"`
	PaymentHash  string    `json:"-"`
}

type OnchainTransaction struct {
	Txid          string    `json:"txid"`
	Direction     string    `json:"direction"`
	AmountSat     int64     `json:"amount_sat"`
	FeeSat        int64     `json:"fee_sat"`
	Confirmations int32     `json:"confirmations"`
	BlockHeight   int32     `json:"block_height"`
	Timestamp     time.Time `json:"timestamp"`
	Label         string    `json:"label,omitempty"`
	Addresses     []string  `json:"addresses,omitempty"`
}

type OnchainUtxo struct {
	Outpoint      string `json:"outpoint"`
	Txid          string `json:"txid"`
	Vout          uint32 `json:"vout"`
	Address       string `json:"address"`
	AddressType   string `json:"address_type"`
	AmountSat     int64  `json:"amount_sat"`
	Confirmations int64  `json:"confirmations"`
	PkScript      string `json:"pk_script,omitempty"`
}
