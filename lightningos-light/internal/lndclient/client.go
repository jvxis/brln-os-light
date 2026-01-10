package lndclient

import (
  "context"
  "crypto/x509"
  "encoding/hex"
  "errors"
  "fmt"
  "io"
  "log"
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

func (c *Client) getStatusUncached(ctx context.Context) (Status, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return Status{WalletState: "unknown"}, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  info, err := client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
  if err != nil {
    if isWalletLocked(err) {
      return Status{WalletState: "locked"}, nil
    }
    return Status{WalletState: "unknown"}, err
  }

  status := Status{
    ServiceActive: true,
    WalletState: "unlocked",
    SyncedToChain: info.SyncedToChain,
    SyncedToGraph: info.SyncedToGraph,
    BlockHeight: int64(info.BlockHeight),
    Version: info.Version,
    Pubkey: info.IdentityPubkey,
  }
  if len(info.Uris) > 0 {
    status.URI = info.Uris[0]
  }

  channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
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
  }

  wallet, err := client.WalletBalance(ctx, &lnrpc.WalletBalanceRequest{})
  if err == nil {
    status.OnchainSat = wallet.TotalBalance
  }

  channelBal, err := client.ChannelBalance(ctx, &lnrpc.ChannelBalanceRequest{})
  if err == nil {
    status.LightningSat = channelBal.Balance
  }

  return status, nil
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

func (c *Client) CreateInvoice(ctx context.Context, amountSat int64, memo string) (string, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return "", err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  resp, err := client.AddInvoice(ctx, &lnrpc.Invoice{
    Memo: memo,
    Value: amountSat,
  })
  if err != nil {
    return "", err
  }

  return resp.PaymentRequest, nil
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
      items = append(items, RecentActivity{
        Type: "invoice",
        AmountSat: inv.Value,
        Memo: inv.Memo,
        Timestamp: time.Unix(inv.CreationDate, 0).UTC(),
        Status: inv.State.String(),
      })
    }
  }
  if payErr == nil {
    for _, pay := range payments.Payments {
      items = append(items, RecentActivity{
        Type: "payment",
        AmountSat: pay.ValueSat,
        Memo: pay.PaymentRequest,
        Timestamp: time.Unix(pay.CreationDate, 0).UTC(),
        Status: pay.Status.String(),
      })
    }
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
    })
  }

  return channels, nil
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
    if len(peer.Errors) > 0 {
      if last := peer.Errors[len(peer.Errors)-1]; last != nil {
        lastErr = last.Error
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

func (c *Client) OpenChannel(ctx context.Context, pubkeyHex string, localFundingSat int64, pushSat int64, private bool) (string, error) {
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
  resp, err := client.OpenChannelSync(ctx, &lnrpc.OpenChannelRequest{
    NodePubkey: pubkey,
    LocalFundingAmount: localFundingSat,
    PushSat: pushSat,
    Private: private,
  })
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

func (c *Client) UpdateChannelFees(ctx context.Context, channelPoint string, applyAll bool, baseFeeMsat int64, feeRatePpm int64, timeLockDelta int64) error {
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
}

type RecentActivity struct {
  Type string `json:"type"`
  AmountSat int64 `json:"amount_sat"`
  Memo string `json:"memo"`
  Timestamp time.Time `json:"timestamp"`
  Status string `json:"status"`
}
