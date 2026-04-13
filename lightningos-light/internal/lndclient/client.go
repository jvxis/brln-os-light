package lndclient

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
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
	"google.golang.org/protobuf/proto"
)

const recentOnchainWindowBlocks int64 = 20160

const (
	walletActivityPageSize                             = 500
	walletActivityMaxPages                             = 200
	walletActivityApproxBlocksPerDay             int64 = 144
	pendingOpenBumpReasonUnavailable                   = "diagnostic_unavailable"
	pendingOpenBumpReasonFundingTxUnavailable          = "funding_tx_unavailable"
	pendingOpenBumpReasonNoWalletOutput                = "no_wallet_output"
	pendingOpenBumpReasonWalletOutputUnavailable       = "wallet_output_unavailable"
	pendingOpenBumpReasonChannelPointInvalid           = "channel_point_invalid"
)

type Client struct {
	cfg                *config.Config
	logger             *log.Logger
	statusMu           sync.Mutex
	statusCached       bool
	statusCache        Status
	statusErr          error
	statusNextFetch    time.Time
	infoCache          infoSnapshot
	infoCacheAt        time.Time
	infoCacheValid     bool
	walletAddressesMu  sync.Mutex
	walletAddresses    map[string]struct{}
	walletAddressesAt  time.Time
	channelStateMu     sync.Mutex
	channelInactive    map[string]time.Time
	channelPendingOpen map[string]time.Time
	nodeAliasMu        sync.Mutex
	nodeAliasCache     map[string]nodeAliasCacheEntry
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

var peerNeighborCriteriaTiers = []peerNeighborCriteria{
	{Name: "strict", MinTotalCapacitySat: 100_000_000, MinChannelCount: 11},
	{Name: "fallback_balanced", MinTotalCapacitySat: 50_000_000, MinChannelCount: 6},
	{Name: "fallback_relaxed", MinTotalCapacitySat: 20_000_000, MinChannelCount: 3},
	{Name: "fallback_loose", MinTotalCapacitySat: 10_000_000, MinChannelCount: 2},
	{Name: "fallback_exhaustive", MinTotalCapacitySat: 1, MinChannelCount: 1},
}

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
	return lookupPaymentWithClient(ctx, client, trimmed, lookback)
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

	if err := c.DeleteFailedPayments(ctx); err != nil {
		return 0, err
	}

	return failedCount, nil
}

func (c *Client) DeleteFailedPayments(ctx context.Context) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
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
				return ErrFailedPaymentsCleanupUnsupported
			}
		}
		return err
	}

	return nil
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
	AmountSat    int64
	AmountMsat   int64
	Memo         string
	Destination  string
	PaymentHash  string
	Expiry       int64
	Timestamp    int64
	CltvExpiry   int64
	RouteHints   []*lnrpc.RouteHint
	PaymentAddr  []byte
	DestFeatures []lnrpc.FeatureBit
	BlindedPaths []*lnrpc.BlindedPaymentPath
}

type CreatedInvoice struct {
	PaymentRequest string
	PaymentHash    string
	PaymentAddr    []byte
}

type CreateInvoiceOptions struct {
	IsBlinded          bool
	IncomingChannelIDs []uint64
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
	decoded, _, err := decodePaymentRequestWithClient(ctx, client, payReq)
	return decoded, err
}

func decodePaymentRequestWithClient(ctx context.Context, client lnrpc.LightningClient, payReq string) (DecodedInvoice, int64, error) {
	trimmed := strings.TrimSpace(payReq)
	if trimmed == "" {
		return DecodedInvoice{}, 0, errors.New("payment_request required")
	}

	resp, err := client.DecodePayReq(ctx, &lnrpc.PayReqString{PayReq: trimmed})
	if err != nil {
		return DecodedInvoice{}, 0, err
	}

	decoded := DecodedInvoice{
		AmountSat:    resp.NumSatoshis,
		AmountMsat:   resp.NumMsat,
		Memo:         resp.Description,
		Destination:  strings.TrimSpace(resp.Destination),
		PaymentHash:  strings.ToLower(strings.TrimSpace(resp.PaymentHash)),
		Expiry:       resp.Expiry,
		Timestamp:    resp.Timestamp,
		CltvExpiry:   resp.CltvExpiry,
		RouteHints:   append([]*lnrpc.RouteHint(nil), resp.RouteHints...),
		PaymentAddr:  append([]byte(nil), resp.PaymentAddr...),
		DestFeatures: payReqFeatureBits(resp.Features),
		BlindedPaths: append([]*lnrpc.BlindedPaymentPath(nil), resp.BlindedPaths...),
	}
	amountMsat := decoded.AmountMsat
	if amountMsat <= 0 && decoded.AmountSat > 0 {
		amountMsat = decoded.AmountSat * 1000
	}
	return decoded, amountMsat, nil
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

func buildCreateInvoiceRequest(amountSat int64, memo string, expirySeconds int64, opts *CreateInvoiceOptions) *lnrpc.Invoice {
	if expirySeconds <= 0 {
		expirySeconds = 3600
	}

	req := &lnrpc.Invoice{
		Memo:   memo,
		Value:  amountSat,
		Expiry: expirySeconds,
	}

	isBlinded := opts != nil && (opts.IsBlinded || len(opts.IncomingChannelIDs) > 0)
	if !isBlinded {
		return req
	}

	req.IsBlinded = true
	if len(opts.IncomingChannelIDs) > 0 {
		req.BlindedPathConfig = &lnrpc.BlindedPathConfig{
			IncomingChannelList: append([]uint64(nil), opts.IncomingChannelIDs...),
		}
	}

	return req
}

func (c *Client) CreateInvoice(ctx context.Context, amountSat int64, memo string, expirySeconds int64, opts *CreateInvoiceOptions) (CreatedInvoice, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return CreatedInvoice{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	resp, err := client.AddInvoice(ctx, buildCreateInvoiceRequest(amountSat, memo, expirySeconds, opts))
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

func (c *Client) PayInvoice(ctx context.Context, paymentRequest string, outgoingChanIDs []uint64, maxFeeSat int64) error {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	if len(outgoingChanIDs) == 0 && maxFeeSat <= 0 {
		client := lnrpc.NewLightningClient(conn)
		req := &lnrpc.SendRequest{PaymentRequest: paymentRequest}
		res, err := client.SendPaymentSync(ctx, req)
		return sendPaymentSyncError(res, err)
	}

	router := routerrpc.NewRouterClient(conn)
	feeLimitMsat := defaultRouterPaymentFeeLimitMsat(ctx, c, paymentRequest)
	if maxFeeSat > 0 {
		feeLimitMsat = maxFeeSat * 1000
	}
	req := &routerrpc.SendPaymentRequest{
		PaymentRequest:    paymentRequest,
		TimeoutSeconds:    paymentTimeoutSeconds(ctx, 90),
		OutgoingChanIds:   append([]uint64(nil), outgoingChanIDs...),
		FeeLimitMsat:      feeLimitMsat,
		NoInflightUpdates: true,
	}
	maxParts := uint32(len(outgoingChanIDs))
	if maxParts < 3 {
		maxParts = 3
	}
	req.MaxParts = maxParts

	stream, err := router.SendPaymentV2(ctx, req)
	if err != nil {
		return err
	}

	for {
		payment, err := stream.Recv()
		if err != nil {
			return err
		}
		if payment == nil {
			continue
		}
		switch payment.Status {
		case lnrpc.Payment_SUCCEEDED:
			return nil
		case lnrpc.Payment_FAILED:
			if payment.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
				return fmt.Errorf("payment failed: %s", payment.FailureReason.String())
			}
			return errors.New("payment failed")
		default:
		}
	}
}

func (c *Client) PayInvoiceWithMPP(ctx context.Context, paymentRequest string, outgoingChanIDs []uint64, maxFeeSat int64, maxParts uint32, maxShardSat int64) error {
	trimmed := strings.TrimSpace(paymentRequest)
	if trimmed == "" {
		return errors.New("payment_request required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	router := routerrpc.NewRouterClient(conn)
	feeLimitMsat := defaultRouterPaymentFeeLimitMsat(ctx, c, trimmed)
	if maxFeeSat > 0 {
		feeLimitMsat = maxFeeSat * 1000
	}
	if maxParts == 0 {
		maxParts = uint32(mppPaymentMaxParts(len(outgoingChanIDs)))
	}
	if maxParts < 2 {
		maxParts = 2
	}
	if maxParts > paymentMPPPlanMaxParts {
		maxParts = paymentMPPPlanMaxParts
	}

	req := &routerrpc.SendPaymentRequest{
		PaymentRequest:    trimmed,
		TimeoutSeconds:    paymentTimeoutSeconds(ctx, 120),
		OutgoingChanIds:   append([]uint64(nil), outgoingChanIDs...),
		FeeLimitMsat:      feeLimitMsat,
		MaxParts:          maxParts,
		NoInflightUpdates: true,
		TimePref:          0,
	}
	if maxShardSat > 0 {
		req.MaxShardSizeMsat = uint64(maxShardSat * 1000)
	}

	stream, err := router.SendPaymentV2(ctx, req)
	if err != nil {
		return err
	}

	for {
		payment, err := stream.Recv()
		if err != nil {
			return err
		}
		if payment == nil {
			continue
		}
		switch payment.Status {
		case lnrpc.Payment_SUCCEEDED:
			return nil
		case lnrpc.Payment_FAILED:
			if payment.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
				return fmt.Errorf("payment failed: %s", payment.FailureReason.String())
			}
			return errors.New("payment failed")
		default:
		}
	}
}

func (c *Client) PayInvoiceWithValidatedRoute(ctx context.Context, paymentRequest string, outgoingChanIDs []uint64, maxFeeSat int64, numRoutes int32, routeToken string) error {
	trimmed := strings.TrimSpace(paymentRequest)
	if trimmed == "" {
		return errors.New("payment_request required")
	}
	if numRoutes <= 0 {
		numRoutes = 5
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return err
	}
	defer conn.Close()

	lightning := lnrpc.NewLightningClient(conn)
	router := routerrpc.NewRouterClient(conn)
	decoded, amountMsat, err := decodePaymentRequestWithClient(ctx, lightning, trimmed)
	if err != nil {
		return err
	}
	if amountMsat <= 0 {
		return errors.New("amountless invoices are not supported for validated route payment")
	}
	if len(decoded.BlindedPaths) > 0 {
		return errors.New("validated route payment is unavailable for blinded invoices")
	}

	var selected *lnrpc.Route
	if strings.TrimSpace(routeToken) != "" {
		selected, err = decodePaymentRouteToken(routeToken)
		if err != nil {
			return err
		}
		if err := validatePaymentRouteForInvoice(selected, decoded, amountMsat, maxFeeSat); err != nil {
			return err
		}
		probeCtx, cancel := context.WithTimeout(ctx, paymentRouteLiquidityProbeTimeout)
		probe := probePaymentRoute(probeCtx, router, selected)
		cancel()
		if !probe.LikelyLiquid {
			return fmt.Errorf("validated route no longer has likely liquidity: %s", paymentRouteProbeFailureDescription(probe))
		}
	} else {
		routes, err := c.previewPaymentRouteCandidates(ctx, lightning, decoded, amountMsat, outgoingChanIDs, numRoutes)
		if err != nil {
			return err
		}
		probeLimit := paymentRoutePreviewProbeLimit(len(routes), int(numRoutes), len(outgoingChanIDs))
		probedRoutes := probeCheapestPaymentRouteCandidates(ctx, router, routes, int(numRoutes), probeLimit)
		for _, probed := range probedRoutes {
			if probed.route == nil || !probed.probe.LikelyLiquid {
				continue
			}
			if maxFeeSat > 0 && routeTotalFeeMsat(probed.route) > maxFeeSat*1000 {
				continue
			}
			if selected == nil || routeTotalFeeMsat(probed.route) < routeTotalFeeMsat(selected) {
				selected = probed.route
			}
		}
	}
	if selected == nil {
		if maxFeeSat > 0 {
			return errors.New("no validated route within max fee")
		}
		return errors.New("no validated route with likely liquidity")
	}

	route, err := routeForInvoicePayment(selected, decoded, amountMsat)
	if err != nil {
		return err
	}
	paymentHash, err := hex.DecodeString(strings.TrimSpace(decoded.PaymentHash))
	if err != nil || len(paymentHash) != 32 {
		return errors.New("invalid payment hash")
	}

	attempt, err := router.SendToRouteV2(ctx, &routerrpc.SendToRouteRequest{
		PaymentHash: paymentHash,
		Route:       route,
		SkipTempErr: false,
	})
	if err != nil {
		return err
	}
	if attempt == nil {
		return errors.New("empty route payment response")
	}
	switch attempt.Status {
	case lnrpc.HTLCAttempt_SUCCEEDED:
		return nil
	case lnrpc.HTLCAttempt_FAILED:
		if attempt.Failure != nil {
			return fmt.Errorf("route payment failed: %s at hop %d", attempt.Failure.Code.String(), attempt.Failure.FailureSourceIndex)
		}
		return errors.New("route payment failed")
	default:
		return fmt.Errorf("route payment status: %s", attempt.Status.String())
	}
}

func routeForInvoicePayment(route *lnrpc.Route, decoded DecodedInvoice, amountMsat int64) (*lnrpc.Route, error) {
	if route == nil {
		return nil, errors.New("route required")
	}
	cloned, ok := proto.Clone(route).(*lnrpc.Route)
	if !ok || cloned == nil {
		return nil, errors.New("invalid route")
	}
	if len(cloned.Hops) == 0 || cloned.Hops[len(cloned.Hops)-1] == nil {
		return nil, errors.New("empty route")
	}
	if amountMsat <= 0 {
		return nil, errors.New("amount required")
	}
	finalHop := cloned.Hops[len(cloned.Hops)-1]
	if len(decoded.PaymentAddr) > 0 {
		finalHop.MppRecord = &lnrpc.MPPRecord{
			PaymentAddr:  append([]byte(nil), decoded.PaymentAddr...),
			TotalAmtMsat: amountMsat,
		}
		finalHop.TlvPayload = true
		finalHop.TotalAmtMsat = uint64(amountMsat)
	}
	return cloned, nil
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

func paymentTimeoutSeconds(ctx context.Context, fallback int32) int32 {
	if fallback <= 0 {
		fallback = 60
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return fallback
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	seconds := int32(math.Floor(remaining.Seconds())) - 2
	if seconds < 1 {
		return 1
	}
	if seconds > fallback {
		return fallback
	}
	return seconds
}

func defaultRouterPaymentFeeLimitMsat(ctx context.Context, c *Client, paymentRequest string) int64 {
	decoded, err := c.DecodeInvoice(ctx, paymentRequest)
	if err != nil {
		return defaultRouterPaymentFeeLimitMsatForDecodedInvoice(DecodedInvoice{})
	}
	return defaultRouterPaymentFeeLimitMsatForDecodedInvoice(decoded)
}

func defaultRouterPaymentFeeLimitMsatForDecodedInvoice(decoded DecodedInvoice) int64 {
	const maxFeeLimitMsat = int64(^uint64(0) >> 1)

	amountMsat := decoded.AmountMsat
	if amountMsat <= 0 && decoded.AmountSat > 0 {
		amountMsat = decoded.AmountSat * 1000
	}
	if amountMsat <= 0 {
		return maxFeeLimitMsat
	}
	if amountMsat <= 1_000_000 {
		return amountMsat
	}
	feeLimitMsat := amountMsat / 20
	if amountMsat%20 != 0 {
		feeLimitMsat++
	}
	if feeLimitMsat <= 0 {
		return maxFeeLimitMsat
	}
	return feeLimitMsat
}

func msatToSatCeil(msat int64) int64 {
	if msat <= 0 {
		return 0
	}
	return (msat + 999) / 1000
}

func paymentPreviewFeeHeadroomSat(feeMsat int64) int64 {
	if feeMsat <= 0 {
		return 1
	}
	return msatToSatCeil((feeMsat*125 + 99) / 100)
}

const (
	paymentRouteLiquidityProbeTimeout      = 4 * time.Second
	paymentRouteProbeStatusLikely          = "likely_liquid"
	paymentRouteProbeStatusFailed          = "failed"
	paymentRouteProbeStatusTimeout         = "timeout"
	paymentRouteProbeStatusUnknown         = "unknown"
	paymentRoutePreviewBaseQueryCount      = 200
	paymentRoutePreviewMaxQueryCount       = 1200
	paymentRoutePreviewMaxCandidates       = 800
	paymentRoutePreviewMaxProbes           = 80
	paymentRoutePreviewRemoteMaxCandidates = 240
	paymentMPPPlanMaxParts                 = 20
	paymentMPPPlanProbeBatchSize           = 5
	paymentMPPPlanMinShardMsat             = 25_000_000
	paymentMPPPlanMaxShardMsat             = 500_000_000
)

func routeTotalFeeMsat(route *lnrpc.Route) int64 {
	if route == nil {
		return 0
	}
	if route.TotalFeesMsat > 0 {
		return route.TotalFeesMsat
	}
	if route.TotalFees > 0 {
		return route.TotalFees * 1000
	}
	var total int64
	for _, hop := range route.Hops {
		if hop == nil {
			continue
		}
		if hop.FeeMsat > 0 {
			total += hop.FeeMsat
			continue
		}
		if hop.Fee > 0 {
			total += hop.Fee * 1000
		}
	}
	return total
}

func paymentRouteTotalFeeMsat(route PaymentRouteSummary) int64 {
	if route.TotalFeesMsat > 0 {
		return route.TotalFeesMsat
	}
	if route.TotalFeesSat > 0 {
		return route.TotalFeesSat * 1000
	}
	var total int64
	for _, hop := range route.Hops {
		if hop.FeeMsat > 0 {
			total += hop.FeeMsat
			continue
		}
		if hop.FeeSat > 0 {
			total += hop.FeeSat * 1000
		}
	}
	return total
}

func routeTotalAmountMsat(route *lnrpc.Route) int64 {
	if route == nil {
		return 0
	}
	if route.TotalAmtMsat > 0 {
		return route.TotalAmtMsat
	}
	if route.TotalAmt > 0 {
		return route.TotalAmt * 1000
	}
	return 0
}

func routeFinalForwardMsat(route *lnrpc.Route) int64 {
	if route == nil || len(route.Hops) == 0 {
		return 0
	}
	finalHop := route.Hops[len(route.Hops)-1]
	if finalHop == nil {
		return 0
	}
	if finalHop.AmtToForwardMsat > 0 {
		return finalHop.AmtToForwardMsat
	}
	if finalHop.AmtToForward > 0 {
		return finalHop.AmtToForward * 1000
	}
	total := routeTotalAmountMsat(route)
	if total <= 0 {
		return 0
	}
	fees := routeTotalFeeMsat(route)
	if total > fees {
		return total - fees
	}
	return total
}

func routeKey(route *lnrpc.Route) string {
	if route == nil || len(route.Hops) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, hop := range route.Hops {
		if hop == nil {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('|')
		}
		builder.WriteString(strconv.FormatUint(hop.ChanId, 10))
		builder.WriteByte(':')
		builder.WriteString(strings.TrimSpace(hop.PubKey))
	}
	return builder.String()
}

func encodePaymentRouteToken(route *lnrpc.Route) string {
	if route == nil {
		return ""
	}
	encoded, err := proto.Marshal(route)
	if err != nil || len(encoded) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func decodePaymentRouteToken(token string) (*lnrpc.Route, error) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return nil, errors.New("validated route token required")
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, errors.New("invalid validated route token")
	}
	route := &lnrpc.Route{}
	if err := proto.Unmarshal(decoded, route); err != nil {
		return nil, errors.New("invalid validated route token")
	}
	return route, nil
}

func validatePaymentRouteForInvoice(route *lnrpc.Route, decoded DecodedInvoice, amountMsat int64, maxFeeSat int64) error {
	if route == nil || len(route.Hops) == 0 || route.Hops[len(route.Hops)-1] == nil {
		return errors.New("validated route is empty")
	}
	if amountMsat <= 0 {
		return errors.New("amount required")
	}
	finalHop := route.Hops[len(route.Hops)-1]
	if destination := strings.TrimSpace(decoded.Destination); destination != "" && strings.TrimSpace(finalHop.PubKey) != destination {
		return errors.New("validated route destination mismatch")
	}
	if finalForward := routeFinalForwardMsat(route); finalForward > 0 && finalForward != amountMsat {
		return fmt.Errorf("validated route amount mismatch: route forwards %d msat, invoice requires %d msat", finalForward, amountMsat)
	}
	if maxFeeSat > 0 && routeTotalFeeMsat(route) > maxFeeSat*1000 {
		return errors.New("validated route exceeds max fee")
	}
	return nil
}

func paymentRouteProbeFailureDescription(probe PaymentRouteProbe) string {
	if code := strings.TrimSpace(probe.FailureCode); code != "" {
		return code
	}
	if status := strings.TrimSpace(probe.Status); status != "" {
		return status
	}
	return "unknown"
}

func routeFirstHopKey(route *lnrpc.Route) string {
	if route == nil || len(route.Hops) == 0 || route.Hops[0] == nil {
		return ""
	}
	hop := route.Hops[0]
	if hop.ChanId > 0 {
		return strconv.FormatUint(hop.ChanId, 10)
	}
	return strings.TrimSpace(hop.PubKey)
}

func edgeSetKey(edges []*lnrpc.EdgeLocator) string {
	if len(edges) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, edge := range edges {
		if edge == nil || edge.ChannelId == 0 {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('|')
		}
		builder.WriteString(strconv.FormatUint(edge.ChannelId, 10))
		builder.WriteByte(':')
		builder.WriteString(strconv.FormatBool(edge.DirectionReverse))
	}
	return builder.String()
}

func routeAlternativeIgnoredEdgeSets(route *lnrpc.Route, keepFirstHop bool) [][]*lnrpc.EdgeLocator {
	if route == nil || len(route.Hops) == 0 {
		return nil
	}
	sets := make([][]*lnrpc.EdgeLocator, 0, len(route.Hops))
	fullRouteEdges := make([]*lnrpc.EdgeLocator, 0, len(route.Hops)*2)
	for i, hop := range route.Hops {
		if hop == nil || hop.ChanId == 0 {
			continue
		}
		if keepFirstHop && i == 0 {
			continue
		}
		fullRouteEdges = append(fullRouteEdges, &lnrpc.EdgeLocator{
			ChannelId:        hop.ChanId,
			DirectionReverse: false,
		}, &lnrpc.EdgeLocator{
			ChannelId:        hop.ChanId,
			DirectionReverse: true,
		})
		sets = append(sets, []*lnrpc.EdgeLocator{
			{
				ChannelId:        hop.ChanId,
				DirectionReverse: false,
			},
			{
				ChannelId:        hop.ChanId,
				DirectionReverse: true,
			},
		})
	}
	if len(fullRouteEdges) > 0 {
		sets = append([][]*lnrpc.EdgeLocator{fullRouteEdges}, sets...)
	}
	return sets
}

func probePaymentRoutes(ctx context.Context, router routerrpc.RouterClient, routes []*lnrpc.Route) []PaymentRouteProbe {
	probes := make([]PaymentRouteProbe, len(routes))
	if len(routes) == 0 {
		return probes
	}

	var wg sync.WaitGroup
	for i, route := range routes {
		i, route := i, route
		wg.Add(1)
		go func() {
			defer wg.Done()
			probeCtx, cancel := context.WithTimeout(ctx, paymentRouteLiquidityProbeTimeout)
			defer cancel()
			probes[i] = probePaymentRoute(probeCtx, router, route)
		}()
	}
	wg.Wait()
	return probes
}

type probedPaymentRoute struct {
	route *lnrpc.Route
	probe PaymentRouteProbe
}

func probePaymentRouteCandidates(ctx context.Context, router routerrpc.RouterClient, routes []*lnrpc.Route, displayLimit int, probeLimit int) []probedPaymentRoute {
	if displayLimit <= 0 {
		displayLimit = 5
	}
	if probeLimit < displayLimit {
		probeLimit = displayLimit
	}
	if probeLimit > len(routes) {
		probeLimit = len(routes)
	}
	routes = selectProbeCandidateRoutes(routes, probeLimit, displayLimit)
	probeLimit = len(routes)
	probed := make([]probedPaymentRoute, 0, probeLimit)
	for start := 0; start < probeLimit; start += displayLimit {
		end := start + displayLimit
		if end > probeLimit {
			end = probeLimit
		}
		batch := routes[start:end]
		probes := probePaymentRoutes(ctx, router, batch)
		foundLikely := false
		for i, route := range batch {
			probe := PaymentRouteProbe{Status: paymentRouteProbeStatusUnknown}
			if i < len(probes) {
				probe = probes[i]
			}
			if probe.LikelyLiquid {
				foundLikely = true
			}
			probed = append(probed, probedPaymentRoute{
				route: route,
				probe: probe,
			})
		}
		if foundLikely {
			break
		}
	}
	return selectPreviewPaymentRoutes(probed, displayLimit)
}

func probeCheapestPaymentRouteCandidates(ctx context.Context, router routerrpc.RouterClient, routes []*lnrpc.Route, batchSize int, probeLimit int) []probedPaymentRoute {
	if batchSize <= 0 {
		batchSize = 5
	}
	if probeLimit < batchSize {
		probeLimit = batchSize
	}
	if probeLimit > len(routes) {
		probeLimit = len(routes)
	}
	probed := make([]probedPaymentRoute, 0, probeLimit)
	for start := 0; start < probeLimit; start += batchSize {
		end := start + batchSize
		if end > probeLimit {
			end = probeLimit
		}
		batch := routes[start:end]
		probes := probePaymentRoutes(ctx, router, batch)
		foundLikely := false
		for i, route := range batch {
			probe := PaymentRouteProbe{Status: paymentRouteProbeStatusUnknown}
			if i < len(probes) {
				probe = probes[i]
			}
			if probe.LikelyLiquid {
				foundLikely = true
			}
			probed = append(probed, probedPaymentRoute{
				route: route,
				probe: probe,
			})
		}
		if foundLikely {
			break
		}
	}
	return probed
}

func probePaymentRouteCandidatesExhaustive(ctx context.Context, router routerrpc.RouterClient, routes []*lnrpc.Route, batchSize int, probeLimit int) []probedPaymentRoute {
	if batchSize <= 0 {
		batchSize = paymentMPPPlanProbeBatchSize
	}
	if probeLimit <= 0 || probeLimit > len(routes) {
		probeLimit = len(routes)
	}
	routes = selectProbeCandidateRoutes(routes, probeLimit, batchSize)
	probeLimit = len(routes)
	probed := make([]probedPaymentRoute, 0, probeLimit)
	for start := 0; start < probeLimit; start += batchSize {
		if err := ctx.Err(); err != nil {
			break
		}
		end := start + batchSize
		if end > probeLimit {
			end = probeLimit
		}
		batch := routes[start:end]
		probes := probePaymentRoutes(ctx, router, batch)
		for i, route := range batch {
			probe := PaymentRouteProbe{Status: paymentRouteProbeStatusUnknown}
			if i < len(probes) {
				probe = probes[i]
			}
			probed = append(probed, probedPaymentRoute{
				route: route,
				probe: probe,
			})
		}
	}
	return probed
}

func probedPaymentRoutesHaveLikely(probed []probedPaymentRoute) bool {
	for _, route := range probed {
		if route.probe.LikelyLiquid {
			return true
		}
	}
	return false
}

func selectProbeCandidateRoutes(routes []*lnrpc.Route, probeLimit int, cheapestLimit int) []*lnrpc.Route {
	if probeLimit <= 0 || len(routes) <= probeLimit {
		return routes
	}
	if cheapestLimit <= 0 {
		cheapestLimit = 5
	}
	selected := make([]*lnrpc.Route, 0, probeLimit)
	seenRoutes := make(map[string]struct{})
	addRoute := func(route *lnrpc.Route) bool {
		if route == nil {
			return false
		}
		key := routeKey(route)
		if key != "" {
			if _, exists := seenRoutes[key]; exists {
				return false
			}
			seenRoutes[key] = struct{}{}
		}
		selected = append(selected, route)
		return len(selected) >= probeLimit
	}

	for i := 0; i < len(routes) && i < cheapestLimit; i++ {
		if addRoute(routes[i]) {
			return selected
		}
	}

	seenFirstHops := make(map[string]struct{})
	for _, route := range routes {
		firstHop := routeFirstHopKey(route)
		if firstHop == "" {
			continue
		}
		if _, exists := seenFirstHops[firstHop]; exists {
			continue
		}
		seenFirstHops[firstHop] = struct{}{}
		if addRoute(route) {
			return selected
		}
	}

	for slot := 0; slot < cheapestLimit; slot++ {
		index := 0
		if cheapestLimit > 1 {
			index = (len(routes) - 1) * slot / (cheapestLimit - 1)
		}
		if index < 0 || index >= len(routes) {
			continue
		}
		if addRoute(routes[index]) {
			return selected
		}
	}

	for _, route := range routes {
		if addRoute(route) {
			return selected
		}
	}
	return selected
}

func selectPreviewPaymentRoutes(probed []probedPaymentRoute, displayLimit int) []probedPaymentRoute {
	if displayLimit <= 0 {
		displayLimit = 5
	}
	if len(probed) <= displayLimit {
		return probed
	}
	sorted := append([]probedPaymentRoute(nil), probed...)
	sort.SliceStable(sorted, func(i, j int) bool {
		leftFee := routeTotalFeeMsat(sorted[i].route)
		rightFee := routeTotalFeeMsat(sorted[j].route)
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		return len(sorted[i].route.Hops) < len(sorted[j].route.Hops)
	})

	selected := append([]probedPaymentRoute(nil), sorted[:displayLimit]...)
	for _, candidate := range selected {
		if candidate.probe.LikelyLiquid {
			return selected
		}
	}
	for _, candidate := range sorted[displayLimit:] {
		if !candidate.probe.LikelyLiquid {
			continue
		}
		selected[displayLimit-1] = candidate
		sort.SliceStable(selected, func(i, j int) bool {
			leftFee := routeTotalFeeMsat(selected[i].route)
			rightFee := routeTotalFeeMsat(selected[j].route)
			if leftFee != rightFee {
				return leftFee < rightFee
			}
			return len(selected[i].route.Hops) < len(selected[j].route.Hops)
		})
		return selected
	}
	return selectFeeSpreadPaymentRoutes(sorted, displayLimit)
}

func selectFeeSpreadPaymentRoutes(sorted []probedPaymentRoute, displayLimit int) []probedPaymentRoute {
	if displayLimit <= 0 || len(sorted) <= displayLimit {
		return sorted
	}
	selected := make([]probedPaymentRoute, 0, displayLimit)
	seen := make(map[string]struct{}, displayLimit)
	add := func(candidate probedPaymentRoute) {
		if len(selected) >= displayLimit {
			return
		}
		key := routeKey(candidate.route)
		if key != "" {
			if _, ok := seen[key]; ok {
				return
			}
			seen[key] = struct{}{}
		}
		selected = append(selected, candidate)
	}
	for slot := 0; slot < displayLimit; slot++ {
		index := 0
		if displayLimit > 1 {
			index = (len(sorted) - 1) * slot / (displayLimit - 1)
		}
		add(sorted[index])
	}
	for _, candidate := range sorted {
		add(candidate)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		leftFee := routeTotalFeeMsat(selected[i].route)
		rightFee := routeTotalFeeMsat(selected[j].route)
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		return len(selected[i].route.Hops) < len(selected[j].route.Hops)
	})
	return selected
}

func paymentRoutePreviewCandidateLimit(targetRoutes int, outgoingCount int) int {
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	limit := targetRoutes * 20
	if outgoingCount > 1 {
		perChannel := targetRoutes * 8
		if perChannel < 30 {
			perChannel = 30
		}
		if expanded := outgoingCount * perChannel; expanded > limit {
			limit = expanded
		}
	}
	if limit > paymentRoutePreviewMaxCandidates {
		limit = paymentRoutePreviewMaxCandidates
	}
	if limit < targetRoutes {
		return targetRoutes
	}
	return limit
}

func paymentRoutePreviewQueryLimit(targetRoutes int, outgoingCount int) int {
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	limit := paymentRoutePreviewBaseQueryCount
	if outgoingCount > 1 {
		if expanded := outgoingCount * targetRoutes * 10; expanded > limit {
			limit = expanded
		}
	}
	if limit > paymentRoutePreviewMaxQueryCount {
		return paymentRoutePreviewMaxQueryCount
	}
	return limit
}

func paymentRoutePreviewPerSearchSetQueryLimit(targetRoutes int, outgoingCount int) int {
	if outgoingCount <= 1 {
		return paymentRoutePreviewQueryLimit(targetRoutes, outgoingCount)
	}
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	limit := targetRoutes * 3
	if limit < 12 {
		return 12
	}
	return limit
}

func paymentRoutePreviewProbeLimit(routeCount int, targetRoutes int, outgoingCount int) int {
	if routeCount <= 0 {
		return 0
	}
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	limit := targetRoutes * 12
	if limit < 40 {
		limit = 40
	}
	if outgoingCount > 1 {
		if expanded := outgoingCount * 4; expanded > limit {
			limit = expanded
		}
	}
	if limit > paymentRoutePreviewMaxProbes {
		limit = paymentRoutePreviewMaxProbes
	}
	if limit > routeCount {
		return routeCount
	}
	return limit
}

func paymentRoutePreviewRemoteSourceQueryLimit(targetRoutes int) int {
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	limit := targetRoutes * 4
	if limit < 20 {
		return 20
	}
	return limit
}

func (c *Client) previewPaymentRouteCandidates(ctx context.Context, lightning lnrpc.LightningClient, decoded DecodedInvoice, amountMsat int64, outgoingChanIDs []uint64, numRoutes int32) ([]*lnrpc.Route, error) {
	targetRoutes := int(numRoutes)
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	maxRouteCandidates := paymentRoutePreviewCandidateLimit(targetRoutes, len(outgoingChanIDs))
	maxRouteQueries := paymentRoutePreviewQueryLimit(targetRoutes, len(outgoingChanIDs))
	perSearchSetQueryLimit := paymentRoutePreviewPerSearchSetQueryLimit(targetRoutes, len(outgoingChanIDs))
	routeSearchMaxFeeMsat := int64(^uint64(0) >> 1)
	routes := make([]*lnrpc.Route, 0, targetRoutes)
	seenRoutes := make(map[string]struct{})
	outgoingSearchSets := make([][]uint64, 0, len(outgoingChanIDs)+1)
	if len(outgoingChanIDs) > 1 {
		for _, outgoingChanID := range outgoingChanIDs {
			if outgoingChanID == 0 {
				continue
			}
			outgoingSearchSets = append(outgoingSearchSets, []uint64{outgoingChanID})
		}
	}
	outgoingSearchSets = append(outgoingSearchSets, append([]uint64(nil), outgoingChanIDs...))
	searchProfiles := []struct {
		useMissionControl bool
		timePref          float64
	}{
		{useMissionControl: false, timePref: -1},
		{useMissionControl: true, timePref: 0},
		{useMissionControl: true, timePref: 1},
	}
	var firstErr error
	queryAttempts := 0
	for _, profile := range searchProfiles {
		if queryAttempts >= maxRouteQueries || len(routes) >= maxRouteCandidates {
			break
		}
		for _, searchOutgoingChanIDs := range outgoingSearchSets {
			if queryAttempts >= maxRouteQueries || len(routes) >= maxRouteCandidates {
				break
			}
			ignoredQueue := [][]*lnrpc.EdgeLocator{nil}
			seenIgnoredSets := map[string]struct{}{"": {}}
			searchSetAttempts := 0
			for len(ignoredQueue) > 0 && queryAttempts < maxRouteQueries && len(routes) < maxRouteCandidates && searchSetAttempts < perSearchSetQueryLimit {
				ignoredEdges := ignoredQueue[0]
				ignoredQueue = ignoredQueue[1:]
				queryAttempts++
				searchSetAttempts++

				req := &lnrpc.QueryRoutesRequest{
					PubKey:            decoded.Destination,
					OutgoingChanIds:   append([]uint64(nil), searchOutgoingChanIDs...),
					UseMissionControl: profile.useMissionControl,
					TimePref:          profile.timePref,
				}
				if amountMsat > 0 {
					req.AmtMsat = amountMsat
				} else {
					req.Amt = decoded.AmountSat
				}
				req.FeeLimit = &lnrpc.FeeLimit{
					Limit: &lnrpc.FeeLimit_FixedMsat{FixedMsat: routeSearchMaxFeeMsat},
				}
				if len(decoded.BlindedPaths) > 0 {
					req.BlindedPaymentPaths = decoded.BlindedPaths
				} else {
					req.RouteHints = decoded.RouteHints
					req.DestFeatures = append([]lnrpc.FeatureBit(nil), decoded.DestFeatures...)
					if decoded.CltvExpiry > 0 {
						req.FinalCltvDelta = int32(decoded.CltvExpiry)
					}
				}
				if len(ignoredEdges) > 0 {
					req.IgnoredEdges = append([]*lnrpc.EdgeLocator(nil), ignoredEdges...)
				}

				queryResp, queryErr := lightning.QueryRoutes(ctx, req)
				if queryErr != nil {
					if firstErr == nil {
						firstErr = queryErr
					}
					continue
				}
				if queryResp == nil || len(queryResp.Routes) == 0 {
					continue
				}
				for _, route := range queryResp.Routes {
					if route == nil {
						continue
					}
					key := routeKey(route)
					if key != "" {
						if _, exists := seenRoutes[key]; exists {
							continue
						}
						seenRoutes[key] = struct{}{}
					}
					routes = append(routes, route)

					keepFirstHop := len(searchOutgoingChanIDs) == 1
					for _, edgeSet := range routeAlternativeIgnoredEdgeSets(route, keepFirstHop) {
						key := edgeSetKey(edgeSet)
						if key == "" {
							continue
						}
						if _, exists := seenIgnoredSets[key]; exists {
							continue
						}
						seenIgnoredSets[key] = struct{}{}
						ignoredQueue = append(ignoredQueue, edgeSet)
					}
				}
			}
		}
	}
	if len(routes) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.SliceStable(routes, func(i, j int) bool {
		leftFee := routeTotalFeeMsat(routes[i])
		rightFee := routeTotalFeeMsat(routes[j])
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		leftHops := len(routes[i].Hops)
		rightHops := len(routes[j].Hops)
		if leftHops != rightHops {
			return leftHops < rightHops
		}
		leftAmt := routeTotalAmountMsat(routes[i])
		rightAmt := routeTotalAmountMsat(routes[j])
		if leftAmt != rightAmt {
			return leftAmt < rightAmt
		}
		return routes[i].TotalTimeLock < routes[j].TotalTimeLock
	})
	return routes, nil
}

func (c *Client) remoteSourcePaymentRouteCandidates(ctx context.Context, lightning lnrpc.LightningClient, router routerrpc.RouterClient, decoded DecodedInvoice, amountMsat int64, outgoingChanIDs []uint64, numRoutes int32, existing []*lnrpc.Route) []*lnrpc.Route {
	if len(outgoingChanIDs) == 0 || amountMsat <= 0 {
		return nil
	}
	peerByChannelID := selectedOutgoingChannelPeers(ctx, lightning, outgoingChanIDs)
	if len(peerByChannelID) == 0 {
		return nil
	}

	targetRoutes := int(numRoutes)
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	maxCandidates := paymentRoutePreviewCandidateLimit(targetRoutes, len(outgoingChanIDs))
	if maxCandidates > paymentRoutePreviewRemoteMaxCandidates {
		maxCandidates = paymentRoutePreviewRemoteMaxCandidates
	}
	queryLimit := paymentRoutePreviewRemoteSourceQueryLimit(targetRoutes)
	seenRoutes := make(map[string]struct{}, len(existing))
	for _, route := range existing {
		if key := routeKey(route); key != "" {
			seenRoutes[key] = struct{}{}
		}
	}

	candidates := make([]*lnrpc.Route, 0, targetRoutes)
	for _, channelID := range outgoingChanIDs {
		if err := ctx.Err(); err != nil || len(candidates) >= maxCandidates {
			break
		}
		remotePubKey := strings.TrimSpace(peerByChannelID[channelID])
		if remotePubKey == "" {
			continue
		}
		sourceRoutes, err := queryPaymentRouteCandidatesFromSource(ctx, lightning, decoded, amountMsat, remotePubKey, queryLimit)
		if err != nil || len(sourceRoutes) == 0 {
			continue
		}
		for _, sourceRoute := range sourceRoutes {
			if len(candidates) >= maxCandidates {
				break
			}
			route, err := buildPaymentRouteFromSourceCandidate(ctx, router, decoded, amountMsat, channelID, remotePubKey, sourceRoute)
			if err != nil || route == nil {
				continue
			}
			key := routeKey(route)
			if key != "" {
				if _, exists := seenRoutes[key]; exists {
					continue
				}
				seenRoutes[key] = struct{}{}
			}
			candidates = append(candidates, route)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		leftFee := routeTotalFeeMsat(candidates[i])
		rightFee := routeTotalFeeMsat(candidates[j])
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		return len(candidates[i].Hops) < len(candidates[j].Hops)
	})
	return candidates
}

func selectedOutgoingChannelPeers(ctx context.Context, lightning lnrpc.LightningClient, outgoingChanIDs []uint64) map[uint64]string {
	if len(outgoingChanIDs) == 0 {
		return nil
	}
	selected := paymentChannelIDSet(outgoingChanIDs)
	resp, err := lightning.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true})
	if err != nil || resp == nil {
		return nil
	}
	peers := make(map[uint64]string, len(selected))
	for _, ch := range resp.Channels {
		if ch == nil || ch.ChanId == 0 {
			continue
		}
		if _, ok := selected[ch.ChanId]; !ok {
			continue
		}
		if remote := strings.TrimSpace(ch.RemotePubkey); remote != "" {
			peers[ch.ChanId] = remote
		}
	}
	return peers
}

func queryPaymentRouteCandidatesFromSource(ctx context.Context, lightning lnrpc.LightningClient, decoded DecodedInvoice, amountMsat int64, sourcePubKey string, queryLimit int) ([]*lnrpc.Route, error) {
	sourcePubKey = strings.TrimSpace(sourcePubKey)
	if sourcePubKey == "" || amountMsat <= 0 {
		return nil, nil
	}
	if queryLimit <= 0 {
		queryLimit = paymentRoutePreviewRemoteSourceQueryLimit(5)
	}
	routeSearchMaxFeeMsat := int64(^uint64(0) >> 1)
	routes := make([]*lnrpc.Route, 0, queryLimit)
	seenRoutes := make(map[string]struct{})
	profiles := []struct {
		useMissionControl bool
		timePref          float64
	}{
		{useMissionControl: false, timePref: -1},
		{useMissionControl: true, timePref: 0},
		{useMissionControl: true, timePref: 1},
	}
	var firstErr error
	queryAttempts := 0
	for _, profile := range profiles {
		if queryAttempts >= queryLimit || len(routes) >= queryLimit {
			break
		}
		ignoredQueue := [][]*lnrpc.EdgeLocator{nil}
		seenIgnoredSets := map[string]struct{}{"": {}}
		for len(ignoredQueue) > 0 && queryAttempts < queryLimit && len(routes) < queryLimit {
			if err := ctx.Err(); err != nil {
				return routes, nil
			}
			ignoredEdges := ignoredQueue[0]
			ignoredQueue = ignoredQueue[1:]
			queryAttempts++

			req := &lnrpc.QueryRoutesRequest{
				PubKey:            decoded.Destination,
				SourcePubKey:      sourcePubKey,
				AmtMsat:           amountMsat,
				UseMissionControl: profile.useMissionControl,
				TimePref:          profile.timePref,
				FeeLimit: &lnrpc.FeeLimit{
					Limit: &lnrpc.FeeLimit_FixedMsat{FixedMsat: routeSearchMaxFeeMsat},
				},
			}
			req.RouteHints = decoded.RouteHints
			req.DestFeatures = append([]lnrpc.FeatureBit(nil), decoded.DestFeatures...)
			if decoded.CltvExpiry > 0 {
				req.FinalCltvDelta = int32(decoded.CltvExpiry)
			}
			if len(ignoredEdges) > 0 {
				req.IgnoredEdges = append([]*lnrpc.EdgeLocator(nil), ignoredEdges...)
			}
			queryResp, queryErr := lightning.QueryRoutes(ctx, req)
			if queryErr != nil {
				if firstErr == nil {
					firstErr = queryErr
				}
				continue
			}
			if queryResp == nil || len(queryResp.Routes) == 0 {
				continue
			}
			for _, route := range queryResp.Routes {
				if route == nil {
					continue
				}
				key := routeKey(route)
				if key != "" {
					if _, exists := seenRoutes[key]; exists {
						continue
					}
					seenRoutes[key] = struct{}{}
				}
				routes = append(routes, route)
				for _, edgeSet := range routeAlternativeIgnoredEdgeSets(route, false) {
					key := edgeSetKey(edgeSet)
					if key == "" {
						continue
					}
					if _, exists := seenIgnoredSets[key]; exists {
						continue
					}
					seenIgnoredSets[key] = struct{}{}
					ignoredQueue = append(ignoredQueue, edgeSet)
				}
			}
		}
	}
	if len(routes) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sort.SliceStable(routes, func(i, j int) bool {
		leftFee := routeTotalFeeMsat(routes[i])
		rightFee := routeTotalFeeMsat(routes[j])
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		return len(routes[i].Hops) < len(routes[j].Hops)
	})
	return routes, nil
}

func buildPaymentRouteFromSourceCandidate(ctx context.Context, router routerrpc.RouterClient, decoded DecodedInvoice, amountMsat int64, outgoingChanID uint64, firstHopPubKey string, sourceRoute *lnrpc.Route) (*lnrpc.Route, error) {
	if outgoingChanID == 0 || strings.TrimSpace(firstHopPubKey) == "" || sourceRoute == nil {
		return nil, errors.New("invalid source route")
	}
	hopPubKeys := make([][]byte, 0, len(sourceRoute.Hops)+1)
	firstHop, err := decodeRoutePubKey(firstHopPubKey)
	if err != nil {
		return nil, err
	}
	hopPubKeys = append(hopPubKeys, firstHop)
	for _, hop := range sourceRoute.Hops {
		if hop == nil {
			continue
		}
		pubKey, err := decodeRoutePubKey(hop.PubKey)
		if err != nil {
			return nil, err
		}
		hopPubKeys = append(hopPubKeys, pubKey)
	}
	if len(hopPubKeys) == 0 {
		return nil, errors.New("empty source route")
	}
	req := &routerrpc.BuildRouteRequest{
		AmtMsat:        amountMsat,
		OutgoingChanId: outgoingChanID,
		HopPubkeys:     hopPubKeys,
	}
	if decoded.CltvExpiry > 0 {
		req.FinalCltvDelta = int32(decoded.CltvExpiry)
	}
	if len(decoded.PaymentAddr) > 0 {
		req.PaymentAddr = append([]byte(nil), decoded.PaymentAddr...)
	}
	resp, err := router.BuildRoute(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Route == nil {
		return nil, errors.New("empty built route")
	}
	return resp.Route, nil
}

func decodeRoutePubKey(pubKey string) ([]byte, error) {
	trimmed := strings.TrimSpace(pubKey)
	if trimmed == "" {
		return nil, errors.New("empty pubkey")
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil || len(decoded) != 33 {
		return nil, errors.New("invalid pubkey")
	}
	return decoded, nil
}

func (c *Client) buildMPPPaymentPlan(ctx context.Context, lightning lnrpc.LightningClient, router routerrpc.RouterClient, decoded DecodedInvoice, amountMsat int64, outgoingChanIDs []uint64) *PaymentMPPPlan {
	maxParts := mppPaymentMaxParts(len(outgoingChanIDs))
	shardCandidates := mppShardSizeCandidates(amountMsat, maxParts)
	if len(shardCandidates) == 0 {
		return nil
	}

	bestPartial := &PaymentMPPPlan{
		Available:    false,
		TotalAmtSat:  msatToSatCeil(amountMsat),
		TotalAmtMsat: amountMsat,
		MaxParts:     maxParts,
		Message:      "no mpp shard route validated liquidity",
	}
	for _, shardMsat := range shardCandidates {
		if err := ctx.Err(); err != nil {
			break
		}
		neededParts := int(ceilDivInt64(amountMsat, shardMsat))
		if neededParts <= 1 || neededParts > maxParts {
			continue
		}
		routes, err := c.previewPaymentRouteCandidates(ctx, lightning, decoded, shardMsat, outgoingChanIDs, 5)
		if err != nil || len(routes) == 0 {
			continue
		}
		probeLimit := mppPlanProbeLimit(neededParts, len(routes))
		probed := probePaymentRouteCandidatesExhaustive(ctx, router, routes, paymentMPPPlanProbeBatchSize, probeLimit)
		selected, coveredMsat := selectMPPLikelyRoutes(probed, amountMsat, maxParts)
		plan := c.mppPlanFromRoutes(ctx, lightning, selected, amountMsat, coveredMsat, maxParts, shardMsat)
		if plan == nil {
			continue
		}
		if plan.ValidatedAmtMsat > bestPartial.ValidatedAmtMsat {
			bestPartial = plan
		}
		if plan.Available {
			return plan
		}
	}
	if bestPartial.ValidatedAmtMsat > 0 {
		return bestPartial
	}
	return nil
}

func (c *Client) mppPlanFromRoutes(ctx context.Context, lightning lnrpc.LightningClient, routes []probedPaymentRoute, amountMsat int64, coveredMsat int64, maxParts int, shardMsat int64) *PaymentMPPPlan {
	if len(routes) == 0 {
		return nil
	}
	totalFeesMsat := int64(0)
	summaries := make([]PaymentRouteSummary, 0, len(routes))
	for _, candidate := range routes {
		if candidate.route == nil {
			continue
		}
		totalFeesMsat += routeTotalFeeMsat(candidate.route)
		summary := c.convertPaymentRouteWithClient(ctx, lightning, candidate.route)
		probe := candidate.probe
		summary.Probe = &probe
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		return nil
	}
	available := coveredMsat >= amountMsat
	message := "mpp plan validated liquidity"
	if !available {
		message = "partial mpp liquidity only"
	}
	return &PaymentMPPPlan{
		Available:          available,
		TotalAmtSat:        msatToSatCeil(amountMsat),
		TotalAmtMsat:       amountMsat,
		ValidatedAmtSat:    msatToSatCeil(coveredMsat),
		ValidatedAmtMsat:   coveredMsat,
		TotalFeesSat:       msatToSatCeil(totalFeesMsat),
		TotalFeesMsat:      totalFeesMsat,
		SuggestedMaxFeeSat: paymentPreviewFeeHeadroomSat(totalFeesMsat),
		MaxShardSat:        msatToSatCeil(shardMsat),
		MaxShardMsat:       shardMsat,
		PartCount:          len(summaries),
		MaxParts:           maxParts,
		Message:            message,
		Routes:             summaries,
	}
}

func (c *Client) buildPaymentPreviewRecommendation(ctx context.Context, lightning lnrpc.LightningClient, router routerrpc.RouterClient, decoded DecodedInvoice, amountMsat int64, outgoingChanIDs []uint64, numRoutes int32) *PaymentPreviewRecommendation {
	if len(outgoingChanIDs) == 0 || amountMsat <= 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	routes, err := c.previewPaymentRouteCandidates(ctx, lightning, decoded, amountMsat, nil, numRoutes)
	if err != nil || len(routes) == 0 {
		return &PaymentPreviewRecommendation{
			Type:    "automatic_lnd_no_route",
			Reason:  "automatic_lnd_no_route_candidates",
			Message: "automatic lnd returned no candidate route",
		}
	}
	targetRoutes := int(numRoutes)
	if targetRoutes <= 0 {
		targetRoutes = 5
	}
	selectedSet := paymentChannelIDSet(outgoingChanIDs)
	selected, selectedProbe, probedCount := probeCheapestPaymentRouteForRecommendation(ctx, router, routes, targetRoutes)
	if selected == nil || len(selected.Hops) == 0 || selected.Hops[0] == nil {
		recommendation := &PaymentPreviewRecommendation{
			Type:                "automatic_lnd_no_validated_route",
			Reason:              "automatic_lnd_probe_failed",
			CandidateRouteCount: len(routes),
			ProbedRouteCount:    probedCount,
			ProbeFailureCode:    selectedProbe.FailureCode,
			Message:             "automatic lnd found graph routes but no probe validated liquidity",
		}
		if selectedProbe.Status != "" {
			recommendation.ProbeStatus = selectedProbe.Status
		}
		return recommendation
	}

	firstHop := selected.Hops[0]
	feeMsat := routeTotalFeeMsat(selected)
	_, targetSelected := selectedSet[firstHop.ChanId]
	recommendationType := "rebalance_target"
	recommendationReason := "automatic_lnd_found_route_outside_selected_channels"
	recommendationMessage := "automatic lnd found a validated route through a channel outside the selected outgoing set"
	if targetSelected {
		recommendationType = "automatic_lnd_validated_route"
		recommendationReason = "automatic_lnd_found_route_using_selected_channel"
		recommendationMessage = "automatic lnd found a validated route through a channel already in the selected outgoing set"
	}
	recommendation := &PaymentPreviewRecommendation{
		Type:                    recommendationType,
		Reason:                  recommendationReason,
		TargetChannelID:         firstHop.ChanId,
		TargetChannelIDString:   strconv.FormatUint(firstHop.ChanId, 10),
		TargetChannelSelected:   targetSelected,
		TargetPubKey:            strings.TrimSpace(firstHop.PubKey),
		TargetAlias:             c.lookupNodeAliasWithClient(ctx, lightning, firstHop.PubKey),
		EstimatedPaymentFeeSat:  msatToSatCeil(feeMsat),
		EstimatedPaymentFeeMsat: feeMsat,
		HopCount:                len(selected.Hops),
		CandidateRouteCount:     len(routes),
		ProbedRouteCount:        probedCount,
		ProbeStatus:             selectedProbe.Status,
		Message:                 recommendationMessage,
	}
	if local := paymentPreviewLocalChannel(ctx, lightning, firstHop.ChanId); local != nil {
		recommendation.TargetChannelPoint = strings.TrimSpace(local.ChannelPoint)
		recommendation.TargetLocalBalanceSat = local.LocalBalance
		if recommendation.TargetPubKey == "" {
			recommendation.TargetPubKey = strings.TrimSpace(local.RemotePubkey)
		}
		if recommendation.TargetAlias == "" {
			recommendation.TargetAlias = strings.TrimSpace(local.PeerAlias)
		}
		if recommendation.TargetAlias == "" {
			recommendation.TargetAlias = c.lookupNodeAliasWithClient(ctx, lightning, local.RemotePubkey)
		}
	}
	return recommendation
}

func probeCheapestPaymentRouteForRecommendation(ctx context.Context, router routerrpc.RouterClient, routes []*lnrpc.Route, batchSize int) (*lnrpc.Route, PaymentRouteProbe, int) {
	if batchSize <= 0 {
		batchSize = 5
	}
	probeLimit := len(routes)
	probedCount := 0
	lastProbe := PaymentRouteProbe{Status: paymentRouteProbeStatusUnknown}
	for start := 0; start < probeLimit; start += batchSize {
		if err := ctx.Err(); err != nil {
			break
		}
		end := start + batchSize
		if end > probeLimit {
			end = probeLimit
		}
		batch := routes[start:end]
		probes := probePaymentRoutes(ctx, router, batch)
		for i, route := range batch {
			if route == nil || len(route.Hops) == 0 || route.Hops[0] == nil {
				continue
			}
			probe := PaymentRouteProbe{Status: paymentRouteProbeStatusUnknown}
			if i < len(probes) {
				probe = probes[i]
			}
			probedCount++
			lastProbe = probe
			if probe.LikelyLiquid {
				return route, probe, probedCount
			}
		}
	}
	return nil, lastProbe, probedCount
}

func paymentChannelIDSet(ids []uint64) map[uint64]struct{} {
	set := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		set[id] = struct{}{}
	}
	return set
}

func paymentPreviewLocalChannel(ctx context.Context, lightning lnrpc.LightningClient, channelID uint64) *lnrpc.Channel {
	if channelID == 0 {
		return nil
	}
	resp, err := lightning.ListChannels(ctx, &lnrpc.ListChannelsRequest{PeerAliasLookup: true})
	if err != nil || resp == nil {
		return nil
	}
	for _, ch := range resp.Channels {
		if ch == nil || ch.ChanId != channelID {
			continue
		}
		return ch
	}
	return nil
}

func mppPaymentMaxParts(outgoingChanCount int) int {
	maxParts := 10
	if outgoingChanCount > 0 {
		maxParts = outgoingChanCount
	}
	if maxParts < 4 {
		maxParts = 4
	}
	if maxParts > paymentMPPPlanMaxParts {
		maxParts = paymentMPPPlanMaxParts
	}
	return maxParts
}

func mppShardSizeCandidates(amountMsat int64, maxParts int) []int64 {
	if amountMsat <= 0 || maxParts < 2 {
		return nil
	}
	added := make(map[int64]struct{})
	candidates := make([]int64, 0, 8)
	add := func(shardMsat int64) {
		if shardMsat <= 0 || shardMsat >= amountMsat {
			return
		}
		if shardMsat > paymentMPPPlanMaxShardMsat {
			shardMsat = paymentMPPPlanMaxShardMsat
		}
		if shardMsat < paymentMPPPlanMinShardMsat {
			return
		}
		parts := ceilDivInt64(amountMsat, shardMsat)
		if parts <= 1 || parts > int64(maxParts) {
			return
		}
		if _, ok := added[shardMsat]; ok {
			return
		}
		added[shardMsat] = struct{}{}
		candidates = append(candidates, shardMsat)
	}
	for _, parts := range []int{2, 4, 5, 8, 10, maxParts} {
		if parts <= 1 {
			continue
		}
		add(ceilDivInt64(amountMsat, int64(parts)))
	}
	for _, shardMsat := range []int64{250_000_000, 200_000_000, 100_000_000, 50_000_000} {
		add(shardMsat)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i] > candidates[j]
	})
	return candidates
}

func mppPlanProbeLimit(neededParts int, routeCount int) int {
	if routeCount <= 0 {
		return 0
	}
	limit := neededParts * 3
	if limit < 15 {
		limit = 15
	}
	if limit > 50 {
		limit = 50
	}
	if limit > routeCount {
		limit = routeCount
	}
	return limit
}

func selectMPPLikelyRoutes(probed []probedPaymentRoute, amountMsat int64, maxParts int) ([]probedPaymentRoute, int64) {
	if amountMsat <= 0 || maxParts <= 0 {
		return nil, 0
	}
	likely := make([]probedPaymentRoute, 0, len(probed))
	for _, candidate := range probed {
		if candidate.route == nil || !candidate.probe.LikelyLiquid || routeFinalForwardMsat(candidate.route) <= 0 {
			continue
		}
		likely = append(likely, candidate)
	}
	sort.SliceStable(likely, func(i, j int) bool {
		leftAmt := routeFinalForwardMsat(likely[i].route)
		rightAmt := routeFinalForwardMsat(likely[j].route)
		leftFee := routeTotalFeeMsat(likely[i].route)
		rightFee := routeTotalFeeMsat(likely[j].route)
		if leftAmt > 0 && rightAmt > 0 && leftFee*rightAmt != rightFee*leftAmt {
			return leftFee*rightAmt < rightFee*leftAmt
		}
		if leftFee != rightFee {
			return leftFee < rightFee
		}
		return leftAmt > rightAmt
	})

	selected := make([]probedPaymentRoute, 0, maxParts)
	seenRoutes := make(map[string]struct{})
	seenFirstHops := make(map[string]struct{})
	coveredMsat := int64(0)
	add := func(candidate probedPaymentRoute, requireNewFirstHop bool) {
		if len(selected) >= maxParts || coveredMsat >= amountMsat {
			return
		}
		key := routeKey(candidate.route)
		if key != "" {
			if _, ok := seenRoutes[key]; ok {
				return
			}
		}
		firstHop := routeFirstHopKey(candidate.route)
		if requireNewFirstHop && firstHop != "" {
			if _, ok := seenFirstHops[firstHop]; ok {
				return
			}
		}
		if key != "" {
			seenRoutes[key] = struct{}{}
		}
		if firstHop != "" {
			seenFirstHops[firstHop] = struct{}{}
		}
		selected = append(selected, candidate)
		coveredMsat += routeFinalForwardMsat(candidate.route)
	}
	for _, candidate := range likely {
		add(candidate, true)
	}
	for _, candidate := range likely {
		add(candidate, false)
	}
	return selected, coveredMsat
}

func ceilDivInt64(value int64, divisor int64) int64 {
	if divisor <= 0 {
		return 0
	}
	if value <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func probePaymentRoute(ctx context.Context, router routerrpc.RouterClient, route *lnrpc.Route) PaymentRouteProbe {
	if route == nil || len(route.Hops) == 0 {
		return PaymentRouteProbe{
			Status:  paymentRouteProbeStatusUnknown,
			Message: "empty route",
		}
	}

	attempt, err := router.SendToRouteV2(ctx, &routerrpc.SendToRouteRequest{
		PaymentHash: probePaymentHashBytes(),
		Route:       route,
		SkipTempErr: false,
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded") {
			return PaymentRouteProbe{
				Status:  paymentRouteProbeStatusTimeout,
				Message: "probe timeout",
			}
		}
		return PaymentRouteProbe{
			Status:  paymentRouteProbeStatusUnknown,
			Message: strings.TrimSpace(err.Error()),
		}
	}
	if attempt == nil {
		return PaymentRouteProbe{
			Status:  paymentRouteProbeStatusUnknown,
			Message: "empty probe response",
		}
	}

	switch attempt.Status {
	case lnrpc.HTLCAttempt_SUCCEEDED:
		return PaymentRouteProbe{
			Status:       paymentRouteProbeStatusLikely,
			LikelyLiquid: true,
			Message:      "probe reached destination",
		}
	case lnrpc.HTLCAttempt_FAILED:
		return paymentRouteProbeFromFailure(attempt.Failure, route)
	default:
		return PaymentRouteProbe{
			Status:  paymentRouteProbeStatusUnknown,
			Message: attempt.Status.String(),
		}
	}
}

func probePaymentHashBytes() []byte {
	hash, err := hex.DecodeString(RandomPaymentHash())
	if err != nil || len(hash) != 32 {
		return bytes.Repeat([]byte{1}, 32)
	}
	return hash
}

func paymentRouteProbeFromFailure(failure *lnrpc.Failure, route *lnrpc.Route) PaymentRouteProbe {
	if failure == nil {
		return PaymentRouteProbe{
			Status:  paymentRouteProbeStatusUnknown,
			Message: "no failure details",
		}
	}

	probe := PaymentRouteProbe{
		Status:             paymentRouteProbeStatusFailed,
		FailureCode:        failure.Code.String(),
		FailureSourceIndex: failure.FailureSourceIndex,
	}
	if failure.FailureSourceIndex > 0 {
		probe.FailureHopIndex = int(failure.FailureSourceIndex)
	}
	if paymentRouteProbeReachedDestination(failure, route) {
		probe.Status = paymentRouteProbeStatusLikely
		probe.LikelyLiquid = true
		probe.Message = "destination rejected fake payment hash"
	}
	return probe
}

func paymentRouteProbeReachedDestination(failure *lnrpc.Failure, route *lnrpc.Route) bool {
	if failure == nil {
		return false
	}
	switch failure.Code {
	case lnrpc.Failure_INCORRECT_OR_UNKNOWN_PAYMENT_DETAILS,
		lnrpc.Failure_INCORRECT_PAYMENT_AMOUNT,
		lnrpc.Failure_FINAL_INCORRECT_HTLC_AMOUNT,
		lnrpc.Failure_FINAL_INCORRECT_CLTV_EXPIRY,
		lnrpc.Failure_FINAL_EXPIRY_TOO_SOON:
	default:
		return false
	}
	if route == nil || len(route.Hops) == 0 {
		return true
	}
	sourceIndex := int(failure.FailureSourceIndex)
	return sourceIndex == 0 || sourceIndex >= len(route.Hops)
}

type PaymentRouteHop struct {
	PubKey             string `json:"pubkey"`
	Alias              string `json:"alias,omitempty"`
	ChannelID          uint64 `json:"channel_id,omitempty"`
	ChannelCapacitySat int64  `json:"channel_capacity_sat,omitempty"`
	AmtToForwardSat    int64  `json:"amt_to_forward_sat,omitempty"`
	AmtToForwardMsat   int64  `json:"amt_to_forward_msat,omitempty"`
	FeeSat             int64  `json:"fee_sat,omitempty"`
	FeeMsat            int64  `json:"fee_msat,omitempty"`
	Expiry             uint32 `json:"expiry,omitempty"`
}

type PaymentRouteProbe struct {
	Status             string `json:"status,omitempty"`
	LikelyLiquid       bool   `json:"likely_liquid,omitempty"`
	FailureCode        string `json:"failure_code,omitempty"`
	FailureSourceIndex uint32 `json:"failure_source_index,omitempty"`
	FailureHopIndex    int    `json:"failure_hop_index,omitempty"`
	Message            string `json:"message,omitempty"`
}

type PaymentRouteSummary struct {
	RouteKey      string             `json:"route_key,omitempty"`
	RouteToken    string             `json:"route_token,omitempty"`
	TotalAmtSat   int64              `json:"total_amt_sat,omitempty"`
	TotalAmtMsat  int64              `json:"total_amt_msat,omitempty"`
	TotalFeesSat  int64              `json:"total_fees_sat,omitempty"`
	TotalFeesMsat int64              `json:"total_fees_msat,omitempty"`
	TotalTimeLock uint32             `json:"total_time_lock,omitempty"`
	HopCount      int                `json:"hop_count"`
	Hops          []PaymentRouteHop  `json:"hops,omitempty"`
	Probe         *PaymentRouteProbe `json:"probe,omitempty"`
}

type PaymentMPPPlan struct {
	Available          bool                  `json:"available"`
	TotalAmtSat        int64                 `json:"total_amt_sat,omitempty"`
	TotalAmtMsat       int64                 `json:"total_amt_msat,omitempty"`
	ValidatedAmtSat    int64                 `json:"validated_amt_sat,omitempty"`
	ValidatedAmtMsat   int64                 `json:"validated_amt_msat,omitempty"`
	TotalFeesSat       int64                 `json:"total_fees_sat,omitempty"`
	TotalFeesMsat      int64                 `json:"total_fees_msat,omitempty"`
	SuggestedMaxFeeSat int64                 `json:"suggested_max_fee_sat,omitempty"`
	MaxShardSat        int64                 `json:"max_shard_sat,omitempty"`
	MaxShardMsat       int64                 `json:"max_shard_msat,omitempty"`
	PartCount          int                   `json:"part_count,omitempty"`
	MaxParts           int                   `json:"max_parts,omitempty"`
	Message            string                `json:"message,omitempty"`
	Routes             []PaymentRouteSummary `json:"routes,omitempty"`
}

type PaymentPreviewRecommendation struct {
	Type                    string `json:"type,omitempty"`
	Reason                  string `json:"reason,omitempty"`
	TargetChannelID         uint64 `json:"target_channel_id,omitempty"`
	TargetChannelIDString   string `json:"target_channel_id_string,omitempty"`
	TargetChannelSelected   bool   `json:"target_channel_selected,omitempty"`
	TargetChannelPoint      string `json:"target_channel_point,omitempty"`
	TargetAlias             string `json:"target_alias,omitempty"`
	TargetPubKey            string `json:"target_pubkey,omitempty"`
	TargetLocalBalanceSat   int64  `json:"target_local_balance_sat,omitempty"`
	EstimatedPaymentFeeSat  int64  `json:"estimated_payment_fee_sat,omitempty"`
	EstimatedPaymentFeeMsat int64  `json:"estimated_payment_fee_msat,omitempty"`
	HopCount                int    `json:"hop_count,omitempty"`
	CandidateRouteCount     int    `json:"candidate_route_count,omitempty"`
	ProbedRouteCount        int    `json:"probed_route_count,omitempty"`
	ProbeStatus             string `json:"probe_status,omitempty"`
	ProbeFailureCode        string `json:"probe_failure_code,omitempty"`
	Message                 string `json:"message,omitempty"`
}

type PaymentProbeEstimate struct {
	Success       bool   `json:"success"`
	FeeSat        int64  `json:"fee_sat,omitempty"`
	FeeMsat       int64  `json:"fee_msat,omitempty"`
	TimeLockDelay int64  `json:"time_lock_delay,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
}

type PaymentPreview struct {
	PaymentRequest      string                        `json:"payment_request,omitempty"`
	AmountSat           int64                         `json:"amount_sat,omitempty"`
	AmountMsat          int64                         `json:"amount_msat,omitempty"`
	Memo                string                        `json:"memo,omitempty"`
	Destination         string                        `json:"destination,omitempty"`
	SuggestedMaxFeeSat  int64                         `json:"suggested_max_fee_sat,omitempty"`
	SuggestedMaxFeeMsat int64                         `json:"suggested_max_fee_msat,omitempty"`
	EffectiveMaxFeeSat  int64                         `json:"effective_max_fee_sat,omitempty"`
	EffectiveMaxFeeMsat int64                         `json:"effective_max_fee_msat,omitempty"`
	LiquidityValidated  bool                          `json:"liquidity_validated"`
	ValidatedRouteCount int                           `json:"validated_route_count,omitempty"`
	Probe               PaymentProbeEstimate          `json:"probe"`
	Routes              []PaymentRouteSummary         `json:"routes,omitempty"`
	MPPPlan             *PaymentMPPPlan               `json:"mpp_plan,omitempty"`
	Recommendation      *PaymentPreviewRecommendation `json:"recommendation,omitempty"`
}

type PaymentDetails struct {
	PaymentHash    string               `json:"payment_hash,omitempty"`
	PaymentRequest string               `json:"payment_request,omitempty"`
	Status         string               `json:"status,omitempty"`
	ValueSat       int64                `json:"value_sat,omitempty"`
	ValueMsat      int64                `json:"value_msat,omitempty"`
	FeeSat         int64                `json:"fee_sat,omitempty"`
	FeeMsat        int64                `json:"fee_msat,omitempty"`
	CreatedAt      time.Time            `json:"created_at,omitempty"`
	Route          *PaymentRouteSummary `json:"route,omitempty"`
}

func (c *Client) PreviewPayment(ctx context.Context, paymentRequest string, outgoingChanIDs []uint64, maxFeeSat int64, numRoutes int32) (PaymentPreview, error) {
	trimmed := strings.TrimSpace(paymentRequest)
	if trimmed == "" {
		return PaymentPreview{}, errors.New("payment_request required")
	}
	if numRoutes <= 0 {
		numRoutes = 5
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return PaymentPreview{}, err
	}
	defer conn.Close()

	lightning := lnrpc.NewLightningClient(conn)
	router := routerrpc.NewRouterClient(conn)

	decoded, amountMsat, err := decodePaymentRequestWithClient(ctx, lightning, trimmed)
	if err != nil {
		return PaymentPreview{}, err
	}
	if amountMsat <= 0 {
		return PaymentPreview{}, errors.New("amountless invoices are not supported for route preview")
	}
	if len(decoded.BlindedPaths) > 0 {
		return PaymentPreview{}, errors.New("route preview is unavailable for blinded invoices")
	}

	suggestedMaxFeeMsat := defaultRouterPaymentFeeLimitMsatForDecodedInvoice(decoded)
	suggestedMaxFeeSat := msatToSatCeil(suggestedMaxFeeMsat)
	effectiveMaxFeeSat := suggestedMaxFeeSat
	effectiveMaxFeeMsat := suggestedMaxFeeMsat
	if maxFeeSat > 0 {
		effectiveMaxFeeSat = maxFeeSat
		effectiveMaxFeeMsat = maxFeeSat * 1000
	}

	preview := PaymentPreview{
		PaymentRequest:      trimmed,
		AmountSat:           decoded.AmountSat,
		AmountMsat:          amountMsat,
		Memo:                decoded.Memo,
		Destination:         decoded.Destination,
		EffectiveMaxFeeSat:  effectiveMaxFeeSat,
		EffectiveMaxFeeMsat: effectiveMaxFeeMsat,
	}

	if len(outgoingChanIDs) == 0 {
		probeResp, probeErr := router.EstimateRouteFee(ctx, &routerrpc.RouteFeeRequest{
			PaymentRequest: trimmed,
			Timeout:        uint32(paymentTimeoutSeconds(ctx, 15)),
		})
		if probeErr == nil && probeResp != nil {
			preview.Probe.Success = probeResp.FailureReason == lnrpc.PaymentFailureReason_FAILURE_REASON_NONE
			preview.Probe.FeeMsat = probeResp.RoutingFeeMsat
			preview.Probe.FeeSat = msatToSatCeil(probeResp.RoutingFeeMsat)
			preview.Probe.TimeLockDelay = probeResp.TimeLockDelay
			if probeResp.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
				preview.Probe.FailureReason = probeResp.FailureReason.String()
			}
		}
	}

	targetRoutes := int(numRoutes)
	routes, err := c.previewPaymentRouteCandidates(ctx, lightning, decoded, amountMsat, outgoingChanIDs, numRoutes)
	if err != nil {
		return preview, err
	}
	probeLimit := paymentRoutePreviewProbeLimit(len(routes), targetRoutes, len(outgoingChanIDs))
	probedRoutes := probePaymentRouteCandidates(ctx, router, routes, targetRoutes, probeLimit)
	if len(outgoingChanIDs) > 0 && !probedPaymentRoutesHaveLikely(probedRoutes) {
		remoteSourceRoutes := c.remoteSourcePaymentRouteCandidates(ctx, lightning, router, decoded, amountMsat, outgoingChanIDs, numRoutes, routes)
		if len(remoteSourceRoutes) > 0 {
			remoteProbeLimit := paymentRoutePreviewProbeLimit(len(remoteSourceRoutes), targetRoutes, len(outgoingChanIDs))
			remoteProbedRoutes := probePaymentRouteCandidates(ctx, router, remoteSourceRoutes, targetRoutes, remoteProbeLimit)
			probedRoutes = selectPreviewPaymentRoutes(append(probedRoutes, remoteProbedRoutes...), targetRoutes)
		}
	}
	preview.Routes = make([]PaymentRouteSummary, 0, len(probedRoutes))
	for _, probed := range probedRoutes {
		route := probed.route
		summary := c.convertPaymentRouteWithClient(ctx, lightning, route)
		probe := probed.probe
		summary.Probe = &probe
		if probe.LikelyLiquid {
			preview.LiquidityValidated = true
			preview.ValidatedRouteCount++
		}
		preview.Routes = append(preview.Routes, summary)
	}
	if preview.LiquidityValidated {
		suggestedRouteIndex := -1
		for i, route := range preview.Routes {
			if route.Probe != nil && route.Probe.LikelyLiquid {
				suggestedRouteIndex = i
				break
			}
		}
		if suggestedRouteIndex >= 0 {
			suggested := paymentPreviewFeeHeadroomSat(paymentRouteTotalFeeMsat(preview.Routes[suggestedRouteIndex]))
			preview.SuggestedMaxFeeSat = suggested
			preview.SuggestedMaxFeeMsat = suggested * 1000
		}
	}
	if !preview.LiquidityValidated {
		if plan := c.buildMPPPaymentPlan(ctx, lightning, router, decoded, amountMsat, outgoingChanIDs); plan != nil {
			preview.MPPPlan = plan
			if plan.Available && plan.SuggestedMaxFeeSat > 0 {
				preview.SuggestedMaxFeeSat = plan.SuggestedMaxFeeSat
				preview.SuggestedMaxFeeMsat = plan.SuggestedMaxFeeSat * 1000
			}
		}
	}
	if !preview.LiquidityValidated && (preview.MPPPlan == nil || !preview.MPPPlan.Available) {
		preview.Recommendation = c.buildPaymentPreviewRecommendation(ctx, lightning, router, decoded, amountMsat, outgoingChanIDs, numRoutes)
	}
	return preview, nil
}

func (c *Client) PaymentDetails(ctx context.Context, paymentHash string, lookback time.Duration) (PaymentDetails, error) {
	trimmed := strings.ToLower(strings.TrimSpace(paymentHash))
	if trimmed == "" {
		return PaymentDetails{}, errors.New("payment_hash required")
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return PaymentDetails{}, err
	}
	defer conn.Close()

	lightning := lnrpc.NewLightningClient(conn)
	pay, err := lookupPaymentWithClient(ctx, lightning, trimmed, lookback)
	if err != nil {
		return PaymentDetails{}, err
	}
	if pay == nil {
		return PaymentDetails{}, errors.New("payment not found")
	}

	details := PaymentDetails{
		PaymentHash:    strings.ToLower(strings.TrimSpace(pay.PaymentHash)),
		PaymentRequest: strings.TrimSpace(pay.PaymentRequest),
		Status:         strings.TrimSpace(pay.Status.String()),
		ValueSat:       pay.ValueSat,
		ValueMsat:      pay.ValueMsat,
		FeeSat:         pay.FeeSat,
		FeeMsat:        pay.FeeMsat,
		CreatedAt:      recentPaymentTimestamp(pay),
	}
	if details.FeeSat <= 0 {
		details.FeeSat = msatToSatCeil(details.FeeMsat)
	}
	if route := recentRouteFromPayment(pay); route != nil {
		summary := c.convertPaymentRouteWithClient(ctx, lightning, route)
		details.Route = &summary
	}
	return details, nil
}

func (c *Client) convertPaymentRouteWithClient(ctx context.Context, client lnrpc.LightningClient, route *lnrpc.Route) PaymentRouteSummary {
	summary := PaymentRouteSummary{}
	if route == nil {
		return summary
	}
	summary.RouteKey = routeKey(route)
	summary.RouteToken = encodePaymentRouteToken(route)
	summary.TotalAmtSat = route.TotalAmt
	summary.TotalAmtMsat = route.TotalAmtMsat
	summary.TotalFeesSat = route.TotalFees
	summary.TotalFeesMsat = route.TotalFeesMsat
	if summary.TotalFeesMsat <= 0 {
		summary.TotalFeesMsat = routeTotalFeeMsat(route)
	}
	if summary.TotalFeesMsat > 0 {
		summary.TotalFeesSat = msatToSatCeil(summary.TotalFeesMsat)
	}
	summary.TotalTimeLock = route.TotalTimeLock
	summary.HopCount = len(route.Hops)
	if len(route.Hops) == 0 {
		return summary
	}
	summary.Hops = make([]PaymentRouteHop, 0, len(route.Hops))
	for _, hop := range route.Hops {
		if hop == nil {
			continue
		}
		feeMsat := hop.FeeMsat
		if feeMsat <= 0 && hop.Fee > 0 {
			feeMsat = hop.Fee * 1000
		}
		feeSat := hop.Fee
		if feeMsat > 0 {
			feeSat = msatToSatCeil(feeMsat)
		}
		summary.Hops = append(summary.Hops, PaymentRouteHop{
			PubKey:             strings.TrimSpace(hop.PubKey),
			Alias:              c.lookupNodeAliasWithClient(ctx, client, hop.PubKey),
			ChannelID:          hop.ChanId,
			ChannelCapacitySat: hop.ChanCapacity,
			AmtToForwardSat:    hop.AmtToForward,
			AmtToForwardMsat:   hop.AmtToForwardMsat,
			FeeSat:             feeSat,
			FeeMsat:            feeMsat,
			Expiry:             hop.Expiry,
		})
	}
	return summary
}

func lookupPaymentWithClient(ctx context.Context, client lnrpc.LightningClient, paymentHash string, lookback time.Duration) (*lnrpc.Payment, error) {
	trimmed := strings.ToLower(strings.TrimSpace(paymentHash))
	if trimmed == "" {
		return nil, nil
	}

	req := &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true,
		Reversed:          true,
		MaxPayments:       500,
	}
	cutoff := time.Time{}
	if lookback > 0 {
		cutoff = time.Now().Add(-lookback)
		start := cutoff.Unix()
		if start > 0 {
			req.CreationDateStart = uint64(start)
		}
	}

	var indexOffset uint64
	var lastOffset uint64
	for pages := 0; pages < walletActivityMaxPages; pages++ {
		req.IndexOffset = indexOffset
		resp, err := client.ListPayments(ctx, req)
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Payments) == 0 {
			break
		}

		oldestBeforeCutoff := false
		for _, pay := range resp.Payments {
			if pay == nil {
				continue
			}
			hash := strings.ToLower(strings.TrimSpace(pay.PaymentHash))
			if hash == trimmed {
				return pay, nil
			}
			if !cutoff.IsZero() && recentPaymentTimestamp(pay).Before(cutoff) {
				oldestBeforeCutoff = true
			}
		}

		nextOffset := resp.FirstIndexOffset
		if nextOffset == 0 {
			for _, pay := range resp.Payments {
				if pay == nil || pay.PaymentIndex == 0 {
					continue
				}
				if nextOffset == 0 || pay.PaymentIndex < nextOffset {
					nextOffset = pay.PaymentIndex
				}
			}
		}
		if nextOffset == 0 || nextOffset == indexOffset || nextOffset == lastOffset || len(resp.Payments) < int(req.MaxPayments) {
			break
		}
		if oldestBeforeCutoff {
			break
		}
		lastOffset = nextOffset
		indexOffset = nextOffset
	}
	return nil, nil
}

func payReqFeatureBits(features map[uint32]*lnrpc.Feature) []lnrpc.FeatureBit {
	if len(features) == 0 {
		return nil
	}
	bits := make([]lnrpc.FeatureBit, 0, len(features))
	for bit := range features {
		bits = append(bits, lnrpc.FeatureBit(bit))
	}
	sort.Slice(bits, func(i, j int) bool {
		return bits[i] < bits[j]
	})
	return bits
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
	paymentItems, payErr := listRecentPaymentsWithFallback(ctx, client, limit)
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
		for _, pay := range paymentItems {
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
			createdAt := time.Unix(inv.CreationDate, 0).UTC()
			settledAt := time.Time{}
			eventTime := createdAt
			if inv.SettleDate > 0 {
				settledAt = time.Unix(inv.SettleDate, 0).UTC()
				eventTime = settledAt
			}
			item := RecentActivity{
				Type:        "invoice",
				Network:     "lightning",
				Direction:   "in",
				AmountSat:   inv.Value,
				Memo:        inv.Memo,
				Timestamp:   eventTime,
				CreatedAt:   createdAt,
				SettledAt:   settledAt,
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
		for _, pay := range paymentItems {
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
				Timestamp:   recentPaymentTimestamp(pay),
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

func (c *Client) ListActivityRange(ctx context.Context, start time.Time, end time.Time, limit int) ([]RecentActivity, error) {
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if start.IsZero() {
		start = end.Add(-7 * 24 * time.Hour)
	}
	if end.Before(start) {
		start, end = end, start
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)

	invoices, err := listInvoicesInRange(ctx, client, start, end, limit)
	if err != nil {
		return nil, err
	}

	paymentItems, err := listPaymentsInRange(ctx, client, start, end, limit)
	if err != nil {
		return nil, err
	}

	lightningItems, err := c.buildLightningActivity(ctx, client, invoices, paymentItems)
	if err != nil {
		return nil, err
	}

	onchainItems, err := c.listOnchainRangeWithClient(ctx, client, start, end)
	if err != nil {
		return nil, err
	}

	items := append(lightningItems, onchainItems...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (c *Client) ListOnchainRange(ctx context.Context, start time.Time, end time.Time, limit int) ([]RecentActivity, error) {
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if start.IsZero() {
		start = end.Add(-7 * 24 * time.Hour)
	}
	if end.Before(start) {
		start, end = end, start
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	items, err := c.listOnchainRangeWithClient(ctx, client, start, end)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (c *Client) buildLightningActivity(ctx context.Context, client lnrpc.LightningClient, invoices []*lnrpc.Invoice, paymentItems []*lnrpc.Payment) ([]RecentActivity, error) {
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
	for _, pay := range paymentItems {
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

	items := make([]RecentActivity, 0, len(invoices)+len(paymentItems))
	for _, inv := range invoices {
		if inv == nil || inv.State != lnrpc.Invoice_SETTLED {
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
		amountSat := inv.Value
		if inv.AmtPaidSat > 0 {
			amountSat = inv.AmtPaidSat
		}
		createdAt := time.Unix(inv.CreationDate, 0).UTC()
		settledAt := time.Time{}
		eventTime := createdAt
		if inv.SettleDate > 0 {
			settledAt = time.Unix(inv.SettleDate, 0).UTC()
			eventTime = settledAt
		}
		item := RecentActivity{
			Type:        "invoice",
			Network:     "lightning",
			Direction:   "in",
			AmountSat:   amountSat,
			Memo:        inv.Memo,
			Timestamp:   eventTime,
			CreatedAt:   createdAt,
			SettledAt:   settledAt,
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

	for _, pay := range paymentItems {
		if pay == nil || pay.Status != lnrpc.Payment_SUCCEEDED {
			continue
		}
		if isSelfPayment(ctx, pubkey, client, pay) {
			continue
		}
		item := RecentActivity{
			Type:        "payment",
			Network:     "lightning",
			Direction:   "out",
			AmountSat:   pay.ValueSat,
			Memo:        pay.PaymentRequest,
			Timestamp:   recentPaymentTimestamp(pay),
			Status:      pay.Status.String(),
			FeeSat:      pay.FeeSat,
			Keysend:     isKeysendPayment(pay),
			PaymentHash: strings.ToLower(strings.TrimSpace(pay.PaymentHash)),
		}
		if route := recentRouteFromPayment(pay); route != nil && len(route.Hops) > 0 {
			firstHop := route.Hops[0]
			applyRecentChannel(&item, firstHop.ChanId, firstHop.PubKey)
		}
		items = append(items, item)
	}

	return items, nil
}

func listInvoicesInRange(ctx context.Context, client lnrpc.LightningClient, start time.Time, end time.Time, limit int) ([]*lnrpc.Invoice, error) {
	pageSize := walletActivityPageRequestSize(limit)
	items := make([]*lnrpc.Invoice, 0, int(pageSize))

	var indexOffset uint64
	var lastOffset uint64
	for pages := 0; pages < walletActivityMaxPages; pages++ {
		resp, err := client.ListInvoices(ctx, &lnrpc.ListInvoiceRequest{
			Reversed:       true,
			IndexOffset:    indexOffset,
			NumMaxInvoices: pageSize,
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Invoices) == 0 {
			break
		}

		for _, inv := range resp.Invoices {
			if inv == nil {
				continue
			}
			eventTime := recentInvoiceTimestamp(inv)
			if eventTime.Before(start) {
				continue
			}
			if eventTime.After(end) {
				continue
			}
			items = append(items, inv)
			if limit > 0 && len(items) >= limit {
				return items[:limit], nil
			}
		}

		nextOffset := resp.FirstIndexOffset
		if nextOffset == 0 {
			for _, inv := range resp.Invoices {
				if inv == nil || inv.AddIndex == 0 {
					continue
				}
				if nextOffset == 0 || inv.AddIndex < nextOffset {
					nextOffset = inv.AddIndex
				}
			}
		}
		if nextOffset == 0 || nextOffset == indexOffset || nextOffset == lastOffset || len(resp.Invoices) < int(pageSize) {
			break
		}
		lastOffset = nextOffset
		indexOffset = nextOffset
	}

	return items, nil
}

func listPaymentsInRange(ctx context.Context, client lnrpc.LightningClient, start time.Time, end time.Time, limit int) ([]*lnrpc.Payment, error) {
	pageSize := walletActivityPageRequestSize(limit)
	items := make([]*lnrpc.Payment, 0, int(pageSize))

	var indexOffset uint64
	var lastOffset uint64
	for pages := 0; pages < walletActivityMaxPages; pages++ {
		resp, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: true,
			Reversed:          true,
			IndexOffset:       indexOffset,
			MaxPayments:       pageSize,
		})
		if err != nil {
			return nil, err
		}
		if resp == nil || len(resp.Payments) == 0 {
			break
		}

		pageHasInRange := false
		oldestEventBeforeStart := false
		for _, pay := range resp.Payments {
			if pay == nil {
				continue
			}
			eventTime := recentPaymentTimestamp(pay)
			if eventTime.Before(start) {
				oldestEventBeforeStart = true
				continue
			}
			if eventTime.After(end) {
				continue
			}
			pageHasInRange = true
			items = append(items, pay)
			if limit > 0 && len(items) >= limit {
				return items[:limit], nil
			}
		}

		nextOffset := resp.FirstIndexOffset
		if nextOffset == 0 {
			for _, pay := range resp.Payments {
				if pay == nil || pay.PaymentIndex == 0 {
					continue
				}
				if nextOffset == 0 || pay.PaymentIndex < nextOffset {
					nextOffset = pay.PaymentIndex
				}
			}
		}
		if nextOffset == 0 || nextOffset == indexOffset || nextOffset == lastOffset || len(resp.Payments) < int(pageSize) {
			break
		}
		if oldestEventBeforeStart && !pageHasInRange {
			break
		}
		lastOffset = nextOffset
		indexOffset = nextOffset
	}

	return items, nil
}

func (c *Client) listOnchainRangeWithClient(ctx context.Context, client lnrpc.LightningClient, start time.Time, end time.Time) ([]RecentActivity, error) {
	startHeight := int32(0)
	if info, infoErr := client.GetInfo(ctx, &lnrpc.GetInfoRequest{}); infoErr == nil && info != nil && info.BlockHeight > 0 {
		rangeHours := end.Sub(start).Hours()
		if rangeHours > 0 {
			windowBlocks := int64(math.Ceil(rangeHours / 24 * float64(walletActivityApproxBlocksPerDay)))
			windowBlocks += walletActivityApproxBlocksPerDay
			if windowBlocks > 0 && int64(info.BlockHeight) > windowBlocks {
				startHeight = int32(int64(info.BlockHeight) - windowBlocks)
			}
		}
	}

	resp, err := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     startHeight,
		EndHeight:       -1,
	})
	if err != nil {
		return nil, err
	}

	items := make([]RecentActivity, 0, len(resp.Transactions))
	for _, tx := range resp.Transactions {
		if tx == nil || tx.Amount == 0 {
			continue
		}
		txTime := time.Unix(tx.TimeStamp, 0).UTC()
		if txTime.Before(start) || txTime.After(end) {
			continue
		}

		amount := tx.Amount
		direction := "in"
		if amount < 0 {
			direction = "out"
			amount *= -1
		}
		status := "PENDING"
		if tx.NumConfirmations > 0 {
			status = "CONFIRMED"
		}

		addresses := make([]string, 0, len(tx.OutputDetails))
		if len(tx.OutputDetails) > 0 {
			for _, out := range tx.OutputDetails {
				if out == nil || out.Address == "" {
					continue
				}
				addresses = append(addresses, out.Address)
			}
		}
		if len(addresses) == 0 && len(tx.DestAddresses) > 0 {
			addresses = append(addresses, tx.DestAddresses...)
		}

		items = append(items, RecentActivity{
			Type:          "onchain",
			Network:       "onchain",
			Direction:     direction,
			AmountSat:     amount,
			Memo:          tx.Label,
			Timestamp:     txTime,
			Status:        status,
			Txid:          tx.TxHash,
			FeeSat:        tx.TotalFees,
			Confirmations: tx.NumConfirmations,
			BlockHeight:   tx.BlockHeight,
			Addresses:     uniqueStrings(addresses),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Timestamp.After(items[j].Timestamp)
	})
	return items, nil
}

func walletActivityPageRequestSize(limit int) uint64 {
	if limit > 0 && limit < walletActivityPageSize {
		return uint64(limit)
	}
	return walletActivityPageSize
}

func walletActivityMaxInt64(left int64, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func listRecentPaymentsWithFallback(ctx context.Context, client lnrpc.LightningClient, limit int) ([]*lnrpc.Payment, error) {
	var lastErr error
	for _, pageSize := range recentPaymentPageSizes(limit) {
		resp, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: true,
			MaxPayments:       pageSize,
			Reversed:          true,
		})
		if err == nil {
			if resp == nil {
				return nil, nil
			}
			return resp.Payments, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func recentPaymentPageSizes(limit int) []uint64 {
	if limit <= 0 {
		limit = 20
	}
	candidates := []int{limit, 500, 200, 100, 50}
	seen := make(map[uint64]struct{}, len(candidates))
	sizes := make([]uint64, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate <= 0 {
			continue
		}
		size := uint64(candidate)
		if _, ok := seen[size]; ok {
			continue
		}
		seen[size] = struct{}{}
		sizes = append(sizes, size)
	}
	return sizes
}

func recentPaymentTimestamp(pay *lnrpc.Payment) time.Time {
	if pay == nil {
		return time.Time{}
	}
	if pay.CreationDate != 0 {
		return time.Unix(pay.CreationDate, 0).UTC()
	}
	if pay.CreationTimeNs != 0 {
		return time.Unix(0, pay.CreationTimeNs).UTC()
	}
	return time.Now().UTC()
}

func recentInvoiceTimestamp(inv *lnrpc.Invoice) time.Time {
	if inv == nil {
		return time.Time{}
	}
	if inv.SettleDate != 0 {
		return time.Unix(inv.SettleDate, 0).UTC()
	}
	if inv.CreationDate != 0 {
		return time.Unix(inv.CreationDate, 0).UTC()
	}
	return time.Now().UTC()
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
		addresses := make([]string, 0, len(tx.OutputDetails))
		if len(tx.OutputDetails) > 0 {
			for _, out := range tx.OutputDetails {
				if out == nil || out.Address == "" {
					continue
				}
				addresses = append(addresses, out.Address)
			}
		}
		if len(addresses) == 0 && len(tx.DestAddresses) > 0 {
			addresses = append(addresses, tx.DestAddresses...)
		}
		items = append(items, RecentActivity{
			Type:          "onchain",
			Network:       "onchain",
			Direction:     direction,
			AmountSat:     amount,
			Memo:          tx.Label,
			Timestamp:     time.Unix(tx.TimeStamp, 0).UTC(),
			Status:        status,
			Txid:          tx.TxHash,
			FeeSat:        tx.TotalFees,
			Confirmations: tx.NumConfirmations,
			BlockHeight:   tx.BlockHeight,
			Addresses:     uniqueStrings(addresses),
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
	pendingOpenPoints := make([]string, 0, len(resp.PendingOpenChannels))
	for _, item := range resp.PendingOpenChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		pendingOpenPoints = append(pendingOpenPoints, item.Channel.ChannelPoint)
	}
	openingSinceByPoint := c.snapshotPendingOpenSince(pendingOpenPoints, time.Now().UTC())
	var pendingOpenBumpByPoint map[string]pendingOpenBumpCandidate
	txResp, txErr := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
		MaxTransactions: 0,
		StartHeight:     0,
		EndHeight:       -1,
	})
	utxoResp, utxoErr := client.ListUnspent(ctx, &lnrpc.ListUnspentRequest{
		MinConfs: 0,
		MaxConfs: 2147483647,
	})
	pendingOpenBumpByPoint = detectPendingOpenBumpCandidates(
		txErr == nil && txResp != nil,
		func() []*lnrpc.Transaction {
			if txErr != nil || txResp == nil {
				return nil
			}
			return txResp.GetTransactions()
		}(),
		utxoErr == nil && utxoResp != nil,
		func() []*lnrpc.Utxo {
			if utxoErr != nil || utxoResp == nil {
				return nil
			}
			return utxoResp.GetUtxos()
		}(),
		pendingOpenPoints,
	)

	pending := []PendingChannelInfo{}
	for _, item := range resp.PendingOpenChannels {
		if item == nil || item.Channel == nil {
			continue
		}
		ch := item.Channel
		openingSinceUnix := int64(0)
		openingDurationSec := int64(0)
		if since, ok := openingSinceByPoint[normalizeChannelPointKey(ch.ChannelPoint)]; ok && !since.IsZero() {
			openingSinceUnix = since.Unix()
			if now := time.Now().UTC(); now.After(since) {
				openingDurationSec = int64(now.Sub(since).Seconds())
			}
		}
		bumpCandidate := pendingOpenBumpByPoint[normalizeChannelPointKey(ch.ChannelPoint)]
		pending = append(pending, PendingChannelInfo{
			ChannelPoint:             ch.ChannelPoint,
			RemotePubkey:             ch.RemoteNodePub,
			PeerAlias:                resolveAlias(ch.RemoteNodePub),
			CapacitySat:              ch.Capacity,
			LocalBalanceSat:          ch.LocalBalance,
			RemoteBalanceSat:         ch.RemoteBalance,
			Status:                   "opening",
			FundingFeeRateSatVbyte:   satPerVbyteFromSatPerKw(item.GetFeePerKw()),
			ConfirmationsUntilActive: item.ConfirmationsUntilActive,
			ConfirmationHeight:       item.ConfirmationHeight,
			OpeningSinceUnix:         openingSinceUnix,
			OpeningDurationSec:       openingDurationSec,
			FundingBumpChecked:       bumpCandidate.Checked,
			FundingBumpEligible:      bumpCandidate.Eligible,
			FundingBumpOutpoint:      bumpCandidate.Outpoint,
			FundingBumpAmountSat:     bumpCandidate.AmountSat,
			FundingBumpReason:        bumpCandidate.Reason,
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

func (c *Client) GetPeerNeighborRecommendations(ctx context.Context, sourcePubkey string, excludePubkeys map[string]struct{}, limit int) ([]PeerNeighborRecommendation, string, error) {
	trimmed := strings.TrimSpace(sourcePubkey)
	if trimmed == "" {
		return nil, "", errors.New("source pubkey required")
	}

	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, "", err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: trimmed, IncludeChannels: true})
	if err != nil {
		return nil, "", err
	}

	recommendations := buildPeerNeighborRecommendations(trimmed, info.GetChannels(), excludePubkeys)
	for i := range recommendations {
		nodeInfo, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: recommendations[i].PubKey, IncludeChannels: false})
		if err != nil || nodeInfo == nil || nodeInfo.GetNode() == nil {
			continue
		}
		node := nodeInfo.GetNode()
		if alias := strings.TrimSpace(node.GetAlias()); alias != "" {
			recommendations[i].Alias = alias
		}
		if count := int(nodeInfo.GetNumChannels()); count > 0 {
			recommendations[i].ChannelCount = count
		}
		if capacity := nodeInfo.GetTotalCapacity(); capacity > 0 {
			recommendations[i].TotalCapacitySat = capacity
		}
		host, hasClearnet := selectPreferredNodeAddress(node.GetAddresses())
		recommendations[i].HasClearnet = hasClearnet
		if host != "" {
			recommendations[i].Host = host
			recommendations[i].PeerAddress = recommendations[i].PubKey + "@" + host
		}
	}

	sort.Slice(recommendations, func(i, j int) bool {
		if recommendations[i].HasClearnet != recommendations[j].HasClearnet {
			return recommendations[i].HasClearnet
		}
		if recommendations[i].InboundFeeRatePpm != recommendations[j].InboundFeeRatePpm {
			return recommendations[i].InboundFeeRatePpm < recommendations[j].InboundFeeRatePpm
		}
		if recommendations[i].TotalCapacitySat != recommendations[j].TotalCapacitySat {
			return recommendations[i].TotalCapacitySat > recommendations[j].TotalCapacitySat
		}
		if recommendations[i].ChannelCount != recommendations[j].ChannelCount {
			return recommendations[i].ChannelCount > recommendations[j].ChannelCount
		}
		if recommendations[i].LargestCapacitySat != recommendations[j].LargestCapacitySat {
			return recommendations[i].LargestCapacitySat > recommendations[j].LargestCapacitySat
		}
		if recommendations[i].OutboundFeeRatePpm != recommendations[j].OutboundFeeRatePpm {
			return recommendations[i].OutboundFeeRatePpm < recommendations[j].OutboundFeeRatePpm
		}
		return recommendations[i].PubKey < recommendations[j].PubKey
	})

	selected, tier := selectPeerNeighborRecommendations(recommendations, limit)
	return selected, tier, nil
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

type peerNeighborCriteria struct {
	Name                string
	MinTotalCapacitySat int64
	MinChannelCount     int
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
			SharedChannelCount: item.ChannelCount,
			SharedCapacitySat:  item.TotalCapacitySat,
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

func selectPeerNeighborRecommendations(recommendations []PeerNeighborRecommendation, limit int) ([]PeerNeighborRecommendation, string) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	if len(recommendations) == 0 {
		return nil, "strict"
	}

	for _, criteria := range peerNeighborCriteriaTiers {
		filtered := filterPeerNeighborRecommendationsByCriteria(recommendations, criteria)
		if len(filtered) == 0 {
			continue
		}
		if len(filtered) > limit {
			filtered = filtered[:limit]
		}
		return filtered, criteria.Name
	}

	return nil, "strict"
}

func filterPeerNeighborRecommendationsByCriteria(recommendations []PeerNeighborRecommendation, criteria peerNeighborCriteria) []PeerNeighborRecommendation {
	filtered := make([]PeerNeighborRecommendation, 0, len(recommendations))
	for _, item := range recommendations {
		if item.TotalCapacitySat < criteria.MinTotalCapacitySat {
			continue
		}
		if item.ChannelCount < criteria.MinChannelCount {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func selectPreferredNodeAddress(addresses []*lnrpc.NodeAddress) (string, bool) {
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
			return addr, true
		}
	}
	return fallback, false
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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

type pendingOpenBumpCandidate struct {
	Checked   bool
	Eligible  bool
	Outpoint  string
	AmountSat int64
	Reason    string
}

type pendingOpenWalletOutput struct {
	Outpoint  string
	AmountSat int64
	Vout      uint32
}

func detectPendingOpenBumpCandidates(gotTransactions bool, transactions []*lnrpc.Transaction, gotUtxos bool, utxos []*lnrpc.Utxo, channelPoints []string) map[string]pendingOpenBumpCandidate {
	results := make(map[string]pendingOpenBumpCandidate, len(channelPoints))
	if len(channelPoints) == 0 {
		return results
	}

	utxosByTxid := make(map[string][]pendingOpenWalletOutput)
	for _, utxo := range utxos {
		if utxo == nil {
			continue
		}
		out := utxo.GetOutpoint()
		if out == nil {
			continue
		}
		txid, err := normalizeTxidHex(firstNonEmpty(out.GetTxidStr(), txidFromBytes(out.GetTxidBytes())))
		if err != nil {
			continue
		}
		utxosByTxid[txid] = append(utxosByTxid[txid], pendingOpenWalletOutput{
			Outpoint:  fmt.Sprintf("%s:%d", txid, out.GetOutputIndex()),
			AmountSat: utxo.GetAmountSat(),
			Vout:      out.GetOutputIndex(),
		})
	}

	txByID := make(map[string]*lnrpc.Transaction, len(transactions))
	for _, tx := range transactions {
		if tx == nil {
			continue
		}
		txid := strings.TrimSpace(tx.GetTxHash())
		if txid == "" {
			txid = txidFromRawTxHex(tx.GetRawTxHex())
		}
		normalized, err := normalizeTxidHex(txid)
		if err != nil {
			continue
		}
		txByID[normalized] = tx
	}

	for _, point := range channelPoints {
		key := normalizeChannelPointKey(point)
		if key == "" {
			continue
		}
		txid, fundingVout, err := channelPointOutpointInfo(point)
		if err != nil {
			results[key] = pendingOpenBumpCandidate{
				Checked: gotTransactions && gotUtxos,
				Reason:  pendingOpenBumpReasonChannelPointInvalid,
			}
			continue
		}

		if candidate, ok := selectPendingOpenBumpCandidate(utxosByTxid[txid], fundingVout); ok {
			results[key] = pendingOpenBumpCandidate{
				Checked:   true,
				Eligible:  true,
				Outpoint:  candidate.Outpoint,
				AmountSat: candidate.AmountSat,
			}
			continue
		}

		if !gotUtxos {
			results[key] = pendingOpenBumpCandidate{
				Checked: false,
				Reason:  pendingOpenBumpReasonUnavailable,
			}
			continue
		}

		tx, ok := txByID[txid]
		if !ok {
			results[key] = pendingOpenBumpCandidate{
				Checked: gotTransactions,
				Reason: func() string {
					if gotTransactions {
						return pendingOpenBumpReasonFundingTxUnavailable
					}
					return pendingOpenBumpReasonUnavailable
				}(),
			}
			continue
		}

		hasWalletOutput := false
		for _, out := range tx.GetOutputDetails() {
			if out == nil || !out.GetIsOurAddress() {
				continue
			}
			if uint32(out.GetOutputIndex()) == fundingVout {
				continue
			}
			hasWalletOutput = true
			break
		}
		if hasWalletOutput {
			results[key] = pendingOpenBumpCandidate{
				Checked: true,
				Reason:  pendingOpenBumpReasonWalletOutputUnavailable,
			}
			continue
		}

		results[key] = pendingOpenBumpCandidate{
			Checked: true,
			Reason:  pendingOpenBumpReasonNoWalletOutput,
		}
	}

	return results
}

func selectPendingOpenBumpCandidate(outputs []pendingOpenWalletOutput, fundingVout uint32) (pendingOpenWalletOutput, bool) {
	var best pendingOpenWalletOutput
	ok := false
	for _, output := range outputs {
		if output.Outpoint == "" || output.Vout == fundingVout {
			continue
		}
		if !ok || output.AmountSat > best.AmountSat {
			best = output
			ok = true
		}
	}
	return best, ok
}

func channelPointOutpointInfo(point string) (string, uint32, error) {
	trimmed := strings.TrimSpace(point)
	if trimmed == "" {
		return "", 0, errors.New("channel_point required")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) != 2 {
		return "", 0, errors.New("channel_point must be txid:index")
	}
	txid, err := normalizeTxidHex(parts[0])
	if err != nil {
		return "", 0, err
	}
	index, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return "", 0, errors.New("invalid channel_point index")
	}
	return txid, uint32(index), nil
}

func satPerVbyteFromSatPerKw(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return (value + 249) / 250
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

func (c *Client) snapshotPendingOpenSince(points []string, now time.Time) map[string]time.Time {
	c.channelStateMu.Lock()
	defer c.channelStateMu.Unlock()

	if c.channelPendingOpen == nil {
		c.channelPendingOpen = make(map[string]time.Time)
	}

	seen := make(map[string]struct{}, len(points))
	for _, point := range points {
		key := normalizeChannelPointKey(point)
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := c.channelPendingOpen[key]; !ok {
			c.channelPendingOpen[key] = now
		}
	}

	for key := range c.channelPendingOpen {
		if _, ok := seen[key]; !ok {
			delete(c.channelPendingOpen, key)
		}
	}

	out := make(map[string]time.Time, len(c.channelPendingOpen))
	for key, ts := range c.channelPendingOpen {
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
	HasClearnet        bool   `json:"has_clearnet"`
	ChannelCount       int    `json:"channel_count"`
	TotalCapacitySat   int64  `json:"total_capacity_sat"`
	SharedChannelCount int    `json:"shared_channel_count"`
	SharedCapacitySat  int64  `json:"shared_capacity_sat"`
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
	FundingFeeRateSatVbyte   int64  `json:"funding_fee_rate_sat_vb,omitempty"`
	ConfirmationsUntilActive uint32 `json:"confirmations_until_active,omitempty"`
	ConfirmationHeight       uint32 `json:"confirmation_height,omitempty"`
	OpeningSinceUnix         int64  `json:"opening_since_unix,omitempty"`
	OpeningDurationSec       int64  `json:"opening_duration_sec,omitempty"`
	FundingBumpChecked       bool   `json:"funding_bump_checked"`
	FundingBumpEligible      bool   `json:"funding_bump_eligible"`
	FundingBumpOutpoint      string `json:"funding_bump_outpoint,omitempty"`
	FundingBumpAmountSat     int64  `json:"funding_bump_amount_sat,omitempty"`
	FundingBumpReason        string `json:"funding_bump_reason,omitempty"`
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
	Type          string    `json:"type"`
	Network       string    `json:"network,omitempty"`
	Direction     string    `json:"direction,omitempty"`
	AmountSat     int64     `json:"amount_sat"`
	Memo          string    `json:"memo"`
	Timestamp     time.Time `json:"timestamp"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	SettledAt     time.Time `json:"settled_at,omitempty"`
	Status        string    `json:"status"`
	Txid          string    `json:"txid,omitempty"`
	FeeSat        int64     `json:"fee_sat,omitempty"`
	Confirmations int32     `json:"confirmations,omitempty"`
	BlockHeight   int32     `json:"block_height,omitempty"`
	Addresses     []string  `json:"addresses,omitempty"`
	Keysend       bool      `json:"keysend,omitempty"`
	ChannelID     uint64    `json:"channel_id,omitempty"`
	ChannelPoint  string    `json:"channel_point,omitempty"`
	ChannelAlias  string    `json:"channel_alias,omitempty"`
	PaymentHash   string    `json:"payment_hash,omitempty"`
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
