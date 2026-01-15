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
  "strconv"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/config"
  "lightningos-light/lnrpc"

  "google.golang.org/grpc"
  "google.golang.org/grpc/credentials"
)

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
)

type macaroonCredential struct {
  macaroon string
}

type BalanceSummary struct {
  OnchainSat int64
  LightningSat int64
  Warnings []string
}

type ChannelPolicy struct {
  ChannelPoint string
  BaseFeeMsat int64
  FeeRatePpm int64
  TimeLockDelta int64
  InboundBaseMsat int64
  InboundFeeRatePpm int64
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
  opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}

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

func (c *Client) PayInvoice(ctx context.Context, paymentRequest string) error {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  _, err = client.SendPaymentSync(ctx, &lnrpc.SendRequest{PaymentRequest: paymentRequest})
  return err
}

func (c *Client) SendCoins(ctx context.Context, address string, amountSat int64, satPerVbyte int64) (string, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return "", err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  req := &lnrpc.SendCoinsRequest{
    Addr: address,
    Amount: amountSat,
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
  payments, payErr := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{IncludeIncomplete: true, MaxPayments: uint64(limit)})

  var items []RecentActivity
  if invErr == nil {
    for _, inv := range invoices.Invoices {
      hash := ""
      if len(inv.RHash) > 0 {
        hash = hex.EncodeToString(inv.RHash)
      }
      items = append(items, RecentActivity{
        Type: "invoice",
        Network: "lightning",
        Direction: "in",
        AmountSat: inv.Value,
        Memo: inv.Memo,
        Timestamp: time.Unix(inv.CreationDate, 0).UTC(),
        Status: inv.State.String(),
        PaymentHash: hash,
      })
    }
  }
  if payErr == nil {
    for _, pay := range payments.Payments {
      items = append(items, RecentActivity{
        Type: "payment",
        Network: "lightning",
        Direction: "out",
        AmountSat: pay.ValueSat,
        Memo: pay.PaymentRequest,
        Timestamp: time.Unix(pay.CreationDate, 0).UTC(),
        Status: pay.Status.String(),
        PaymentHash: strings.ToLower(pay.PaymentHash),
      })
    }
  }

  return items, nil
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
  resp, err := client.GetTransactions(ctx, &lnrpc.GetTransactionsRequest{
    MaxTransactions: uint32(limit),
  })
  if err != nil {
    return nil, err
  }

  items := make([]RecentActivity, 0, len(resp.Transactions))
  for _, tx := range resp.Transactions {
    if tx == nil {
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

    if !ch.Private {
      if edge, err := client.GetChanInfo(ctx, &lnrpc.ChanInfoRequest{ChanId: ch.ChanId}); err == nil {
        policy := edge.Node1Policy
        if ch.RemotePubkey != "" {
          if edge.Node1Pub == ch.RemotePubkey {
            policy = edge.Node2Policy
          } else if edge.Node2Pub == ch.RemotePubkey {
            policy = edge.Node1Policy
          }
        }
        if policy != nil {
          base := int64(policy.FeeBaseMsat)
          rate := int64(policy.FeeRateMilliMsat)
          inbound := int64(policy.InboundFeeRateMilliMsat)
          baseFeeMsat = &base
          feeRatePpm = &rate
          inboundFeeRatePpm = &inbound
        }
      }
    }

    channels = append(channels, ChannelInfo{
      ChannelPoint: ch.ChannelPoint,
      ChannelID: ch.ChanId,
      RemotePubkey: ch.RemotePubkey,
      PeerAlias: ch.PeerAlias,
      Active: ch.Active,
      Private: ch.Private,
      CapacitySat: ch.Capacity,
      LocalBalanceSat: ch.LocalBalance,
      RemoteBalanceSat: ch.RemoteBalance,
      BaseFeeMsat: baseFeeMsat,
      FeeRatePpm: feeRatePpm,
      InboundFeeRatePpm: inboundFeeRatePpm,
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
  conn, err := c.dial(ctx, true)
  if err != nil {
    return err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  _, err = client.ConnectPeer(ctx, &lnrpc.ConnectPeerRequest{
    Addr: &lnrpc.LightningAddress{
      Pubkey: pubkey,
      Host: host,
    },
    Perm: perm,
    Timeout: 8,
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

func (c *Client) CloseChannel(ctx context.Context, channelPoint string, force bool) error {
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
  stream, err := client.CloseChannel(ctx, &lnrpc.CloseChannelRequest{
    ChannelPoint: cp,
    Force: force,
    NoWait: true,
  })
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
  conn, err := c.dial(ctx, true)
  if err != nil {
    return err
  }
  defer conn.Close()

  req := &lnrpc.PolicyUpdateRequest{
    BaseFeeMsat: baseFeeMsat,
    FeeRatePpm: uint32(feeRatePpm),
    TimeLockDelta: uint32(timeLockDelta),
  }
  if inboundEnabled {
    if inboundBaseMsat < math.MinInt32 || inboundBaseMsat > math.MaxInt32 {
      return fmt.Errorf("inbound base fee out of range")
    }
    if inboundFeeRatePpm < math.MinInt32 || inboundFeeRatePpm > math.MaxInt32 {
      return fmt.Errorf("inbound fee rate out of range")
    }
    req.InboundFee = &lnrpc.InboundFee{
      BaseFeeMsat: int32(inboundBaseMsat),
      FeeRatePpm: int32(inboundFeeRatePpm),
    }
  }
  if applyAll {
    req.Scope = &lnrpc.PolicyUpdateRequest_Global{Global: true}
  } else {
    cp, err := parseChannelPoint(channelPoint)
    if err != nil {
      return err
    }
    req.Scope = &lnrpc.PolicyUpdateRequest_ChanPoint{ChanPoint: cp}
  }

  client := lnrpc.NewLightningClient(conn)
  _, err = client.UpdateChannelPolicy(ctx, req)
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
  Private bool `json:"private"`
  CapacitySat int64 `json:"capacity_sat"`
  LocalBalanceSat int64 `json:"local_balance_sat"`
  RemoteBalanceSat int64 `json:"remote_balance_sat"`
  BaseFeeMsat *int64 `json:"base_fee_msat,omitempty"`
  FeeRatePpm *int64 `json:"fee_rate_ppm,omitempty"`
  InboundFeeRatePpm *int64 `json:"inbound_fee_rate_ppm,omitempty"`
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
  PaymentHash string `json:"-"`
}
