package server

import (
  "context"
  "crypto/rand"
  "encoding/hex"
  "encoding/json"
  "errors"
  "fmt"
  "log"
  "net/http"
  "net/url"
  "os"
  "sort"
  "strconv"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"
  "lightningos-light/lnrpc"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgtype"
  "github.com/jackc/pgx/v5/pgxpool"
)

const (
  notificationRetentionDays = 365
  notificationCleanupInterval = 6 * time.Hour
  paymentsPollInterval = 15 * time.Second
  forwardsPollInterval = 30 * time.Second
  pendingChannelsPollInterval = 30 * time.Second
  paymentsPendingMaxAge = 48 * time.Hour
)

const (
  notificationsSecretsPath = "/etc/lightningos/secrets.env"
  notificationsSecretsDir = "/etc/lightningos"
  notificationsDBName = "lightningos"
  notificationsDBUser = "losapp"
  paymentsPendingCursorKey = "payments_inflight"
  paymentsPendingMax = 200
)

type Notification struct {
  ID int64 `json:"id"`
  OccurredAt time.Time `json:"occurred_at"`
  Type string `json:"type"`
  Action string `json:"action"`
  Direction string `json:"direction"`
  Status string `json:"status"`
  AmountSat int64 `json:"amount_sat"`
  FeeSat int64 `json:"fee_sat"`
  FeeMsat int64 `json:"fee_msat"`
  PeerPubkey string `json:"peer_pubkey,omitempty"`
  PeerAlias string `json:"peer_alias,omitempty"`
  ChannelID int64 `json:"channel_id,omitempty"`
  ChannelPoint string `json:"channel_point,omitempty"`
  Txid string `json:"txid,omitempty"`
  PaymentHash string `json:"payment_hash,omitempty"`
  Memo string `json:"memo,omitempty"`
}

type rebalanceRouteInfo struct {
  PeerLabel string
  ChannelLabel string
}

type notificationRowScanner interface {
  Scan(dest ...any) error
}

type pendingPaymentEntry struct {
  Hash string `json:"hash"`
  LastSeen int64 `json:"last_seen"`
}

type Notifier struct {
  db *pgxpool.Pool
  lnd *lndclient.Client
  logger *log.Logger

  mu sync.Mutex
  backupMu sync.Mutex
  pendingMu sync.Mutex
  subscribers map[chan Notification]struct{}
  started bool
  stop chan struct{}
  lastCleanup time.Time
  backupSent map[string]time.Time
  pendingSent map[string]time.Time
}

func NewNotifier(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *Notifier {
  return &Notifier{
    db: db,
    lnd: lnd,
    logger: logger,
    subscribers: map[chan Notification]struct{}{},
    backupSent: map[string]time.Time{},
    pendingSent: map[string]time.Time{},
  }
}

func (n *Notifier) Start() {
  n.mu.Lock()
  if n.started {
    n.mu.Unlock()
    return
  }
  n.started = true
  n.stop = make(chan struct{})
  n.mu.Unlock()

  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  if err := n.ensureSchema(ctx); err != nil {
    n.logger.Printf("notifications disabled: failed to init schema: %v", err)
    cancel()
    return
  }
  cancel()

  go n.runInvoices()
  go n.runPayments()
  go n.runTransactions()
  go n.runChannels()
  go n.runPendingChannels()
  go n.runForwards()
}

func bootstrapNotificationsDSN(logger *log.Logger) (string, error) {
  if existing, err := readEnvFileValue(notificationsSecretsPath, "NOTIFICATIONS_PG_DSN"); err == nil && strings.TrimSpace(existing) != "" && !isPlaceholderDSN(existing) {
    _ = os.Setenv("NOTIFICATIONS_PG_DSN", existing)
    return existing, nil
  }

  adminDSN, err := ensureNotificationsAdminDSN(logger)
  if err != nil {
    return "", err
  }
  if strings.TrimSpace(adminDSN) == "" {
    return "", errors.New("NOTIFICATIONS_PG_ADMIN_DSN not set")
  }

  password, err := randomPassword(32)
  if err != nil {
    return "", err
  }

  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()

  pool, err := pgxpool.New(ctx, adminDSN)
  if err != nil {
    return "", err
  }
  defer pool.Close()

  adminUser := adminUserFromDSN(adminDSN)

  roleExists := false
  var roleCheck int
  err = pool.QueryRow(ctx, "select 1 from pg_roles where rolname=$1", notificationsDBUser).Scan(&roleCheck)
  if err == nil && roleCheck == 1 {
    roleExists = true
  }

  if !roleExists {
    if _, err := pool.Exec(ctx, fmt.Sprintf("create role %s with login password '%s'", notificationsDBUser, password)); err != nil {
      return "", err
    }
  } else {
    if _, err := pool.Exec(ctx, fmt.Sprintf("alter role %s with password '%s'", notificationsDBUser, password)); err != nil {
      return "", err
    }
  }

  if adminUser != "" && adminUser != notificationsDBUser {
    if _, err := pool.Exec(ctx, fmt.Sprintf("grant %s to %s", notificationsDBUser, adminUser)); err != nil {
      logger.Printf("notifications warning: failed to grant %s to %s: %v", notificationsDBUser, adminUser, err)
    }
  }

  dbExists := false
  var dbCheck int
  err = pool.QueryRow(ctx, "select 1 from pg_database where datname=$1", notificationsDBName).Scan(&dbCheck)
  if err == nil && dbCheck == 1 {
    dbExists = true
  }

  if !dbExists {
    if _, err := pool.Exec(ctx, fmt.Sprintf("create database %s owner %s", notificationsDBName, notificationsDBUser)); err != nil {
      return "", err
    }
  } else {
    _, _ = pool.Exec(ctx, fmt.Sprintf("alter database %s owner to %s", notificationsDBName, notificationsDBUser))
  }

  dsn := fmt.Sprintf("postgres://%s:%s@127.0.0.1:5432/%s?sslmode=disable", notificationsDBUser, password, notificationsDBName)
  if err := ensureSecretsDir(); err != nil {
    logger.Printf("notifications warning: failed to prepare secrets dir: %v", err)
  }
  if err := writeEnvFileValue(notificationsSecretsPath, "NOTIFICATIONS_PG_DSN", dsn); err != nil {
    logger.Printf("notifications warning: failed to persist NOTIFICATIONS_PG_DSN: %v", err)
  }

  _ = os.Setenv("NOTIFICATIONS_PG_DSN", dsn)
  logger.Printf("notifications: provisioned database %s with user %s", notificationsDBName, notificationsDBUser)
  return dsn, nil
}

func (n *Notifier) Subscribe() chan Notification {
  ch := make(chan Notification, 50)
  n.mu.Lock()
  n.subscribers[ch] = struct{}{}
  n.mu.Unlock()
  return ch
}

func (n *Notifier) Unsubscribe(ch chan Notification) {
  n.mu.Lock()
  if _, ok := n.subscribers[ch]; ok {
    delete(n.subscribers, ch)
    close(ch)
  }
  n.mu.Unlock()
}

func readEnvFileValue(path, key string) (string, error) {
  data, err := os.ReadFile(path)
  if err != nil {
    return "", err
  }
  lines := strings.Split(string(data), "\n")
  prefix := key + "="
  for _, line := range lines {
    trimmed := strings.TrimSpace(line)
    if strings.HasPrefix(trimmed, prefix) {
      return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), nil
    }
  }
  return "", nil
}

func writeEnvFileValue(path, key, value string) error {
  data, err := os.ReadFile(path)
  if err != nil && !errors.Is(err, os.ErrNotExist) {
    return err
  }
  lines := []string{}
  if len(data) > 0 {
    lines = strings.Split(string(data), "\n")
  }
  prefix := key + "="
  updated := false
  for i, line := range lines {
    if strings.HasPrefix(strings.TrimSpace(line), prefix) {
      lines[i] = fmt.Sprintf("%s=%s", key, value)
      updated = true
    }
  }
  if !updated {
    lines = append(lines, fmt.Sprintf("%s=%s", key, value))
  }
  output := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
  return os.WriteFile(path, []byte(output), 0o660)
}

func randomPassword(length int) (string, error) {
  if length < 16 {
    length = 16
  }
  const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
  b := make([]byte, length)
  if _, err := rand.Read(b); err != nil {
    return "", err
  }
  for i := range b {
    b[i] = chars[int(b[i])%len(chars)]
  }
  return string(b), nil
}

func ensureSecretsDir() error {
  return os.MkdirAll(notificationsSecretsDir, 0o750)
}

func ensureNotificationsAdminDSN(logger *log.Logger) (string, error) {
  adminDSN := os.Getenv("NOTIFICATIONS_PG_ADMIN_DSN")
  if strings.TrimSpace(adminDSN) != "" && !isPlaceholderDSN(adminDSN) && dsnHasPassword(adminDSN) {
    return adminDSN, nil
  }

  if existing, err := readEnvFileValue(notificationsSecretsPath, "NOTIFICATIONS_PG_ADMIN_DSN"); err == nil && strings.TrimSpace(existing) != "" && !isPlaceholderDSN(existing) && dsnHasPassword(existing) {
    _ = os.Setenv("NOTIFICATIONS_PG_ADMIN_DSN", existing)
    return existing, nil
  }

  if derived, err := deriveAdminDSNFromLND(); err == nil && strings.TrimSpace(derived) != "" && !isPlaceholderDSN(derived) {
    if err := ensureSecretsDir(); err != nil {
      logger.Printf("notifications warning: failed to prepare secrets dir: %v", err)
    }
    if err := writeEnvFileValue(notificationsSecretsPath, "NOTIFICATIONS_PG_ADMIN_DSN", derived); err != nil {
      logger.Printf("notifications warning: failed to persist NOTIFICATIONS_PG_ADMIN_DSN: %v", err)
    }
    _ = os.Setenv("NOTIFICATIONS_PG_ADMIN_DSN", derived)
    return derived, nil
  }

  return "", errors.New("NOTIFICATIONS_PG_ADMIN_DSN not set")
}

func deriveAdminDSNFromLND() (string, error) {
  raw := strings.TrimSpace(os.Getenv("LND_PG_DSN"))
  if raw == "" || isPlaceholderDSN(raw) {
    return "", errors.New("LND_PG_DSN not set")
  }
  parsed, err := url.Parse(raw)
  if err != nil {
    return "", err
  }
  parsed.Path = "/postgres"
  return parsed.String(), nil
}

func dsnHasPassword(raw string) bool {
  parsed, err := url.Parse(raw)
  if err != nil {
    return false
  }
  if parsed.User == nil {
    return false
  }
  _, ok := parsed.User.Password()
  return ok
}

func adminUserFromDSN(raw string) string {
  parsed, err := url.Parse(raw)
  if err != nil {
    return ""
  }
  if parsed.User == nil {
    return ""
  }
  return parsed.User.Username()
}

func isPlaceholderDSN(dsn string) bool {
  return strings.Contains(dsn, "CHANGE_ME")
}

func (n *Notifier) broadcast(evt Notification) {
  n.mu.Lock()
  defer n.mu.Unlock()
  for ch := range n.subscribers {
    select {
    case ch <- evt:
    default:
    }
  }
}

func (n *Notifier) ensureSchema(ctx context.Context) error {
  if n.db == nil {
    return errors.New("db not configured")
  }

  _, err := n.db.Exec(ctx, `
create table if not exists notifications (
  id bigserial primary key,
  event_key text unique not null,
  occurred_at timestamptz not null,
  type text not null,
  action text not null,
  direction text not null,
  status text not null,
  amount_sat bigint not null default 0,
  fee_sat bigint not null default 0,
  fee_msat bigint not null default 0,
  peer_pubkey text,
  peer_alias text,
  channel_id bigint,
  channel_point text,
  txid text,
  payment_hash text,
  memo text,
  created_at timestamptz not null default now()
);

alter table notifications add column if not exists fee_msat bigint not null default 0;

create index if not exists notifications_occurred_at_idx on notifications (occurred_at desc);
create index if not exists notifications_type_idx on notifications (type);
create index if not exists notifications_payment_hash_idx on notifications (payment_hash);

create table if not exists notification_cursors (
  key text primary key,
  value text not null,
  updated_at timestamptz not null default now()
);
`)
  return err
}

func (n *Notifier) upsertNotification(ctx context.Context, eventKey string, evt Notification) (Notification, error) {
  if eventKey == "" {
    return Notification{}, errors.New("event key required")
  }

  row := n.db.QueryRow(ctx, `
insert into notifications (
  event_key, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat,
  peer_pubkey, peer_alias, channel_id, channel_point, txid, payment_hash, memo
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
on conflict (event_key) do update set
  occurred_at = excluded.occurred_at,
  type = excluded.type,
  action = excluded.action,
  direction = excluded.direction,
  status = excluded.status,
  amount_sat = excluded.amount_sat,
  fee_sat = excluded.fee_sat,
  fee_msat = excluded.fee_msat,
  peer_pubkey = excluded.peer_pubkey,
  peer_alias = excluded.peer_alias,
  channel_id = excluded.channel_id,
  channel_point = excluded.channel_point,
  txid = excluded.txid,
  payment_hash = excluded.payment_hash,
  memo = excluded.memo
returning id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, txid, payment_hash, memo
`, eventKey, evt.OccurredAt, evt.Type, evt.Action, evt.Direction, evt.Status,
    evt.AmountSat, evt.FeeSat, evt.FeeMsat, nullableString(evt.PeerPubkey), nullableString(evt.PeerAlias),
    nullableInt(evt.ChannelID), nullableString(evt.ChannelPoint), nullableString(evt.Txid),
    nullableString(evt.PaymentHash), nullableString(evt.Memo),
  )

  var stored Notification
  stored, err := scanNotification(row)
  if err != nil {
    return Notification{}, err
  }

  n.cleanupIfNeeded()
  n.broadcast(stored)
  return stored, nil
}

func (n *Notifier) list(ctx context.Context, limit int) ([]Notification, error) {
  if n.db == nil {
    return nil, errors.New("notifications disabled")
  }
  if limit <= 0 {
    limit = 200
  }
  if limit > 1000 {
    limit = 1000
  }

  rows, err := n.db.Query(ctx, `
select id, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat,
  peer_pubkey, peer_alias, channel_id, channel_point, txid, payment_hash, memo
from notifications
order by occurred_at desc, id desc
limit $1`, limit)
  if err != nil {
    return nil, err
  }
  defer rows.Close()

  var items []Notification
  for rows.Next() {
    evt, err := scanNotification(rows)
    if err != nil {
      return nil, err
    }
    items = append(items, evt)
  }
  return items, rows.Err()
}

func (n *Notifier) getCursor(ctx context.Context, key string) (string, error) {
  var val string
  err := n.db.QueryRow(ctx, "select value from notification_cursors where key=$1", key).Scan(&val)
  if err == pgx.ErrNoRows {
    return "", nil
  }
  return val, err
}

func (n *Notifier) setCursor(ctx context.Context, key, val string) error {
  _, err := n.db.Exec(ctx, `
insert into notification_cursors (key, value, updated_at)
values ($1, $2, now())
on conflict (key) do update set value=excluded.value, updated_at=excluded.updated_at
`, key, val)
  return err
}

func (n *Notifier) loadPendingPayments(ctx context.Context) map[string]int64 {
  pending := map[string]int64{}
  if n == nil || n.db == nil {
    return pending
  }
  raw, err := n.getCursor(ctx, paymentsPendingCursorKey)
  if err != nil || strings.TrimSpace(raw) == "" {
    return pending
  }

  var entries []pendingPaymentEntry
  if err := json.Unmarshal([]byte(raw), &entries); err == nil {
    now := time.Now().Unix()
    for _, entry := range entries {
      hash := normalizeHash(entry.Hash)
      if hash == "" {
        continue
      }
      lastSeen := entry.LastSeen
      if lastSeen <= 0 {
        lastSeen = now
      }
      pending[hash] = lastSeen
    }
    return pending
  }

  var legacy map[string]int64
  if err := json.Unmarshal([]byte(raw), &legacy); err == nil {
    now := time.Now().Unix()
    for hash, lastSeen := range legacy {
      normalized := normalizeHash(hash)
      if normalized == "" {
        continue
      }
      if lastSeen <= 0 {
        lastSeen = now
      }
      pending[normalized] = lastSeen
    }
  }
  return pending
}

func (n *Notifier) storePendingPayments(ctx context.Context, pending map[string]int64) {
  if n == nil || n.db == nil {
    return
  }
  cutoff := time.Now().Add(-paymentsPendingMaxAge).Unix()
  entries := make([]pendingPaymentEntry, 0, len(pending))
  now := time.Now().Unix()
  for hash, lastSeen := range pending {
    normalized := normalizeHash(hash)
    if normalized == "" {
      continue
    }
    if lastSeen <= 0 {
      lastSeen = now
    }
    if lastSeen < cutoff {
      continue
    }
    entries = append(entries, pendingPaymentEntry{Hash: normalized, LastSeen: lastSeen})
  }
  sort.Slice(entries, func(i, j int) bool {
    return entries[i].LastSeen > entries[j].LastSeen
  })
  if len(entries) > paymentsPendingMax {
    entries = entries[:paymentsPendingMax]
  }
  payload, err := json.Marshal(entries)
  if err != nil {
    return
  }
  _ = n.setCursor(ctx, paymentsPendingCursorKey, string(payload))
}

func (n *Notifier) reconcileRebalance(ctx context.Context, paymentHash string) {
  normalized := normalizeHash(paymentHash)
  if normalized == "" {
    return
  }

  tx, err := n.db.Begin(ctx)
  if err != nil {
    return
  }
  defer tx.Rollback(ctx)

  var payID int64
  var payFee int64
  var payFeeMsat int64
  err = tx.QueryRow(ctx, `
select id, fee_sat, fee_msat from notifications
where payment_hash=$1 and type='lightning' and action='sent' and status='SUCCEEDED'
order by occurred_at desc limit 1`, normalized).Scan(&payID, &payFee, &payFeeMsat)
  if err != nil {
    return
  }
  if payFeeMsat == 0 && payFee != 0 {
    payFeeMsat = payFee * 1000
  }
  if payFeeMsat == 0 {
    feeCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
    if pay, err := n.lookupPaymentByHash(feeCtx, normalized); err == nil && pay != nil {
      if feeMsat := paymentFeeMsat(pay); feeMsat != 0 {
        payFeeMsat = feeMsat
        payFee = feeMsat / 1000
      }
    }
    cancel()
  }

  var invID int64
  var invAmount int64
  var invMemo pgtype.Text
  var invAt time.Time
  err = tx.QueryRow(ctx, `
select id, amount_sat, memo, occurred_at from notifications
where payment_hash=$1 and type='lightning' and action='received' and status='SETTLED'
order by occurred_at desc limit 1`, normalized).Scan(&invID, &invAmount, &invMemo, &invAt)
  if err != nil {
    return
  }

  memoValue := nullableString(invMemo.String)
  if !invMemo.Valid {
    memoValue = nil
  }

  row := tx.QueryRow(ctx, `
update notifications
set type='rebalance',
  action='rebalanced',
  direction='neutral',
  status='SETTLED',
  amount_sat=$2,
  fee_sat=$3,
  fee_msat=$4,
  memo=$5,
  occurred_at=$6
where id=$1
returning id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, txid, payment_hash, memo
`, payID, invAmount, payFee, payFeeMsat, memoValue, invAt)
  updated, err := scanNotification(row)
  if err != nil {
    return
  }

  _, err = tx.Exec(ctx, `delete from notifications where id=$1`, invID)
  if err != nil {
    return
  }

  if err := tx.Commit(ctx); err != nil {
    return
  }

  n.broadcast(updated)
}

func (n *Notifier) cleanupIfNeeded() {
  n.mu.Lock()
  next := n.lastCleanup.Add(notificationCleanupInterval)
  if time.Now().Before(next) {
    n.mu.Unlock()
    return
  }
  n.lastCleanup = time.Now()
  n.mu.Unlock()

  ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
  defer cancel()
  cutoff := time.Now().AddDate(0, 0, -notificationRetentionDays)
  _, _ = n.db.Exec(ctx, "delete from notifications where occurred_at < $1", cutoff)
}

func (n *Notifier) runInvoices() {
  for {
    select {
    case <-n.stop:
      return
    default:
    }

    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    cursorVal, _ := n.getCursor(ctx, "invoice_settle_index")
    cancel()

    var settleIndex uint64
    if cursorVal != "" {
      if parsed, err := strconv.ParseUint(cursorVal, 10, 64); err == nil {
        settleIndex = parsed
      }
    }

    conn, err := n.lnd.DialLightning(context.Background())
    if err != nil {
      n.logger.Printf("notifications: invoice stream dial failed: %v", err)
      time.Sleep(5 * time.Second)
      continue
    }

    client := lnrpc.NewLightningClient(conn)
    stream, err := client.SubscribeInvoices(context.Background(), &lnrpc.InvoiceSubscription{
      SettleIndex: settleIndex,
    })
    if err != nil {
      n.logger.Printf("notifications: invoice stream subscribe failed: %v", err)
      conn.Close()
      time.Sleep(5 * time.Second)
      continue
    }

    for {
      invoice, err := stream.Recv()
      if err != nil {
        n.logger.Printf("notifications: invoice stream ended: %v", err)
        _ = conn.Close()
        break
      }

      if invoice.State != lnrpc.Invoice_SETTLED {
        continue
      }
      if invoice.SettleIndex <= settleIndex {
        continue
      }

      settleIndex = invoice.SettleIndex
      hash := normalizeHash(hex.EncodeToString(invoice.RHash))
      if hash == "" {
        continue
      }
      amount := invoice.AmtPaidSat
      if amount == 0 {
        amount = invoice.Value
      }
      occurredAt := time.Unix(invoice.SettleDate, 0).UTC()
      isKeysend := invoice.IsKeysend
      evtType := "lightning"
      peerPubkey := ""
      peerAlias := ""
      memo := strings.TrimSpace(invoice.Memo)
      ctxPeer, cancelPeer := context.WithTimeout(context.Background(), 4*time.Second)
      peerPubkey, peerAlias = n.keysendPeerFromInvoice(ctxPeer, invoice)
      cancelPeer()
      if peerAlias == "" && peerPubkey != "" {
        peerAlias = n.lookupNodeAlias(peerPubkey)
      }
      if isKeysend {
        evtType = "keysend"
        memo = keysendMessageFromInvoice(invoice)
      }
      evt := Notification{
        OccurredAt: occurredAt,
        Type: evtType,
        Action: "received",
        Direction: "in",
        Status: "SETTLED",
        AmountSat: amount,
        PeerPubkey: peerPubkey,
        PeerAlias: peerAlias,
        PaymentHash: hash,
        Memo: memo,
      }

      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      if n.isRebalanceHash(ctx, hash) {
        _ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
        cancel()
        continue
      }

      if pay, err := n.lookupPaymentByHash(ctx, hash); err == nil && pay != nil {
        if n.isSelfPayment(ctx, pay.PaymentRequest, pay) {
          rebalanceEvt := n.rebalanceEvent(ctx, pay, occurredAt)
          if _, err := n.upsertNotification(ctx, fmt.Sprintf("payment:%s", hash), rebalanceEvt); err == nil {
            _ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
          }
          cancel()
          continue
        }
      }

      if _, err := n.upsertNotification(ctx, fmt.Sprintf("invoice:%s", hash), evt); err == nil {
        _ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
        n.reconcileRebalance(ctx, hash)
      }
      cancel()
    }

    time.Sleep(2 * time.Second)
  }
}

func (n *Notifier) runPayments() {
  pending := map[string]int64{}
  if n != nil {
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    pending = n.loadPendingPayments(ctx)
    cancel()
  }

  processPayment := func(pay *lnrpc.Payment) {
    if pay == nil {
      return
    }
    paymentHash := normalizeHash(pay.PaymentHash)
    if paymentHash == "" {
      return
    }
    status := pay.Status.String()
    if status == "IN_FLIGHT" {
      return
    }
    if status != "SUCCEEDED" && isProbePayment(pay) {
      return
    }

    amount := pay.ValueSat
    feeMsat := paymentFeeMsat(pay)
    fee := feeMsat / 1000
    occurredAt := time.Unix(0, pay.CreationTimeNs).UTC()
    if pay.CreationTimeNs == 0 {
      occurredAt = time.Now().UTC()
    }
    isKeysend := isKeysendPayment(pay)
    keysendDestPubkey := ""
    if isKeysend {
      keysendDestPubkey = keysendDestinationFromPayment(pay)
    }
    isRebalance := false
    if !isKeysend {
      ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
      isRebalance = n.isSelfPayment(ctx, pay.PaymentRequest, pay)
      if !isRebalance && n.hasInvoiceHash(ctx, paymentHash) {
        isRebalance = true
      }
      cancel()
      if status != "SUCCEEDED" && isRebalance {
        return
      }
    }
    peerPubkey := ""
    peerAlias := ""
    memo := ""
    if isKeysend {
      peerPubkey = keysendDestPubkey
      memo = keysendMessageFromPayment(pay)
    } else {
      trimmed := strings.TrimSpace(pay.PaymentRequest)
      if trimmed != "" {
        ctxDecode, cancelDecode := context.WithTimeout(context.Background(), 4*time.Second)
        if decoded, err := n.lnd.DecodeInvoice(ctxDecode, trimmed); err == nil {
          peerPubkey = strings.TrimSpace(decoded.Destination)
          memo = strings.TrimSpace(decoded.Memo)
        }
        cancelDecode()
      }
      if peerPubkey == "" {
        if route := rebalanceRouteFromPayment(pay); route != nil {
          hops := route.GetHops()
          if len(hops) > 0 {
            peerPubkey = strings.TrimSpace(hops[len(hops)-1].PubKey)
          }
        }
      }
    }
    if peerAlias == "" && peerPubkey != "" {
      peerAlias = n.lookupNodeAlias(peerPubkey)
    }

    evtType := "lightning"
    if isKeysend {
      evtType = "keysend"
    }
    evt := Notification{
      OccurredAt: occurredAt,
      Type: evtType,
      Action: "sent",
      Direction: "out",
      Status: status,
      AmountSat: amount,
      FeeSat: fee,
      FeeMsat: feeMsat,
      PeerPubkey: peerPubkey,
      PeerAlias: peerAlias,
      PaymentHash: paymentHash,
      Memo: memo,
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    if isRebalance {
      evt = n.rebalanceEvent(ctx, pay, occurredAt)
    }
    if _, err := n.upsertNotification(ctx, fmt.Sprintf("payment:%s", paymentHash), evt); err == nil {
      if isRebalance {
        _ = n.removeRebalanceInvoice(ctx, paymentHash)
      } else {
        n.reconcileRebalance(ctx, paymentHash)
      }
    }
    cancel()
  }

  for {
    select {
    case <-n.stop:
      return
    case <-time.After(paymentsPollInterval):
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    cursorVal, _ := n.getCursor(ctx, "payments_index")
    cancel()

    var indexOffset uint64
    if cursorVal != "" {
      if parsed, err := strconv.ParseUint(cursorVal, 10, 64); err == nil {
        indexOffset = parsed
      }
    }

    conn, err := n.lnd.DialLightning(context.Background())
    if err != nil {
      n.logger.Printf("notifications: payments poll dial failed: %v", err)
      continue
    }

    client := lnrpc.NewLightningClient(conn)
    res, err := client.ListPayments(context.Background(), &lnrpc.ListPaymentsRequest{
      IncludeIncomplete: true,
      IndexOffset: indexOffset,
      MaxPayments: 200,
      Reversed: false,
    })
    _ = conn.Close()
    if err != nil {
      n.logger.Printf("notifications: payments poll failed: %v", err)
      continue
    }

    maxIndex := indexOffset
    pendingDirty := false
    now := time.Now().Unix()
    for _, pay := range res.Payments {
      if pay.PaymentIndex <= indexOffset {
        continue
      }
      if pay.PaymentIndex > maxIndex {
        maxIndex = pay.PaymentIndex
      }
      paymentHash := normalizeHash(pay.PaymentHash)
      if paymentHash == "" {
        continue
      }
      status := pay.Status.String()
      if status == "IN_FLIGHT" {
        if pending[paymentHash] != now {
          pending[paymentHash] = now
          pendingDirty = true
        }
        continue
      }
      if status != "SUCCEEDED" && isProbePayment(pay) {
        if _, ok := pending[paymentHash]; ok {
          delete(pending, paymentHash)
          pendingDirty = true
        }
        continue
      }
      processPayment(pay)
      if _, ok := pending[paymentHash]; ok {
        delete(pending, paymentHash)
        pendingDirty = true
      }
    }

    if len(pending) > 0 {
      cutoff := time.Now().Add(-paymentsPendingMaxAge).Unix()
      for hash, lastSeen := range pending {
        if lastSeen < cutoff {
          delete(pending, hash)
          pendingDirty = true
          continue
        }
        ctxLookup, cancelLookup := context.WithTimeout(context.Background(), 6*time.Second)
        pay, err := n.lookupPaymentByHash(ctxLookup, hash)
        cancelLookup()
        if err != nil || pay == nil {
          continue
        }
        status := pay.Status.String()
        if status == "IN_FLIGHT" {
          if pending[hash] != now {
            pending[hash] = now
            pendingDirty = true
          }
          continue
        }
        if status != "SUCCEEDED" && isProbePayment(pay) {
          delete(pending, hash)
          pendingDirty = true
          continue
        }
        processPayment(pay)
        delete(pending, hash)
        pendingDirty = true
      }
    }

    if maxIndex > indexOffset {
      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      _ = n.setCursor(ctx, "payments_index", strconv.FormatUint(maxIndex, 10))
      cancel()
    }
    if pendingDirty {
      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      n.storePendingPayments(ctx, pending)
      cancel()
    }
  }
}

func (n *Notifier) runTransactions() {
  for {
    select {
    case <-n.stop:
      return
    default:
    }

    conn, err := n.lnd.DialLightning(context.Background())
    if err != nil {
      n.logger.Printf("notifications: transaction stream dial failed: %v", err)
      time.Sleep(5 * time.Second)
      continue
    }

    client := lnrpc.NewLightningClient(conn)
    stream, err := client.SubscribeTransactions(context.Background(), &lnrpc.GetTransactionsRequest{})
    if err != nil {
      n.logger.Printf("notifications: transaction stream subscribe failed: %v", err)
      conn.Close()
      time.Sleep(5 * time.Second)
      continue
    }

    for {
      tx, err := stream.Recv()
      if err != nil {
        n.logger.Printf("notifications: transaction stream ended: %v", err)
        _ = conn.Close()
        break
      }

      amount := tx.Amount
      direction := "in"
      action := "receive"
      if amount < 0 {
        direction = "out"
        action = "send"
        amount = amount * -1
      }
      status := "PENDING"
      if tx.NumConfirmations > 0 {
        status = "CONFIRMED"
      }
      occurredAt := time.Unix(tx.TimeStamp, 0).UTC()
      evt := Notification{
        OccurredAt: occurredAt,
        Type: "onchain",
        Action: action,
        Direction: direction,
        Status: status,
        AmountSat: amount,
        FeeSat: tx.TotalFees,
        Txid: tx.TxHash,
      }

      if direction == "in" && status == "CONFIRMED" {
        txid := strings.TrimSpace(tx.TxHash)
        if txid != "" {
          n.triggerTelegramBackup("onchain_receive_confirmed", txid)
        }
      }

      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      _, _ = n.upsertNotification(ctx, fmt.Sprintf("onchain:%s", tx.TxHash), evt)
      cancel()
    }

    time.Sleep(2 * time.Second)
  }
}

func (n *Notifier) runChannels() {
  for {
    select {
    case <-n.stop:
      return
    default:
    }

    conn, err := n.lnd.DialLightning(context.Background())
    if err != nil {
      n.logger.Printf("notifications: channel stream dial failed: %v", err)
      time.Sleep(5 * time.Second)
      continue
    }

    client := lnrpc.NewLightningClient(conn)
    stream, err := client.SubscribeChannelEvents(context.Background(), &lnrpc.ChannelEventSubscription{})
    if err != nil {
      n.logger.Printf("notifications: channel stream subscribe failed: %v", err)
      conn.Close()
      time.Sleep(5 * time.Second)
      continue
    }

    for {
      update, err := stream.Recv()
      if err != nil {
        n.logger.Printf("notifications: channel stream ended: %v", err)
        _ = conn.Close()
        break
      }

      evt, eventKey := n.channelEventToNotification(update)
      if eventKey == "" {
        continue
      }

      n.maybeSendTelegramBackup(update)

      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      _, _ = n.upsertNotification(ctx, eventKey, evt)
      cancel()
    }

    time.Sleep(2 * time.Second)
  }
}

func (n *Notifier) runPendingChannels() {
  for {
    select {
    case <-n.stop:
      return
    case <-time.After(pendingChannelsPollInterval):
    }

    ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
    pending, err := n.lnd.ListPendingChannels(ctx)
    cancel()
    if err != nil {
      n.logger.Printf("notifications: pending channels poll failed: %v", err)
      continue
    }

    for _, item := range pending {
      reason := ""
      isClosing := false
      switch item.Status {
      case "opening":
        reason = "opening"
      case "closing", "force_closing", "waiting_close":
        reason = "closing"
        isClosing = true
      default:
        continue
      }

      channelPoint := strings.TrimSpace(item.ChannelPoint)
      if channelPoint == "" && strings.TrimSpace(item.ClosingTxid) != "" {
        channelPoint = strings.TrimSpace(item.ClosingTxid)
      }
      n.triggerTelegramBackup(reason, channelPoint)
      if isClosing {
        n.notifyPendingChannelClosing(item, channelPoint)
      }
    }
  }
}

func (n *Notifier) notifyPendingChannelClosing(item lndclient.PendingChannelInfo, key string) {
  eventKey := strings.TrimSpace(key)
  if eventKey == "" {
    return
  }
  eventKey = "channel:closing:" + eventKey
  if !n.markPendingNotification(eventKey) {
    return
  }

  amount := item.LocalBalanceSat
  if amount <= 0 && item.LimboBalance > 0 {
    amount = item.LimboBalance
  }
  status := "PENDING"
  switch item.Status {
  case "force_closing":
    status = "FORCE_CLOSING"
  case "waiting_close":
    status = "WAITING_CLOSE"
  }

  evt := Notification{
    OccurredAt: time.Now().UTC(),
    Type: "channel",
    Action: "closing",
    Direction: "neutral",
    Status: status,
    AmountSat: amount,
    PeerPubkey: item.RemotePubkey,
    PeerAlias: item.PeerAlias,
    ChannelPoint: item.ChannelPoint,
    Txid: item.ClosingTxid,
  }
  if evt.PeerAlias == "" && evt.PeerPubkey != "" {
    evt.PeerAlias = n.lookupNodeAlias(evt.PeerPubkey)
  }

  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  _, _ = n.upsertNotification(ctx, eventKey, evt)
  cancel()
}

func (n *Notifier) markPendingNotification(key string) bool {
  if n == nil {
    return false
  }
  trimmed := strings.TrimSpace(key)
  if trimmed == "" {
    return false
  }
  n.pendingMu.Lock()
  defer n.pendingMu.Unlock()
  if _, ok := n.pendingSent[trimmed]; ok {
    return false
  }
  n.pendingSent[trimmed] = time.Now().UTC()
  return true
}

func (n *Notifier) channelEventToNotification(update *lnrpc.ChannelEventUpdate) (Notification, string) {
  if update == nil {
    return Notification{}, ""
  }

  now := time.Now().UTC()
  switch update.Type {
  case lnrpc.ChannelEventUpdate_OPEN_CHANNEL:
    ch := update.GetOpenChannel()
    if ch == nil {
      return Notification{}, ""
    }
    evt := Notification{
      OccurredAt: now,
      Type: "channel",
      Action: "open",
      Direction: "neutral",
      Status: "OPENED",
      AmountSat: ch.Capacity,
      PeerPubkey: ch.RemotePubkey,
      PeerAlias: ch.PeerAlias,
      ChannelID: int64(ch.ChanId),
      ChannelPoint: ch.ChannelPoint,
    }
    if evt.Txid == "" {
      evt.Txid = channelPointTxid(evt.ChannelPoint)
    }
    if evt.PeerAlias == "" && evt.PeerPubkey != "" {
      evt.PeerAlias = n.lookupNodeAlias(evt.PeerPubkey)
    }
    return evt, fmt.Sprintf("channel:open:%s", ch.ChannelPoint)
  case lnrpc.ChannelEventUpdate_CLOSED_CHANNEL:
    ch := update.GetClosedChannel()
    if ch == nil {
      return Notification{}, ""
    }
    evt := Notification{
      OccurredAt: now,
      Type: "channel",
      Action: "close",
      Direction: "neutral",
      Status: "CLOSED",
      AmountSat: ch.SettledBalance,
      PeerPubkey: ch.RemotePubkey,
      ChannelID: int64(ch.ChanId),
      ChannelPoint: ch.ChannelPoint,
      Txid: ch.ClosingTxHash,
    }
    if evt.PeerAlias == "" && evt.PeerPubkey != "" {
      evt.PeerAlias = n.lookupNodeAlias(evt.PeerPubkey)
    }
    return evt, fmt.Sprintf("channel:close:%s", ch.ChannelPoint)
  case lnrpc.ChannelEventUpdate_PENDING_OPEN_CHANNEL:
    ch := update.GetPendingOpenChannel()
    if ch == nil {
      return Notification{}, ""
    }
    txid := txidFromBytes(ch.Txid)
    channelPoint := ""
    if txid != "" {
      channelPoint = fmt.Sprintf("%s:%d", txid, ch.OutputIndex)
    }
    evt := Notification{
      OccurredAt: now,
      Type: "channel",
      Action: "opening",
      Direction: "neutral",
      Status: "PENDING",
      AmountSat: 0,
      ChannelPoint: channelPoint,
      Txid: txid,
    }
    if info := n.lookupPendingChannel(channelPoint, txid); info != nil {
      if info.CapacitySat > 0 {
        evt.AmountSat = info.CapacitySat
      }
      if info.RemotePubkey != "" {
        evt.PeerPubkey = info.RemotePubkey
      }
      if info.PeerAlias != "" {
        evt.PeerAlias = info.PeerAlias
      }
      if evt.PeerAlias == "" && evt.PeerPubkey != "" {
        evt.PeerAlias = n.lookupNodeAlias(evt.PeerPubkey)
      }
      if evt.ChannelPoint == "" && info.ChannelPoint != "" {
        evt.ChannelPoint = info.ChannelPoint
      }
    }
    if channelPoint == "" {
      return evt, fmt.Sprintf("channel:opening:%d", time.Now().UnixNano())
    }
    return evt, fmt.Sprintf("channel:opening:%s", channelPoint)
  default:
    return Notification{}, ""
  }
}

func (n *Notifier) lookupPendingChannel(channelPoint string, txid string) *lndclient.PendingChannelInfo {
  ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
  defer cancel()

  pending, err := n.lnd.ListPendingChannels(ctx)
  if err != nil {
    return nil
  }

  pointLower := strings.ToLower(strings.TrimSpace(channelPoint))
  txidLower := strings.ToLower(strings.TrimSpace(txid))
  for i := range pending {
    item := pending[i]
    if item.Status != "opening" {
      continue
    }
    itemPoint := strings.ToLower(strings.TrimSpace(item.ChannelPoint))
    if pointLower != "" && itemPoint == pointLower {
      return &pending[i]
    }
    if pointLower == "" && txidLower != "" && strings.HasPrefix(itemPoint, txidLower+":") {
      return &pending[i]
    }
  }

  return nil
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

func channelPointTxid(channelPoint string) string {
  trimmed := strings.TrimSpace(channelPoint)
  if trimmed == "" {
    return ""
  }
  parts := strings.SplitN(trimmed, ":", 2)
  if len(parts) != 2 {
    return ""
  }
  return strings.TrimSpace(parts[0])
}

func (n *Notifier) lookupNodeAlias(pubkey string) string {
  trimmed := strings.TrimSpace(pubkey)
  if trimmed == "" {
    return ""
  }

  ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
  defer cancel()

  conn, err := n.lnd.DialLightning(ctx)
  if err != nil {
    return ""
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  info, err := client.GetNodeInfo(ctx, &lnrpc.NodeInfoRequest{PubKey: trimmed, IncludeChannels: false})
  if err != nil || info.GetNode() == nil {
    return ""
  }
  return info.GetNode().Alias
}

func (n *Notifier) runForwards() {
  debug := strings.EqualFold(strings.TrimSpace(os.Getenv("NOTIFICATIONS_DEBUG_FORWARDS")), "1") ||
    strings.EqualFold(strings.TrimSpace(os.Getenv("NOTIFICATIONS_DEBUG_FORWARDS")), "true")
  backfill := strings.EqualFold(strings.TrimSpace(os.Getenv("NOTIFICATIONS_FORWARDS_BACKFILL")), "1") ||
    strings.EqualFold(strings.TrimSpace(os.Getenv("NOTIFICATIONS_FORWARDS_BACKFILL")), "true")

  for {
    select {
    case <-n.stop:
      return
    case <-time.After(forwardsPollInterval):
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    cursorVal, _ := n.getCursor(ctx, "forwards_after")
    cancel()

    var after uint64
    if cursorVal != "" {
      if parsed, err := strconv.ParseUint(cursorVal, 10, 64); err == nil {
        after = parsed
      }
    }
    if after == 0 && !backfill {
      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      if latest, ok := n.latestForwardOccurredAt(ctx); ok {
        latestUnix := latest.Unix()
        now := time.Now().UTC().Unix()
        if latestUnix > 0 && now-latestUnix > int64(7*24*time.Hour/time.Second) {
          if now > int64(time.Hour/time.Second) {
            after = uint64(now - int64(time.Hour/time.Second))
          }
        } else if latestUnix > 0 {
          if latestUnix > 5 {
            after = uint64(latestUnix - 5)
          } else {
            after = uint64(latestUnix)
          }
        }
      } else {
        now := time.Now().UTC().Unix()
        if now > int64(time.Hour/time.Second) {
          after = uint64(now - int64(time.Hour/time.Second))
        }
      }
      cancel()
    }
    if debug {
      n.logger.Printf("notifications: forwards poll start (after=%d backfill=%t)", after, backfill)
    }

    conn, err := n.lnd.DialLightning(context.Background())
    if err != nil {
      n.logger.Printf("notifications: forwards poll dial failed: %v", err)
      continue
    }

    client := lnrpc.NewLightningClient(conn)
    endTime := uint64(time.Now().Unix())
    if after > endTime+300 {
      n.logger.Printf("notifications: forwards cursor ahead of time (after=%d end=%d), resetting", after, endTime)
      after = 0
      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      _ = n.setCursor(ctx, "forwards_after", "0")
      cancel()
    }
    if endTime <= after {
      endTime = after + 1
    }

    var indexOffset uint32
    processed := false
    for {
      reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
      res, err := client.ForwardingHistory(reqCtx, &lnrpc.ForwardingHistoryRequest{
        StartTime: after,
        EndTime: endTime,
        IndexOffset: indexOffset,
        NumMaxEvents: 200,
        PeerAliasLookup: true,
      })
      cancel()
      if err != nil {
        n.logger.Printf("notifications: forwards poll failed: %v", err)
        break
      }
      if debug {
        count := 0
        if res != nil {
          count = len(res.ForwardingEvents)
        }
        n.logger.Printf("notifications: forwards poll batch (count=%d offset=%d last_offset=%d after=%d end=%d)", count, indexOffset, res.LastOffsetIndex, after, endTime)
      }
      if res == nil || len(res.ForwardingEvents) == 0 {
        break
      }

      for _, fwd := range res.ForwardingEvents {
        occurredAt, _, tsKey := normalizeForwardTimestamp(fwd)
        amount := int64(fwd.AmtOut)
        fee := int64(fwd.Fee)
        feeMsat := int64(fwd.FeeMsat)
        evt := Notification{
          OccurredAt: occurredAt,
          Type: "forward",
          Action: "forwarded",
          Direction: "neutral",
          Status: "SETTLED",
          AmountSat: amount,
          FeeSat: fee,
          FeeMsat: feeMsat,
          PeerAlias: strings.TrimSpace(fmt.Sprintf("%s -> %s", fwd.PeerAliasIn, fwd.PeerAliasOut)),
          ChannelID: int64(fwd.ChanIdOut),
        }
        eventKey := fmt.Sprintf("forward:%d:%d:%d", fwd.IncomingHtlcId, fwd.OutgoingHtlcId, tsKey)
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        _, _ = n.upsertNotification(ctx, eventKey, evt)
        cancel()
      }

      processed = true
      if res.LastOffsetIndex <= indexOffset {
        break
      }
      indexOffset = res.LastOffsetIndex
      if len(res.ForwardingEvents) < 200 {
        break
      }
    }
    _ = conn.Close()

    if processed || after == 0 {
      ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
      _ = n.setCursor(ctx, "forwards_after", strconv.FormatUint(endTime, 10))
      cancel()
    }
  }
}

func nullableString(value string) any {
  if strings.TrimSpace(value) == "" {
    return nil
  }
  return value
}

func normalizeForwardTimestamp(fwd *lnrpc.ForwardingEvent) (time.Time, uint64, uint64) {
  if fwd == nil {
    now := uint64(time.Now().Unix())
    return time.Unix(int64(now), 0).UTC(), now, now * uint64(time.Second)
  }
  tsSec := fwd.Timestamp
  tsNs := fwd.TimestampNs
  if tsNs > 0 && tsNs < 1_000_000_000_000 {
    tsSec = tsNs
    tsNs = tsNs * uint64(time.Second)
  }
  if tsNs == 0 && tsSec > 0 {
    tsNs = tsSec * uint64(time.Second)
  }
  if tsSec == 0 && tsNs > 0 {
    tsSec = tsNs / uint64(time.Second)
  }
  if tsSec == 0 {
    tsSec = uint64(time.Now().Unix())
  }
  if tsNs == 0 {
    tsNs = tsSec * uint64(time.Second)
  }
  return time.Unix(0, int64(tsNs)).UTC(), tsSec, tsNs
}

func (n *Notifier) latestForwardOccurredAt(ctx context.Context) (time.Time, bool) {
  if n == nil || n.db == nil {
    return time.Time{}, false
  }
  var occurredAt time.Time
  err := n.db.QueryRow(ctx, `
select occurred_at from notifications
where type='forward'
order by occurred_at desc
limit 1`).Scan(&occurredAt)
  if err == pgx.ErrNoRows {
    return time.Time{}, false
  }
  if err != nil {
    return time.Time{}, false
  }
  return occurredAt, true
}

func normalizeHash(value string) string {
  return strings.ToLower(strings.TrimSpace(value))
}

func (n *Notifier) lookupPaymentByHash(ctx context.Context, paymentHash string) (*lnrpc.Payment, error) {
  normalized := normalizeHash(paymentHash)
  if normalized == "" {
    return nil, nil
  }

  conn, err := n.lnd.DialLightning(ctx)
  if err != nil {
    return nil, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  res, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
    IncludeIncomplete: true,
    MaxPayments: 400,
    Reversed: true,
  })
  if err != nil {
    return nil, err
  }

  for _, pay := range res.Payments {
    if pay == nil {
      continue
    }
    if normalizeHash(pay.PaymentHash) == normalized {
      return pay, nil
    }
  }
  return nil, nil
}

func (n *Notifier) rebalanceRouteInfo(ctx context.Context, pay *lnrpc.Payment) *rebalanceRouteInfo {
  if pay == nil {
    return nil
  }
  route := rebalanceRouteFromPayment(pay)
  if route == nil {
    return nil
  }
  hops := route.GetHops()
  if len(hops) == 0 {
    return nil
  }

  channelMap := n.channelMap(ctx)
  outHop := hops[0]
  inHop := hops[len(hops)-1]
  outInfo, outOK := channelMap[outHop.ChanId]
  inInfo, inOK := channelMap[inHop.ChanId]

  outAlias := pickAlias(outInfo.PeerAlias, outInfo.RemotePubkey, outHop.PubKey)
  inAlias := pickAlias(inInfo.PeerAlias, inInfo.RemotePubkey, inHop.PubKey)
  outPoint := ""
  inPoint := ""
  if outOK {
    outPoint = outInfo.ChannelPoint
  }
  if inOK {
    inPoint = inInfo.ChannelPoint
  }

  peerLabel := formatRebalanceLabel("Out", outAlias, "In", inAlias)
  channelLabel := formatRebalanceLabel("Channels", shortChannelPoint(outPoint), "", shortChannelPoint(inPoint))

  if peerLabel == "" && channelLabel == "" {
    return nil
  }

  return &rebalanceRouteInfo{
    PeerLabel: peerLabel,
    ChannelLabel: channelLabel,
  }
}

func (n *Notifier) rebalanceEvent(ctx context.Context, pay *lnrpc.Payment, occurredAt time.Time) Notification {
  paymentHash := ""
  if pay != nil {
    paymentHash = normalizeHash(pay.PaymentHash)
  }
  evt := Notification{
    OccurredAt: occurredAt,
    Type: "rebalance",
    Action: "rebalanced",
    Direction: "neutral",
    Status: "SETTLED",
    AmountSat: 0,
    FeeSat: 0,
    FeeMsat: 0,
    PaymentHash: paymentHash,
  }
  if pay != nil {
    evt.AmountSat = pay.ValueSat
    feeMsat := paymentFeeMsat(pay)
    evt.FeeMsat = feeMsat
    evt.FeeSat = feeMsat / 1000
  }
  if info := n.rebalanceRouteInfo(ctx, pay); info != nil {
    if info.PeerLabel != "" {
      evt.PeerAlias = info.PeerLabel
    }
    if info.ChannelLabel != "" {
      evt.Memo = info.ChannelLabel
    }
  }
  return evt
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

func paymentFeeMsat(pay *lnrpc.Payment) int64 {
  if pay == nil {
    return 0
  }
  if pay.FeeMsat != 0 {
    return pay.FeeMsat
  }
  if pay.FeeSat != 0 {
    return pay.FeeSat * 1000
  }
  if pay.Fee != 0 {
    return pay.Fee * 1000
  }
  route := rebalanceRouteFromPayment(pay)
  if route == nil {
    return 0
  }
  if route.TotalFeesMsat != 0 {
    return route.TotalFeesMsat
  }
  if route.TotalFees != 0 {
    return route.TotalFees * 1000
  }
  return 0
}

func hasKeysendRecord(records map[uint64][]byte) bool {
  if len(records) == 0 {
    return false
  }
  if _, ok := records[lndclient.KeysendPreimageRecord]; ok {
    return true
  }
  if _, ok := records[lndclient.KeysendMessageRecord]; ok {
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

func isProbePayment(pay *lnrpc.Payment) bool {
  if pay == nil {
    return false
  }
  if strings.TrimSpace(pay.PaymentRequest) != "" {
    return false
  }
  if isKeysendPayment(pay) {
    return false
  }
  return true
}

func keysendDestinationFromPayment(pay *lnrpc.Payment) string {
  route := rebalanceRouteFromPayment(pay)
  if route == nil {
    return ""
  }
  hops := route.GetHops()
  if len(hops) == 0 {
    return ""
  }
  return strings.TrimSpace(hops[len(hops)-1].PubKey)
}

func keysendMessageFromInvoice(invoice *lnrpc.Invoice) string {
  if invoice == nil {
    return ""
  }
  for _, htlc := range invoice.Htlcs {
    if htlc == nil {
      continue
    }
    if msg := keysendMessageFromRecords(htlc.CustomRecords); msg != "" {
      return msg
    }
  }
  return ""
}

func keysendMessageFromPayment(pay *lnrpc.Payment) string {
  if pay == nil {
    return ""
  }
  if msg := keysendMessageFromRecords(pay.FirstHopCustomRecords); msg != "" {
    return msg
  }
  for _, attempt := range pay.Htlcs {
    if attempt == nil || attempt.Route == nil {
      continue
    }
    for _, hop := range attempt.Route.Hops {
      if hop == nil {
        continue
      }
      if msg := keysendMessageFromRecords(hop.CustomRecords); msg != "" {
        return msg
      }
    }
  }
  return ""
}

func keysendMessageFromRecords(records map[uint64][]byte) string {
  if len(records) == 0 {
    return ""
  }
  raw, ok := records[lndclient.KeysendMessageRecord]
  if !ok || len(raw) == 0 {
    return ""
  }
  msg := strings.TrimSpace(string(raw))
  if msg == "" {
    return ""
  }
  if len(msg) > 500 {
    msg = msg[:500]
  }
  return msg
}

func (n *Notifier) keysendPeerFromInvoice(ctx context.Context, invoice *lnrpc.Invoice) (string, string) {
  if n == nil || invoice == nil {
    return "", ""
  }
  if len(invoice.Htlcs) == 0 {
    return "", ""
  }
  channelMap := n.channelMap(ctx)
  for _, htlc := range invoice.Htlcs {
    if htlc == nil || htlc.ChanId == 0 {
      continue
    }
    info, ok := channelMap[htlc.ChanId]
    if !ok {
      continue
    }
    return info.RemotePubkey, strings.TrimSpace(info.PeerAlias)
  }
  return "", ""
}

func (n *Notifier) channelMap(ctx context.Context) map[uint64]lndclient.ChannelInfo {
  channels, err := n.lnd.ListChannels(ctx)
  if err != nil {
    return map[uint64]lndclient.ChannelInfo{}
  }
  mapped := make(map[uint64]lndclient.ChannelInfo, len(channels))
  for _, ch := range channels {
    mapped[ch.ChannelID] = ch
  }
  return mapped
}

func pickAlias(alias string, pubkey string, hopPubkey string) string {
  trimmed := strings.TrimSpace(alias)
  if trimmed != "" {
    return trimmed
  }
  if pubkey == "" {
    pubkey = hopPubkey
  }
  return shortPubKey(pubkey)
}

func shortPubKey(value string) string {
  trimmed := strings.TrimSpace(value)
  if trimmed == "" {
    return ""
  }
  if len(trimmed) <= 12 {
    return trimmed
  }
  return trimmed[:12]
}

func shortChannelPoint(channelPoint string) string {
  trimmed := strings.TrimSpace(channelPoint)
  if trimmed == "" {
    return ""
  }
  parts := strings.SplitN(trimmed, ":", 2)
  if len(parts) != 2 {
    return ""
  }
  txid := parts[0]
  index := parts[1]
  if len(txid) > 8 {
    txid = txid[:8]
  }
  return fmt.Sprintf("%s...:%s", txid, index)
}

func formatRebalanceLabel(leftLabel string, leftValue string, rightLabel string, rightValue string) string {
  leftValue = strings.TrimSpace(leftValue)
  rightValue = strings.TrimSpace(rightValue)
  if leftValue == "" && rightValue == "" {
    return ""
  }
  if leftLabel != "" {
    if leftValue == "" {
      leftValue = "?"
    }
    leftValue = leftLabel + " " + leftValue
  }
  if rightLabel != "" {
    if rightValue == "" {
      rightValue = "?"
    }
    rightValue = rightLabel + " " + rightValue
  }
  if rightValue == "" {
    return leftValue
  }
  if leftValue == "" {
    return rightValue
  }
  return leftValue + " -> " + rightValue
}

func (n *Notifier) removeRebalanceInvoice(ctx context.Context, paymentHash string) error {
  normalized := normalizeHash(paymentHash)
  if normalized == "" {
    return nil
  }
  _, err := n.db.Exec(ctx, `
delete from notifications
where payment_hash=$1 and type='lightning' and action='received'
`, normalized)
  return err
}

func (n *Notifier) isRebalanceHash(ctx context.Context, paymentHash string) bool {
  if n == nil || n.db == nil {
    return false
  }
  normalized := normalizeHash(paymentHash)
  if normalized == "" {
    return false
  }
  var id int64
  err := n.db.QueryRow(ctx, `
select id from notifications where payment_hash=$1 and type='rebalance' limit 1
`, normalized).Scan(&id)
  return err == nil
}

func (n *Notifier) hasInvoiceHash(ctx context.Context, paymentHash string) bool {
  if n == nil || n.db == nil {
    return false
  }
  normalized := normalizeHash(paymentHash)
  if normalized == "" {
    return false
  }
  var id int64
  err := n.db.QueryRow(ctx, `
select id from notifications
where payment_hash=$1 and type='lightning' and action='received' and status='SETTLED'
limit 1
`, normalized).Scan(&id)
  return err == nil
}

func (n *Notifier) isSelfPayment(ctx context.Context, paymentRequest string, pay *lnrpc.Payment) bool {
  trimmed := strings.TrimSpace(paymentRequest)
  pubkey := n.lnd.CachedPubkey()
  if pubkey == "" {
    return false
  }

  if trimmed != "" {
    decoded, err := n.lnd.DecodeInvoice(ctx, trimmed)
    if err == nil && strings.EqualFold(decoded.Destination, pubkey) {
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

func nullableInt(value int64) any {
  if value == 0 {
    return nil
  }
  return value
}

func scanNotification(scanner notificationRowScanner) (Notification, error) {
  var evt Notification
  var peerPubkey pgtype.Text
  var peerAlias pgtype.Text
  var channelPoint pgtype.Text
  var txid pgtype.Text
  var paymentHash pgtype.Text
  var memo pgtype.Text
  var channelID pgtype.Int8
  err := scanner.Scan(
    &evt.ID,
    &evt.OccurredAt,
    &evt.Type,
    &evt.Action,
    &evt.Direction,
    &evt.Status,
    &evt.AmountSat,
    &evt.FeeSat,
    &evt.FeeMsat,
    &peerPubkey,
    &peerAlias,
    &channelID,
    &channelPoint,
    &txid,
    &paymentHash,
    &memo,
  )
  if err != nil {
    return Notification{}, err
  }
  if peerPubkey.Valid {
    evt.PeerPubkey = peerPubkey.String
  }
  if peerAlias.Valid {
    evt.PeerAlias = peerAlias.String
  }
  if channelID.Valid {
    evt.ChannelID = channelID.Int64
  }
  if channelPoint.Valid {
    evt.ChannelPoint = channelPoint.String
  }
  if txid.Valid {
    evt.Txid = txid.String
  }
  if paymentHash.Valid {
    evt.PaymentHash = paymentHash.String
  }
  if memo.Valid {
    evt.Memo = memo.String
  }
  return evt, nil
}

func (s *Server) handleNotificationsList(w http.ResponseWriter, r *http.Request) {
  if s.notifier == nil {
    msg := strings.TrimSpace(s.notifierErr)
    if msg == "" {
      msg = "notifications disabled"
    }
    writeError(w, http.StatusServiceUnavailable, msg)
    return
  }

  limit := 200
  if raw := r.URL.Query().Get("limit"); raw != "" {
    if parsed, err := strconv.Atoi(raw); err == nil {
      limit = parsed
    }
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()

  items, err := s.notifier.list(ctx, limit)
  if err != nil {
    s.logger.Printf("notifications: list failed: %v", err)
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to load notifications: %v", err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleNotificationsStream(w http.ResponseWriter, r *http.Request) {
  if s.notifier == nil {
    msg := strings.TrimSpace(s.notifierErr)
    if msg == "" {
      msg = "notifications disabled"
    }
    writeError(w, http.StatusServiceUnavailable, msg)
    return
  }

  flusher, ok := w.(http.Flusher)
  if !ok {
    writeError(w, http.StatusInternalServerError, "stream not supported")
    return
  }

  w.Header().Set("Content-Type", "text/event-stream")
  w.Header().Set("Cache-Control", "no-cache")
  w.Header().Set("Connection", "keep-alive")

  ch := s.notifier.Subscribe()
  defer s.notifier.Unsubscribe(ch)

  _, _ = w.Write([]byte("event: ready\ndata: {}\n\n"))
  flusher.Flush()

  ticker := time.NewTicker(25 * time.Second)
  defer ticker.Stop()

  for {
    select {
    case <-r.Context().Done():
      return
    case evt := <-ch:
      payload, err := json.Marshal(evt)
      if err != nil {
        continue
      }
      _, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
      flusher.Flush()
    case <-ticker.C:
      _, _ = w.Write([]byte("event: heartbeat\ndata: {}\n\n"))
      flusher.Flush()
    }
  }
}
