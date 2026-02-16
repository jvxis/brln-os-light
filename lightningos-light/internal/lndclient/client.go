package lndclient

import (
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

  "google.golang.org/grpc"
  "google.golang.org/grpc/credentials"
)

const recentOnchainWindowBlocks int64 = 20160

type Client struct {
  cfg *config.Config
  logger *log.Logger
  statusMu sync.Mutex
  statusCached bool
  statusCache Status
  statusErr error
  statusNextFetch time.Time
  infoCache infoSnapshot
  infoCacheAt time.Time
  infoCacheValid bool
}

func New(cfg *config.Config, logger *log.Logger) *Client {
  return &Client{cfg: cfg, logger: logger}
}

const (
  statusCacheOK = 30 * time.Second
  statusCacheErr = 45 * time.Second
  statusCacheTimeout = 60 * time.Second
  maxGRPCMsgSize = 32 * 1024 * 1024
  defaultConnectPeerTimeoutSec = uint64(8)
)

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
    Reversed: true,
    MaxPayments: 200,
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

type macaroonCredential struct {
  macaroon string
}

type BalanceSummary struct {
  OnchainSat int64
  LightningSat int64
  OnchainConfirmedSat int64
  OnchainUnconfirmedSat int64
  LightningLocalSat int64
  LightningUnsettledLocalSat int64
  Warnings []string
}

type ChannelPolicy struct {
  ChannelPoint string
  BaseFeeMsat int64
  FeeRatePpm int64
  TimeLockDelta int64
  MinHtlcMsat uint64
  MaxHtlcMsat uint64
  InboundBaseMsat int64
  InboundFeeRatePpm int64
}

type UpdateChannelPolicyParams struct {
  ChannelPoint string
  ApplyAll bool
  BaseFeeMsat int64
  FeeRatePpm int64
  TimeLockDelta int64
  InboundEnabled bool
  InboundBaseMsat int64
  InboundFeeRatePpm int64
  MaxHtlcMsat *uint64
  MinHtlcMsat *uint64
  MinHtlcMsatSpecified bool
}

type infoSnapshot struct {
  SyncedToChain bool
  SyncedToGraph bool
  BlockHeight int64
  Version string
  Pubkey string
  URI string
}

type DecodedInvoice struct {
  AmountSat int64
  AmountMsat int64
  Memo string
  Destination string
  PaymentHash string
  Expiry int64
  Timestamp int64
}

type CreatedInvoice struct {
  PaymentRequest string
  PaymentHash string
  PaymentAddr []byte
}

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
    AmountSat: resp.NumSatoshis,
    AmountMsat: resp.NumMsat,
    Memo: resp.Description,
    Destination: resp.Destination,
    PaymentHash: strings.ToLower(resp.PaymentHash),
    Expiry: resp.Expiry,
    Timestamp: resp.Timestamp,
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
    ChannelPoint: channelPoint,
    BaseFeeMsat: policy.FeeBaseMsat,
    FeeRatePpm: policy.FeeRateMilliMsat,
    TimeLockDelta: int64(policy.TimeLockDelta),
    MinHtlcMsat: maxInt64ToUint64(policy.MinHtlc),
    MaxHtlcMsat: policy.MaxHtlcMsat,
    InboundBaseMsat: int64(policy.InboundFeeBaseMsat),
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
    if len(info.Uris) > 0 {
      status.URI = info.Uris[0]
    }

    c.statusMu.Lock()
    c.infoCache = infoSnapshot{
      SyncedToChain: status.SyncedToChain,
      SyncedToGraph: status.SyncedToGraph,
      BlockHeight: status.BlockHeight,
      Version: status.Version,
      Pubkey: status.Pubkey,
      URI: status.URI,
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
    status.URI = cachedInfo.URI
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
    WalletPassword: []byte(walletPassword),
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
    Memo: memo,
    Value: amountSat,
    Expiry: expirySeconds,
  })
  if err != nil {
    return CreatedInvoice{}, err
  }

  return CreatedInvoice{
    PaymentRequest: resp.PaymentRequest,
    PaymentHash: strings.ToLower(hex.EncodeToString(resp.RHash)),
    PaymentAddr: resp.PaymentAddr,
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
  _, err = client.SendPaymentSync(ctx, req)
  return err
}

func (c *Client) SendCoins(ctx context.Context, address string, amountSat int64, satPerVbyte int64, sendAll bool) (string, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return "", err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  req := &lnrpc.SendCoinsRequest{
    Addr: address,
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
    MaxPayments: uint64(limit),
    Reversed: true,
  })
  pubkey := strings.TrimSpace(c.CachedPubkey())
  if pubkey == "" {
    if info, infoErr := client.GetInfo(ctx, &lnrpc.GetInfoRequest{}); infoErr == nil && info != nil {
      pubkey = strings.TrimSpace(info.IdentityPubkey)
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
      items = append(items, RecentActivity{
        Type: "invoice",
        Network: "lightning",
        Direction: "in",
        AmountSat: inv.Value,
        Memo: inv.Memo,
        Timestamp: time.Unix(inv.CreationDate, 0).UTC(),
        Status: inv.State.String(),
        Keysend: inv.IsKeysend,
        PaymentHash: hash,
      })
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
      items = append(items, RecentActivity{
        Type: "payment",
        Network: "lightning",
        Direction: "out",
        AmountSat: pay.ValueSat,
        Memo: pay.PaymentRequest,
        Timestamp: time.Unix(pay.CreationDate, 0).UTC(),
        Status: pay.Status.String(),
        Keysend: isKeysend,
        PaymentHash: strings.ToLower(pay.PaymentHash),
      })
    }
  }

  return items, nil
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
      Type: "onchain",
      Network: "onchain",
      Direction: direction,
      AmountSat: amount,
      Memo: tx.Label,
      Timestamp: time.Unix(tx.TimeStamp, 0).UTC(),
      Status: status,
      Txid: tx.TxHash,
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
      Txid: tx.TxHash,
      Direction: direction,
      AmountSat: amount,
      FeeSat: tx.TotalFees,
      Confirmations: tx.NumConfirmations,
      BlockHeight: tx.BlockHeight,
      Timestamp: time.Unix(tx.TimeStamp, 0).UTC(),
      Label: tx.Label,
      Addresses: uniqueStrings(addresses),
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
      Outpoint: outpoint,
      Txid: txid,
      Vout: vout,
      Address: utxo.Address,
      AddressType: addressTypeLabel(utxo.AddressType),
      AmountSat: utxo.AmountSat,
      Confirmations: utxo.Confirmations,
      PkScript: utxo.PkScript,
    })
  }

  return items, nil
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

  channels := make([]ChannelInfo, 0, len(resp.Channels))
  for _, ch := range resp.Channels {
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

    channels = append(channels, ChannelInfo{
      ChannelPoint: ch.ChannelPoint,
      ChannelID: ch.ChanId,
      RemotePubkey: ch.RemotePubkey,
      PeerAlias: ch.PeerAlias,
      Active: ch.Active,
      ChanStatusFlags: ch.ChanStatusFlags,
      LocalDisabled: localDisabled,
      Private: ch.Private,
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      LocalChanReserveSat: localReserveSat,
      UnsettledBalanceSat: ch.UnsettledBalance,
      PendingHtlcCount: len(ch.PendingHtlcs),
      BaseFeeMsat: baseFeeMsat,
      FeeRatePpm: feeRatePpm,
      InboundFeeRatePpm: inboundFeeRatePpm,
      PeerFeeRatePpm: peerFeeRatePpm,
      PeerBaseMsat: peerBaseMsat,
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
    info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: pubkey, IncludeChannels: false})
    if err == nil && info.GetNode() != nil {
      alias := info.GetNode().Alias
      if alias != "" {
        aliasMap[pubkey] = alias
        return alias
      }
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
      ChannelPoint: ch.ChannelPoint,
      RemotePubkey: ch.RemoteNodePub,
      PeerAlias: resolveAlias(ch.RemoteNodePub),
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      Status: "opening",
      ConfirmationsUntilActive: item.ConfirmationsUntilActive,
      Private: ch.Private,
    })
  }

  for _, item := range resp.PendingClosingChannels {
    if item == nil || item.Channel == nil {
      continue
    }
    ch := item.Channel
    pending = append(pending, PendingChannelInfo{
      ChannelPoint: ch.ChannelPoint,
      RemotePubkey: ch.RemoteNodePub,
      PeerAlias: resolveAlias(ch.RemoteNodePub),
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      Status: "closing",
      ClosingTxid: item.ClosingTxid,
      Private: ch.Private,
    })
  }

  for _, item := range resp.PendingForceClosingChannels {
    if item == nil || item.Channel == nil {
      continue
    }
    ch := item.Channel
    pending = append(pending, PendingChannelInfo{
      ChannelPoint: ch.ChannelPoint,
      RemotePubkey: ch.RemoteNodePub,
      PeerAlias: resolveAlias(ch.RemoteNodePub),
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      Status: "force_closing",
      ClosingTxid: item.ClosingTxid,
      BlocksTilMaturity: item.BlocksTilMaturity,
      LimboBalance: item.LimboBalance,
      Private: ch.Private,
    })
  }

  for _, item := range resp.WaitingCloseChannels {
    if item == nil || item.Channel == nil {
      continue
    }
    ch := item.Channel
    pending = append(pending, PendingChannelInfo{
      ChannelPoint: ch.ChannelPoint,
      RemotePubkey: ch.RemoteNodePub,
      PeerAlias: resolveAlias(ch.RemoteNodePub),
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      Status: "waiting_close",
      ClosingTxid: item.ClosingTxid,
      LimboBalance: item.LimboBalance,
      Private: ch.Private,
    })
  }

  return pending, nil
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
      info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: peer.PubKey, IncludeChannels: false})
      if err == nil && info.GetNode() != nil {
        alias = info.GetNode().Alias
      }
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
      PubKey: peer.PubKey,
      Alias: alias,
      Address: peer.Address,
      Inbound: peer.Inbound,
      BytesSent: peer.BytesSent,
      BytesRecv: peer.BytesRecv,
      SatSent: peer.SatSent,
      SatRecv: peer.SatRecv,
      PingTime: peer.PingTime,
      SyncType: peer.SyncType.String(),
      LastError: lastErr,
      LastErrorTime: lastErrTime,
    })
  }

  return peers, nil
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
      Host: host,
    },
    Perm: perm,
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
      Addr: item.Addr,
    })
  }

  return NodeDetails{
    PubKey: trimmed,
    Alias: node.Alias,
    Addresses: addresses,
  }, nil
}

func (c *Client) OpenChannel(ctx context.Context, pubkeyHex string, localFundingSat int64, closeAddress string, private bool, satPerVbyte int64) (string, error) {
  pubkeyHex = strings.TrimSpace(pubkeyHex)
  if pubkeyHex == "" {
    return "", errors.New("pubkey required")
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
    NodePubkey: pubkey,
    LocalFundingAmount: localFundingSat,
    Private: private,
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

func (c *Client) CloseChannel(ctx context.Context, channelPoint string, force bool, satPerVbyte int64) error {
  cp, err := parseChannelPoint(channelPoint)
  if err != nil {
    return err
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  req := &lnrpc.CloseChannelRequest{
    ChannelPoint: cp,
    Force: force,
    NoWait: true,
  }
  if satPerVbyte > 0 {
    req.SatPerVbyte = uint64(satPerVbyte)
  }
  stream, err := client.CloseChannel(ctx, req)
  if err != nil {
    return err
  }
  _, err = stream.Recv()
  if err != nil && !errors.Is(err, io.EOF) {
    return err
  }
  return nil
}

func (c *Client) UpdateChannelFees(ctx context.Context, channelPoint string, applyAll bool, baseFeeMsat int64, feeRatePpm int64, timeLockDelta int64, inboundEnabled bool, inboundBaseMsat int64, inboundFeeRatePpm int64) error {
  return c.UpdateChannelPolicy(ctx, UpdateChannelPolicyParams{
    ChannelPoint: channelPoint,
    ApplyAll: applyAll,
    BaseFeeMsat: baseFeeMsat,
    FeeRatePpm: feeRatePpm,
    TimeLockDelta: timeLockDelta,
    InboundEnabled: inboundEnabled,
    InboundBaseMsat: inboundBaseMsat,
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
    BaseFeeMsat: params.BaseFeeMsat,
    FeeRatePpm: uint32(params.FeeRatePpm),
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
      FeeRatePpm: int32(params.InboundFeeRatePpm),
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
    Action: action,
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
  ServiceActive bool
  WalletState string
  SyncedToChain bool
  SyncedToGraph bool
  BlockHeight int64
  Version string
  Pubkey string
  URI string
  InfoKnown bool
  InfoStale bool
  InfoAgeSeconds int64
  ChannelsActive int
  ChannelsInactive int
  OnchainSat int64
  LightningSat int64
}

type ChannelInfo struct {
  ChannelPoint string `json:"channel_point"`
  ChannelID uint64 `json:"channel_id"`
  RemotePubkey string `json:"remote_pubkey"`
  PeerAlias string `json:"peer_alias"`
  Active bool `json:"active"`
  ChanStatusFlags string `json:"chan_status_flags,omitempty"`
  LocalDisabled bool `json:"local_disabled,omitempty"`
  Private bool `json:"private"`
  CapacitySat int64 `json:"capacity_sat"`
  LocalBalanceSat int64 `json:"local_balance_sat"`
  RemoteBalanceSat int64 `json:"remote_balance_sat"`
  LocalChanReserveSat int64 `json:"local_chan_reserve_sat,omitempty"`
  UnsettledBalanceSat int64 `json:"unsettled_balance_sat,omitempty"`
  PendingHtlcCount int `json:"pending_htlc_count,omitempty"`
  BaseFeeMsat *int64 `json:"base_fee_msat,omitempty"`
  FeeRatePpm *int64 `json:"fee_rate_ppm,omitempty"`
  InboundFeeRatePpm *int64 `json:"inbound_fee_rate_ppm,omitempty"`
  PeerFeeRatePpm *int64 `json:"peer_fee_rate_ppm,omitempty"`
  PeerBaseMsat *int64 `json:"peer_base_msat,omitempty"`
  ClassLabel string `json:"class_label,omitempty"`
}

type PeerInfo struct {
  PubKey string `json:"pub_key"`
  Alias string `json:"alias"`
  Address string `json:"address"`
  Inbound bool `json:"inbound"`
  BytesSent uint64 `json:"bytes_sent"`
  BytesRecv uint64 `json:"bytes_recv"`
  SatSent int64 `json:"sat_sent"`
  SatRecv int64 `json:"sat_recv"`
  PingTime int64 `json:"ping_time"`
  SyncType string `json:"sync_type"`
  LastError string `json:"last_error"`
  LastErrorTime int64 `json:"last_error_time,omitempty"`
}

type NodeAddress struct {
  Network string `json:"network"`
  Addr string `json:"addr"`
}

type NodeDetails struct {
  PubKey string `json:"pub_key"`
  Alias string `json:"alias"`
  Addresses []NodeAddress `json:"addresses,omitempty"`
}

type PendingChannelInfo struct {
  ChannelPoint string `json:"channel_point"`
  RemotePubkey string `json:"remote_pubkey"`
  PeerAlias string `json:"peer_alias,omitempty"`
  CapacitySat int64 `json:"capacity_sat"`
  LocalBalanceSat int64 `json:"local_balance_sat"`
  RemoteBalanceSat int64 `json:"remote_balance_sat"`
  Status string `json:"status"`
  ClosingTxid string `json:"closing_txid,omitempty"`
  BlocksTilMaturity int32 `json:"blocks_til_maturity,omitempty"`
  LimboBalance int64 `json:"limbo_balance,omitempty"`
  ConfirmationsUntilActive uint32 `json:"confirmations_until_active,omitempty"`
  Private bool `json:"private"`
}

type RecentActivity struct {
  Type string `json:"type"`
  Network string `json:"network,omitempty"`
  Direction string `json:"direction,omitempty"`
  AmountSat int64 `json:"amount_sat"`
  Memo string `json:"memo"`
  Timestamp time.Time `json:"timestamp"`
  Status string `json:"status"`
  Txid string `json:"txid,omitempty"`
  Keysend bool `json:"keysend,omitempty"`
  PaymentHash string `json:"-"`
}

type OnchainTransaction struct {
  Txid string `json:"txid"`
  Direction string `json:"direction"`
  AmountSat int64 `json:"amount_sat"`
  FeeSat int64 `json:"fee_sat"`
  Confirmations int32 `json:"confirmations"`
  BlockHeight int32 `json:"block_height"`
  Timestamp time.Time `json:"timestamp"`
  Label string `json:"label,omitempty"`
  Addresses []string `json:"addresses,omitempty"`
}

type OnchainUtxo struct {
  Outpoint string `json:"outpoint"`
  Txid string `json:"txid"`
  Vout uint32 `json:"vout"`
  Address string `json:"address"`
  AddressType string `json:"address_type"`
  AmountSat int64 `json:"amount_sat"`
  Confirmations int64 `json:"confirmations"`
  PkScript string `json:"pk_script,omitempty"`
}
