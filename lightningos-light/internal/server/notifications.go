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
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	notificationRetentionDays         = 365
	notificationCleanupInterval       = 6 * time.Hour
	paymentsPollInterval              = 15 * time.Second
	paymentsPollPageSize              = 200
	forwardsPollInterval              = 30 * time.Second
	pendingChannelsPollInterval       = 30 * time.Second
	waitingCloseRecoveryRetryInterval = 5 * time.Minute
	paymentsPendingMaxAge             = 48 * time.Hour
	telegramActivityMirrorQueueSize   = 256
	telegramActivityMirrorSendTimeout = 1 * time.Second
	telegramActivityMirrorLiveGrace   = 2 * time.Minute
	invoiceCatchupLiveGrace           = 2 * time.Minute
)

const (
	notificationsSecretsPath  = "/etc/lightningos/secrets.env"
	notificationsSecretsDir   = "/etc/lightningos"
	notificationsDBName       = "lightningos"
	notificationsDBUser       = "losapp"
	paymentsPendingCursorKey  = "payments_inflight"
	paymentsPendingMax        = 200
	paymentsPendingFastChecks = 4
	paymentsPendingSlowChecks = 10
)

var notificationsSecretsMu sync.RWMutex

type Notification struct {
	ID                int64     `json:"id"`
	OccurredAt        time.Time `json:"occurred_at"`
	Type              string    `json:"type"`
	Action            string    `json:"action"`
	Direction         string    `json:"direction"`
	Status            string    `json:"status"`
	AmountSat         int64     `json:"amount_sat"`
	FeeSat            int64     `json:"fee_sat"`
	FeeMsat           int64     `json:"fee_msat"`
	PeerPubkey        string    `json:"peer_pubkey,omitempty"`
	PeerAlias         string    `json:"peer_alias,omitempty"`
	ChannelID         int64     `json:"channel_id,omitempty"`
	ChannelPoint      string    `json:"channel_point,omitempty"`
	ChannelAlias      string    `json:"channel_alias,omitempty"`
	ChanIDIn          int64     `json:"chan_id_in,omitempty"`
	ChanIDOut         int64     `json:"chan_id_out,omitempty"`
	AmountInMsat      int64     `json:"amount_in_msat,omitempty"`
	AmountOutMsat     int64     `json:"amount_out_msat,omitempty"`
	PeerPubkeyIn      string    `json:"peer_pubkey_in,omitempty"`
	PeerPubkeyOut     string    `json:"peer_pubkey_out,omitempty"`
	ChannelPointIn    string    `json:"channel_point_in,omitempty"`
	ChannelPointOut   string    `json:"channel_point_out,omitempty"`
	RebalSourceChanID int64     `json:"rebal_source_chan_id,omitempty"`
	RebalTargetChanID int64     `json:"rebal_target_chan_id,omitempty"`
	RebalSourcePoint  string    `json:"rebal_source_point,omitempty"`
	RebalTargetPoint  string    `json:"rebal_target_point,omitempty"`
	RebalSourcePubkey string    `json:"rebal_source_pubkey,omitempty"`
	RebalTargetPubkey string    `json:"rebal_target_pubkey,omitempty"`
	Txid              string    `json:"txid,omitempty"`
	PaymentHash       string    `json:"payment_hash,omitempty"`
	Memo              string    `json:"memo,omitempty"`
}

type paymentRouteAttemptRecord struct {
	PaymentHash        string
	PaymentIndex       int64
	PaymentType        string
	PaymentStatus      string
	PaymentCreatedAt   time.Time
	AttemptID          int64
	AttemptStatus      string
	AttemptStartedAt   time.Time
	AttemptResolvedAt  time.Time
	FailureCode        string
	FailureSourceIndex int64
	TotalAmtMsat       int64
	TotalFeeMsat       int64
	TotalTimeLock      int64
	HopCount           int
}

type paymentRouteHopRecord struct {
	PaymentHash      string
	AttemptID        int64
	HopIndex         int
	NodePubkey       string
	NodeAlias        string
	ChannelID        int64
	ChannelCapacity  int64
	AmtToForwardMsat int64
	FeeMsat          int64
	Expiry           int64
	CostToMsat       int64
	IsFirstHop       bool
	IsFinalHop       bool
}

type rebalanceRouteInfo struct {
	PeerLabel    string
	ChannelLabel string
}

type notificationRowScanner interface {
	Scan(dest ...any) error
}

type pendingPaymentEntry struct {
	Hash         string `json:"hash"`
	LastSeen     int64  `json:"last_seen"`
	PaymentIndex uint64 `json:"payment_index,omitempty"`
	LastChecked  int64  `json:"last_checked,omitempty"`
	NextCheck    int64  `json:"next_check,omitempty"`
	CheckCount   int    `json:"check_count,omitempty"`
}

type stuckInFlightPayment struct {
	Hash         string
	PaymentIndex uint64
}

type waitingCloseRecoveryInfo struct {
	Attempts          int
	LastAttemptAt     time.Time
	LastResult        string
	LastError         string
	LastRecoveredTxid string
}

type notificationUpsertOptions struct {
	suppressMirror bool
}

type Notifier struct {
	db     *pgxpool.Pool
	lnd    *lndclient.Client
	logger *log.Logger

	mu                            sync.Mutex
	backupMu                      sync.Mutex
	pendingMu                     sync.Mutex
	subscribers                   map[chan Notification]struct{}
	started                       bool
	stop                          chan struct{}
	lastCleanup                   time.Time
	backupSent                    map[string]time.Time
	pendingSent                   map[string]time.Time
	waitingCloseRecoveries        map[string]waitingCloseRecoveryInfo
	telegramMirrorQueue           chan Notification
	telegramActivityMirrorEnabled atomic.Bool
	startedAt                     time.Time
	paymentsCatchupMode           atomic.Bool
	invoicesCatchupMode           atomic.Bool
}

func NewNotifier(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *Notifier {
	return &Notifier{
		db:                     db,
		lnd:                    lnd,
		logger:                 logger,
		subscribers:            map[chan Notification]struct{}{},
		backupSent:             map[string]time.Time{},
		pendingSent:            map[string]time.Time{},
		waitingCloseRecoveries: map[string]waitingCloseRecoveryInfo{},
		telegramMirrorQueue:    make(chan Notification, telegramActivityMirrorQueueSize),
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
	n.startedAt = time.Now().UTC()
	n.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := n.ensureSchema(ctx); err != nil {
		n.logger.Printf("notifications disabled: failed to init schema: %v", err)
		cancel()
		return
	}
	cancel()

	settingsCtx, settingsCancel := context.WithTimeout(context.Background(), 3*time.Second)
	n.refreshTelegramActivityMirrorSettings(settingsCtx)
	settingsCancel()

	go n.runRecoverable("telegram_activity_mirror", n.runTelegramActivityMirrorLoop)
	go n.runRecoverable("invoices", n.runInvoices)
	go n.runRecoverable("payments", n.runPayments)
	go n.runRecoverable("transactions", n.runTransactions)
	go n.runRecoverable("channels", n.runChannels)
	go n.runRecoverable("pending_channels", n.runPendingChannels)
	go n.runRecoverable("forwards", n.runForwards)
}

// runRecoverable wraps a notifier loop so that a panic doesn't kill the
// goroutine silently. Logs the panic with stack trace and restarts the loop
// after a short delay. Returns when n.stop is closed.
func (n *Notifier) runRecoverable(name string, body func()) {
	for {
		select {
		case <-n.stop:
			return
		default:
		}
		func() {
			defer func() {
				if r := recover(); r != nil && n.logger != nil {
					n.logger.Printf("notifications: %s goroutine panicked: %v\n%s", name, r, debug.Stack())
				}
			}()
			body()
		}()
		// body() returned (n.stop fired) or panicked. If we panicked, give
		// dependent services a beat before retrying.
		select {
		case <-n.stop:
			return
		case <-time.After(5 * time.Second):
		}
	}
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
	notificationsSecretsMu.RLock()
	defer notificationsSecretsMu.RUnlock()
	return readEnvFileValueLocked(path, key)
}

func readEnvFileValueLocked(path, key string) (string, error) {
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
	return writeEnvFileValues(path, map[string]string{key: value})
}

func writeEnvFileValues(path string, updates map[string]string) error {
	notificationsSecretsMu.Lock()
	defer notificationsSecretsMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := []string{}
	if len(data) > 0 {
		lines = strings.Split(string(data), "\n")
	}

	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	updated := make(map[string]bool, len(updates))
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, key := range keys {
			prefix := key + "="
			if strings.HasPrefix(trimmed, prefix) {
				lines[i] = fmt.Sprintf("%s=%s", key, updates[key])
				updated[key] = true
				break
			}
		}
	}
	for _, key := range keys {
		if !updated[key] {
			lines = append(lines, fmt.Sprintf("%s=%s", key, updates[key]))
		}
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
  channel_alias text,
  chan_id_in bigint,
  chan_id_out bigint,
  amount_in_msat bigint,
  amount_out_msat bigint,
  peer_pubkey_in text,
  peer_pubkey_out text,
  channel_point_in text,
  channel_point_out text,
  rebal_source_chan_id bigint,
  rebal_target_chan_id bigint,
  rebal_source_point text,
  rebal_target_point text,
  rebal_source_pubkey text,
  rebal_target_pubkey text,
  txid text,
  payment_hash text,
  memo text,
  created_at timestamptz not null default now()
);

alter table notifications add column if not exists fee_msat bigint not null default 0;
alter table notifications add column if not exists channel_alias text;
alter table notifications add column if not exists chan_id_in bigint;
alter table notifications add column if not exists chan_id_out bigint;
alter table notifications add column if not exists amount_in_msat bigint;
alter table notifications add column if not exists amount_out_msat bigint;
alter table notifications add column if not exists peer_pubkey_in text;
alter table notifications add column if not exists peer_pubkey_out text;
alter table notifications add column if not exists channel_point_in text;
alter table notifications add column if not exists channel_point_out text;
alter table notifications add column if not exists rebal_source_chan_id bigint;
alter table notifications add column if not exists rebal_target_chan_id bigint;
alter table notifications add column if not exists rebal_source_point text;
alter table notifications add column if not exists rebal_target_point text;
alter table notifications add column if not exists rebal_source_pubkey text;
alter table notifications add column if not exists rebal_target_pubkey text;

create index if not exists notifications_occurred_at_idx on notifications (occurred_at desc);
create index if not exists notifications_type_idx on notifications (type);
create index if not exists notifications_payment_hash_idx on notifications (payment_hash);
create index if not exists notifications_chan_out_idx on notifications (chan_id_out, occurred_at desc);
create index if not exists notifications_chan_in_idx on notifications (chan_id_in, occurred_at desc);
create index if not exists notifications_rebal_target_idx on notifications (rebal_target_chan_id, occurred_at desc);
create index if not exists notifications_rebal_source_idx on notifications (rebal_source_chan_id, occurred_at desc);

create table if not exists payment_route_attempts (
  payment_hash text not null,
  payment_index bigint not null default 0,
  payment_type text not null,
  payment_status text not null,
  payment_created_at timestamptz not null,
  attempt_id bigint not null,
  attempt_status text not null,
  attempt_started_at timestamptz,
  attempt_resolved_at timestamptz,
  failure_code text,
  failure_source_index integer,
  total_amt_msat bigint not null default 0,
  total_fee_msat bigint not null default 0,
  total_time_lock integer not null default 0,
  hop_count integer not null default 0,
  created_at timestamptz not null default now(),
  primary key (payment_hash, attempt_id)
);

create index if not exists payment_route_attempts_created_at_idx on payment_route_attempts (payment_created_at desc);
create index if not exists payment_route_attempts_type_idx on payment_route_attempts (payment_type, payment_created_at desc);
create index if not exists payment_route_attempts_status_idx on payment_route_attempts (payment_status, attempt_status, payment_created_at desc);

create table if not exists payment_route_hops (
  payment_hash text not null,
  attempt_id bigint not null,
  hop_index integer not null,
  node_pubkey text,
  node_alias text,
  channel_id bigint,
  channel_capacity_sat bigint not null default 0,
  amt_to_forward_msat bigint not null default 0,
  fee_msat bigint not null default 0,
  expiry integer not null default 0,
  cost_to_msat bigint not null default 0,
  is_first_hop boolean not null default false,
  is_final_hop boolean not null default false,
  created_at timestamptz not null default now(),
  primary key (payment_hash, attempt_id, hop_index),
  foreign key (payment_hash, attempt_id)
    references payment_route_attempts (payment_hash, attempt_id)
    on delete cascade
);

create index if not exists payment_route_hops_pubkey_idx on payment_route_hops (node_pubkey);
create index if not exists payment_route_hops_channel_idx on payment_route_hops (channel_id);
create index if not exists payment_route_hops_cost_idx on payment_route_hops (cost_to_msat desc);

  create table if not exists notification_cursors (
    key text primary key,
    value text not null,
    updated_at timestamptz not null default now()
  );

  create table if not exists telegram_notification_settings (
    id integer primary key,
    scb_backup_enabled boolean not null default true,
    summary_enabled boolean not null default false,
    summary_interval_min integer not null default 720,
    summary_last_sent_at timestamptz,
    system_summary_enabled boolean not null default false,
    system_summary_interval_min integer not null default 720,
    system_summary_last_sent_at timestamptz,
    activity_mirror_enabled boolean not null default false,
    autofee_summary_enabled boolean not null default false,
    last_update_id bigint not null default 0,
    updated_at timestamptz not null default now()
  );

  alter table telegram_notification_settings add column if not exists system_summary_enabled boolean not null default false;
  alter table telegram_notification_settings add column if not exists system_summary_interval_min integer not null default 720;
  alter table telegram_notification_settings add column if not exists system_summary_last_sent_at timestamptz;
  alter table telegram_notification_settings add column if not exists activity_mirror_enabled boolean not null default false;
  alter table telegram_notification_settings add column if not exists autofee_summary_enabled boolean not null default false;

  insert into telegram_notification_settings (id)
  values (1)
  on conflict (id) do nothing;
  `)
	return err
}

func (n *Notifier) upsertNotification(ctx context.Context, eventKey string, evt Notification) (Notification, error) {
	return n.upsertNotificationWithOptions(ctx, eventKey, evt, notificationUpsertOptions{})
}

func (n *Notifier) upsertNotificationWithOptions(ctx context.Context, eventKey string, evt Notification, opts notificationUpsertOptions) (Notification, error) {
	if eventKey == "" {
		return Notification{}, errors.New("event key required")
	}

	prevStatus := ""
	prevType := ""
	prevAction := ""
	hadPrev := false
	prevErr := n.db.QueryRow(ctx, `
select status, type, action
from notifications
where event_key=$1
`, eventKey).Scan(&prevStatus, &prevType, &prevAction)
	if prevErr == nil {
		hadPrev = true
	} else if prevErr != pgx.ErrNoRows {
		return Notification{}, prevErr
	}

	row := n.db.QueryRow(ctx, `
with ins as (
insert into notifications (
  event_key, occurred_at, type, action, direction, status, amount_sat, fee_sat, fee_msat,
  peer_pubkey, peer_alias, channel_id, channel_point, channel_alias,
  chan_id_in, chan_id_out, amount_in_msat, amount_out_msat,
  peer_pubkey_in, peer_pubkey_out, channel_point_in, channel_point_out,
  rebal_source_chan_id, rebal_target_chan_id, rebal_source_point, rebal_target_point,
  rebal_source_pubkey, rebal_target_pubkey,
  txid, payment_hash, memo
) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
  $15,$16,$17,$18,
  $19,$20,$21,$22,
  $23,$24,$25,$26,
  $27,$28,$29,
  $30,$31
)
on conflict (event_key) do nothing
returning id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo,
  true as inserted
),
upd as (
update notifications set
  occurred_at = $2,
  type = $3,
  action = $4,
  direction = $5,
  status = $6,
  amount_sat = $7,
  fee_sat = $8,
  fee_msat = $9,
  peer_pubkey = $10,
  peer_alias = $11,
  channel_id = $12,
  channel_point = $13,
  channel_alias = $14,
  chan_id_in = $15,
  chan_id_out = $16,
  amount_in_msat = $17,
  amount_out_msat = $18,
  peer_pubkey_in = $19,
  peer_pubkey_out = $20,
  channel_point_in = $21,
  channel_point_out = $22,
  rebal_source_chan_id = $23,
  rebal_target_chan_id = $24,
  rebal_source_point = $25,
  rebal_target_point = $26,
  rebal_source_pubkey = $27,
  rebal_target_pubkey = $28,
  txid = $29,
  payment_hash = $30,
  memo = $31
where event_key = $1
  and not exists (select 1 from ins)
returning id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo,
  false as inserted
)
select id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo,
  inserted
from ins
union all
select id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo,
  inserted
from upd
limit 1
`, eventKey, evt.OccurredAt, evt.Type, evt.Action, evt.Direction, evt.Status,
		evt.AmountSat, evt.FeeSat, evt.FeeMsat, nullableString(evt.PeerPubkey), nullableString(evt.PeerAlias),
		nullableInt(evt.ChannelID), nullableString(evt.ChannelPoint), nullableString(evt.ChannelAlias),
		nullableInt(evt.ChanIDIn), nullableInt(evt.ChanIDOut),
		nullableInt(evt.AmountInMsat), nullableInt(evt.AmountOutMsat),
		nullableString(evt.PeerPubkeyIn), nullableString(evt.PeerPubkeyOut),
		nullableString(evt.ChannelPointIn), nullableString(evt.ChannelPointOut),
		nullableInt(evt.RebalSourceChanID), nullableInt(evt.RebalTargetChanID),
		nullableString(evt.RebalSourcePoint), nullableString(evt.RebalTargetPoint),
		nullableString(evt.RebalSourcePubkey), nullableString(evt.RebalTargetPubkey),
		nullableString(evt.Txid), nullableString(evt.PaymentHash), nullableString(evt.Memo),
	)

	var stored Notification
	var inserted bool
	stored, inserted, err := scanNotificationWithInserted(row)
	if err != nil {
		return Notification{}, err
	}

	n.cleanupIfNeeded()
	n.broadcast(stored)
	shouldMirror := inserted
	if !shouldMirror && hadPrev {
		changedStatus := !strings.EqualFold(strings.TrimSpace(prevStatus), strings.TrimSpace(stored.Status))
		changedType := !strings.EqualFold(strings.TrimSpace(prevType), strings.TrimSpace(stored.Type))
		changedAction := !strings.EqualFold(strings.TrimSpace(prevAction), strings.TrimSpace(stored.Action))
		shouldMirror = changedStatus || changedType || changedAction
	}
	if shouldMirror && !opts.suppressMirror && !shouldSuppressHistoricalTelegramActivityMirror(stored, n.startedAt, inserted, hadPrev, prevType, prevAction) {
		n.enqueueTelegramActivityMirror(stored)
	}
	return stored, nil
}

func shouldSuppressHistoricalTelegramActivityMirror(evt Notification, startedAt time.Time, _, _ bool, _, _ string) bool {
	// Any event whose OccurredAt is from before this manager session is
	// backlog: never mirror to Telegram. Includes both fresh historical
	// inserts (post-reboot catchup) and status updates on rows that
	// originated in a previous session. The DB upsert and UI broadcast
	// still happen — only the Telegram enqueue is gated here.
	return notificationIsHistoricalCatchup(startedAt, evt.OccurredAt, telegramActivityMirrorLiveGrace)
}

func notificationIsHistoricalCatchup(startedAt, occurredAt time.Time, grace time.Duration) bool {
	if startedAt.IsZero() || occurredAt.IsZero() {
		return false
	}
	return occurredAt.Before(startedAt.Add(-grace))
}

func shouldContinuePaymentsCatchup(batchSize int) bool {
	return batchSize >= paymentsPollPageSize
}

func (n *Notifier) refreshTelegramActivityMirrorSettings(ctx context.Context) {
	if n == nil || n.db == nil {
		return
	}
	settings, err := loadTelegramNotificationSettings(ctx, n.db)
	if err != nil {
		if n.logger != nil {
			n.logger.Printf("notifications: failed to load telegram activity mirror setting: %v", err)
		}
		return
	}
	n.telegramActivityMirrorEnabled.Store(settings.ActivityMirrorEnabled)
}

func (n *Notifier) setTelegramActivityMirrorEnabled(enabled bool) {
	if n == nil {
		return
	}
	n.telegramActivityMirrorEnabled.Store(enabled)
}

func (n *Notifier) enqueueTelegramActivityMirror(evt Notification) {
	if n == nil || n.telegramMirrorQueue == nil || !n.telegramActivityMirrorEnabled.Load() {
		return
	}
	if evt.Type == "rebalance" && strings.EqualFold(strings.TrimSpace(evt.Status), "IN_FLIGHT") {
		return
	}
	select {
	case n.telegramMirrorQueue <- evt:
	default:
		if n.logger != nil {
			n.logger.Printf("notifications: telegram activity mirror queue full, dropping event id=%d type=%s", evt.ID, evt.Type)
		}
	}
}

func (n *Notifier) runTelegramActivityMirrorLoop() {
	if n == nil || n.telegramMirrorQueue == nil {
		return
	}
	for {
		select {
		case <-n.stop:
			return
		case evt := <-n.telegramMirrorQueue:
			if !n.telegramActivityMirrorEnabled.Load() {
				continue
			}
			cfg := readTelegramBackupConfig()
			if !cfg.configured() {
				continue
			}
			msg := telegramActivityMirrorMessage(evt)
			if strings.TrimSpace(msg) == "" {
				continue
			}
			sendCtx, sendCancel := context.WithTimeout(context.Background(), telegramActivityMirrorSendTimeout)
			err := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, msg)
			sendCancel()
			if err != nil && n.logger != nil {
				n.logger.Printf("notifications: telegram activity mirror send failed: %v", err)
			}
		}
	}
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
  peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo
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

func normalizePendingPaymentEntry(entry pendingPaymentEntry, now int64) (pendingPaymentEntry, bool) {
	hash := normalizeHash(entry.Hash)
	if hash == "" {
		return pendingPaymentEntry{}, false
	}
	entry.Hash = hash
	if entry.LastSeen <= 0 {
		entry.LastSeen = now
	}
	if entry.LastChecked < 0 {
		entry.LastChecked = 0
	}
	if entry.NextCheck < 0 {
		entry.NextCheck = 0
	}
	if entry.CheckCount < 0 {
		entry.CheckCount = 0
	}
	return entry, true
}

func pendingPaymentBackoff(checkCount int) time.Duration {
	switch {
	case checkCount <= paymentsPendingFastChecks:
		return paymentsPollInterval
	case checkCount <= paymentsPendingSlowChecks:
		return time.Minute
	default:
		return 5 * time.Minute
	}
}

func markPendingPaymentChecked(entry pendingPaymentEntry, now int64, stillInFlight bool) pendingPaymentEntry {
	if stillInFlight || entry.LastSeen <= 0 {
		entry.LastSeen = now
	}
	entry.LastChecked = now
	entry.CheckCount++
	entry.NextCheck = now + int64(pendingPaymentBackoff(entry.CheckCount)/time.Second)
	return entry
}

func observePendingPayment(pending map[string]pendingPaymentEntry, paymentHash string, paymentIndex uint64, now int64) bool {
	normalized := normalizeHash(paymentHash)
	if normalized == "" {
		return false
	}

	entry := pending[normalized]
	original := entry
	entry.Hash = normalized
	if paymentIndex > 0 && entry.PaymentIndex != paymentIndex {
		entry.PaymentIndex = paymentIndex
		entry.LastChecked = 0
		entry.NextCheck = 0
		entry.CheckCount = 0
	}
	entry = markPendingPaymentChecked(entry, now, true)
	pending[normalized] = entry
	return original != entry
}

func (n *Notifier) loadStuckInFlightPayments(ctx context.Context) []stuckInFlightPayment {
	if n == nil || n.db == nil {
		return nil
	}
	cutoff := time.Now().Add(-paymentsPendingMaxAge)
	rows, err := n.db.Query(ctx, `
select n.payment_hash, coalesce(max(pra.payment_index), 0)
from notifications n
left join payment_route_attempts pra on pra.payment_hash = n.payment_hash
where n.status='IN_FLIGHT'
  and n.type in ('rebalance','keysend','lightning')
  and n.payment_hash is not null
  and n.payment_hash <> ''
  and n.occurred_at > $1
group by n.payment_hash
`, cutoff)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var payments []stuckInFlightPayment
	for rows.Next() {
		var hash string
		var paymentIndex int64
		if err := rows.Scan(&hash, &paymentIndex); err != nil {
			continue
		}
		normalized := normalizeHash(hash)
		if normalized == "" {
			continue
		}
		payment := stuckInFlightPayment{Hash: normalized}
		if paymentIndex > 0 {
			payment.PaymentIndex = uint64(paymentIndex)
		}
		payments = append(payments, payment)
	}
	return payments
}

func (n *Notifier) loadPendingPayments(ctx context.Context) map[string]pendingPaymentEntry {
	pending := map[string]pendingPaymentEntry{}
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
			normalized, ok := normalizePendingPaymentEntry(entry, now)
			if !ok {
				continue
			}
			pending[normalized.Hash] = normalized
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
			pending[normalized] = pendingPaymentEntry{Hash: normalized, LastSeen: lastSeen}
		}
	}
	return pending
}

func (n *Notifier) storePendingPayments(ctx context.Context, pending map[string]pendingPaymentEntry) {
	if n == nil || n.db == nil {
		return
	}
	cutoff := time.Now().Add(-paymentsPendingMaxAge).Unix()
	entries := make([]pendingPaymentEntry, 0, len(pending))
	now := time.Now().Unix()
	for hash, entry := range pending {
		if strings.TrimSpace(entry.Hash) == "" {
			entry.Hash = hash
		}
		normalized, ok := normalizePendingPaymentEntry(entry, now)
		if !ok {
			continue
		}
		if normalized.LastSeen < cutoff {
			continue
		}
		entries = append(entries, normalized)
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
	var pay *lnrpc.Payment
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
		if lookup, lookupErr := n.lookupPaymentByHash(feeCtx, normalized); lookupErr == nil && lookup != nil {
			pay = lookup
			if feeMsat := paymentFeeMsat(pay); feeMsat != 0 {
				payFeeMsat = feeMsat
				payFee = feeMsat / 1000
			}
		}
		cancel()
	}
	if pay == nil {
		lookupCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		pay, _ = n.lookupPaymentByHash(lookupCtx, normalized)
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
	rebalanceMeta := Notification{}
	if pay != nil {
		rebalanceMeta = n.rebalanceEvent(ctx, pay, invAt)
		if memoValue == nil {
			memoValue = nullableString(rebalanceMeta.Memo)
		}
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
  occurred_at=$6,
  rebal_source_chan_id=$7,
  rebal_target_chan_id=$8,
  rebal_source_point=$9,
  rebal_target_point=$10,
  rebal_source_pubkey=$11,
  rebal_target_pubkey=$12,
  peer_pubkey=null,
  peer_alias=$13,
  channel_id=null,
  channel_point=null,
  channel_alias=null
where id=$1
returning id, occurred_at, type, action, direction, status, amount_sat, fee_sat,
  fee_msat, peer_pubkey, peer_alias, channel_id, channel_point, channel_alias, txid, payment_hash, memo
`, payID, invAmount, payFee, payFeeMsat, memoValue, invAt,
		nullableInt(rebalanceMeta.RebalSourceChanID), nullableInt(rebalanceMeta.RebalTargetChanID),
		nullableString(rebalanceMeta.RebalSourcePoint), nullableString(rebalanceMeta.RebalTargetPoint),
		nullableString(rebalanceMeta.RebalSourcePubkey), nullableString(rebalanceMeta.RebalTargetPubkey),
		nullableString(rebalanceMeta.PeerAlias))
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

	if pay != nil {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := n.updatePaymentRouteHistoryType(updateCtx, normalized, "rebalance"); err != nil && n.logger != nil {
			n.logger.Printf("notifications: payment route history rebalance update failed for %s: %v", normalized, err)
		}
		cancel()
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
		if strings.TrimSpace(cursorVal) == "" {
			n.invoicesCatchupMode.Store(true)
		}

		var settleIndex uint64
		if cursorVal != "" {
			if parsed, err := strconv.ParseUint(cursorVal, 10, 64); err == nil {
				settleIndex = parsed
			}
		}

		conn, release, err := n.lnd.BorrowLightning(context.Background(), true)
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
			release()
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			invoice, err := stream.Recv()
			if err != nil {
				n.logger.Printf("notifications: invoice stream ended: %v", err)
				release()
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
			suppressMirror := false
			if n.invoicesCatchupMode.Load() {
				if notificationIsHistoricalCatchup(n.startedAt, occurredAt, invoiceCatchupLiveGrace) {
					suppressMirror = true
				} else {
					n.invoicesCatchupMode.Store(false)
				}
			}
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
				OccurredAt:  occurredAt,
				Type:        evtType,
				Action:      "received",
				Direction:   "in",
				Status:      "SETTLED",
				AmountSat:   amount,
				PeerPubkey:  peerPubkey,
				PeerAlias:   peerAlias,
				PaymentHash: hash,
				Memo:        memo,
			}
			ctxChannel, cancelChannel := context.WithTimeout(context.Background(), 4*time.Second)
			if chanID, channelPoint, channelAlias := n.invoiceReceiveChannel(ctxChannel, invoice); chanID != 0 || channelPoint != "" || channelAlias != "" {
				evt.ChannelID = chanID
				evt.ChannelPoint = channelPoint
				evt.ChannelAlias = channelAlias
			}
			cancelChannel()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if n.isRebalanceHash(ctx, hash) {
				_ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
				cancel()
				continue
			}

			if pay, err := n.lookupPaymentByHash(ctx, hash); err == nil && pay != nil {
				if n.isSelfPayment(ctx, pay.PaymentRequest, pay) {
					if shouldSuppressExternalFailedRebalance(pay.Status.String(), memo) {
						_ = n.removeRebalanceInvoice(ctx, hash)
						_ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
						cancel()
						continue
					}
					rebalanceEvt := n.rebalanceEvent(ctx, pay, occurredAt)
					if _, err := n.upsertNotificationWithOptions(ctx, fmt.Sprintf("payment:%s", hash), rebalanceEvt, notificationUpsertOptions{suppressMirror: suppressMirror}); err == nil {
						_ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
					}
					cancel()
					continue
				}
			}

			if _, err := n.upsertNotificationWithOptions(ctx, fmt.Sprintf("invoice:%s", hash), evt, notificationUpsertOptions{suppressMirror: suppressMirror}); err == nil {
				_ = n.setCursor(ctx, "invoice_settle_index", strconv.FormatUint(settleIndex, 10))
				n.reconcileRebalance(ctx, hash)
			}
			cancel()
		}

		time.Sleep(2 * time.Second)
	}
}

func (n *Notifier) runPayments() {
	pending := map[string]pendingPaymentEntry{}
	if n != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		pending = n.loadPendingPayments(ctx)
		cancel()
		stuckCtx, stuckCancel := context.WithTimeout(context.Background(), 5*time.Second)
		nowUnix := time.Now().Unix()
		for _, stuck := range n.loadStuckInFlightPayments(stuckCtx) {
			entry, ok := pending[stuck.Hash]
			if !ok {
				pending[stuck.Hash] = pendingPaymentEntry{Hash: stuck.Hash, LastSeen: nowUnix, PaymentIndex: stuck.PaymentIndex}
				continue
			}
			if stuck.PaymentIndex > 0 && entry.PaymentIndex == 0 {
				entry.PaymentIndex = stuck.PaymentIndex
				pending[stuck.Hash] = entry
			}
		}
		stuckCancel()
	}

	processPayment := func(pay *lnrpc.Payment, suppressMirror bool) {
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
		if isRebalance && shouldSuppressExternalFailedRebalance(status, memo) {
			// Ignore failed self-payments from external tools in notifications/telegram
			// and clean any invoice record persisted before payment classification.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = n.removeRebalanceInvoice(cleanupCtx, paymentHash)
			cleanupCancel()
			return
		}
		if peerAlias == "" && peerPubkey != "" {
			peerAlias = n.lookupNodeAlias(peerPubkey)
		}

		evtType := "lightning"
		if isKeysend {
			evtType = "keysend"
		}
		evt := Notification{
			OccurredAt:  occurredAt,
			Type:        evtType,
			Action:      "sent",
			Direction:   "out",
			Status:      status,
			AmountSat:   amount,
			FeeSat:      fee,
			FeeMsat:     feeMsat,
			PeerPubkey:  peerPubkey,
			PeerAlias:   peerAlias,
			PaymentHash: paymentHash,
			Memo:        memo,
		}
		ctxChannel, cancelChannel := context.WithTimeout(context.Background(), 4*time.Second)
		if chanID, channelPoint, channelAlias := n.paymentSendChannel(ctxChannel, pay); chanID != 0 || channelPoint != "" || channelAlias != "" {
			evt.ChannelID = chanID
			evt.ChannelPoint = channelPoint
			evt.ChannelAlias = channelAlias
		}
		cancelChannel()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if isRebalance {
			evt = n.rebalanceEvent(ctx, pay, occurredAt)
		}
		if _, err := n.upsertNotificationWithOptions(ctx, fmt.Sprintf("payment:%s", paymentHash), evt, notificationUpsertOptions{suppressMirror: suppressMirror}); err == nil {
			paymentType := paymentRouteType(pay, isKeysend, isRebalance)
			if routeErr := n.replacePaymentRouteHistory(ctx, pay, paymentType, occurredAt); routeErr != nil && n.logger != nil {
				n.logger.Printf("notifications: payment route history upsert failed for %s: %v", paymentHash, routeErr)
			}
			if isRebalance {
				_ = n.removeRebalanceInvoice(ctx, paymentHash)
			} else {
				n.reconcileRebalance(ctx, paymentHash)
			}
		} else if n.logger != nil {
			n.logger.Printf("notifications: payment upsert failed for %s: %v", paymentHash, err)
		}
		cancel()
	}

	for {
		select {
		case <-n.stop:
			return
		case <-time.After(paymentsPollInterval):
		}

		// Self-healing sweep: any rebalance/keysend/lightning notification
		// still sitting at IN_FLIGHT in the DB gets re-queued for the pending
		// poll. Catches races where the forward scan missed the IN_FLIGHT
		// transition (cursor already past it) and ensures stuck rows
		// reconcile within one poll cycle without needing a restart.
		stuckCtx, stuckCancel := context.WithTimeout(context.Background(), 5*time.Second)
		nowUnix := time.Now().Unix()
		stuckAdded := false
		for _, stuck := range n.loadStuckInFlightPayments(stuckCtx) {
			entry, ok := pending[stuck.Hash]
			if !ok {
				pending[stuck.Hash] = pendingPaymentEntry{Hash: stuck.Hash, LastSeen: nowUnix, PaymentIndex: stuck.PaymentIndex}
				stuckAdded = true
				continue
			}
			if stuck.PaymentIndex > 0 && entry.PaymentIndex == 0 {
				entry.PaymentIndex = stuck.PaymentIndex
				pending[stuck.Hash] = entry
				stuckAdded = true
			}
		}
		stuckCancel()
		if stuckAdded {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 5*time.Second)
			n.storePendingPayments(persistCtx, pending)
			persistCancel()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cursorVal, _ := n.getCursor(ctx, "payments_index")
		cancel()
		if strings.TrimSpace(cursorVal) == "" && !n.paymentsCatchupMode.Load() {
			n.paymentsCatchupMode.Store(true)
		}

		var indexOffset uint64
		if cursorVal != "" {
			if parsed, err := strconv.ParseUint(cursorVal, 10, 64); err == nil {
				indexOffset = parsed
			}
		}

		conn, release, err := n.lnd.BorrowLightning(context.Background(), false)
		if err != nil {
			n.logger.Printf("notifications: payments poll dial failed: %v", err)
			continue
		}

		client := lnrpc.NewLightningClient(conn)
		res, err := client.ListPayments(context.Background(), &lnrpc.ListPaymentsRequest{
			IncludeIncomplete: true,
			IndexOffset:       indexOffset,
			MaxPayments:       paymentsPollPageSize,
			Reversed:          false,
		})
		release()
		if err != nil {
			n.logger.Printf("notifications: payments poll failed: %v", err)
			continue
		}

		maxIndex := indexOffset
		pendingDirty := false
		now := time.Now().Unix()
		suppressMirror := n.paymentsCatchupMode.Load()
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
				if observePendingPayment(pending, paymentHash, pay.PaymentIndex, now) {
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
			processPayment(pay, suppressMirror)
			if _, ok := pending[paymentHash]; ok {
				delete(pending, paymentHash)
				pendingDirty = true
			}
		}

		if len(pending) > 0 {
			cutoff := time.Now().Add(-paymentsPendingMaxAge).Unix()
			for hash, entry := range pending {
				if strings.TrimSpace(entry.Hash) == "" {
					entry.Hash = hash
				}
				normalized, ok := normalizePendingPaymentEntry(entry, now)
				if !ok {
					delete(pending, hash)
					pendingDirty = true
					continue
				}
				if normalized.Hash != hash {
					delete(pending, hash)
					pending[normalized.Hash] = normalized
					hash = normalized.Hash
					pendingDirty = true
				}
				if normalized.LastSeen < cutoff {
					delete(pending, hash)
					pendingDirty = true
					continue
				}
				if normalized.NextCheck > now {
					continue
				}
				ctxLookup, cancelLookup := context.WithTimeout(context.Background(), 6*time.Second)
				pay, err := n.lookupPendingPayment(ctxLookup, normalized)
				cancelLookup()
				if err != nil || pay == nil {
					normalized = markPendingPaymentChecked(normalized, now, false)
					pending[hash] = normalized
					pendingDirty = true
					continue
				}
				status := pay.Status.String()
				if status == "IN_FLIGHT" {
					if pay.PaymentIndex > 0 {
						normalized.PaymentIndex = pay.PaymentIndex
					}
					normalized = markPendingPaymentChecked(normalized, now, true)
					pending[hash] = normalized
					pendingDirty = true
					continue
				}
				if status != "SUCCEEDED" && isProbePayment(pay) {
					delete(pending, hash)
					pendingDirty = true
					continue
				}
				processPayment(pay, n.paymentsCatchupMode.Load())
				delete(pending, hash)
				pendingDirty = true
			}
		}
		if n.paymentsCatchupMode.Load() && !shouldContinuePaymentsCatchup(len(res.Payments)) {
			n.paymentsCatchupMode.Store(false)
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

		conn, release, err := n.lnd.BorrowLightning(context.Background(), true)
		if err != nil {
			n.logger.Printf("notifications: transaction stream dial failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		client := lnrpc.NewLightningClient(conn)
		stream, err := client.SubscribeTransactions(context.Background(), &lnrpc.GetTransactionsRequest{})
		if err != nil {
			n.logger.Printf("notifications: transaction stream subscribe failed: %v", err)
			release()
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			tx, err := stream.Recv()
			if err != nil {
				n.logger.Printf("notifications: transaction stream ended: %v", err)
				release()
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
				Type:       "onchain",
				Action:     action,
				Direction:  direction,
				Status:     status,
				AmountSat:  amount,
				FeeSat:     tx.TotalFees,
				Txid:       tx.TxHash,
			}

			if direction == "in" && status == "CONFIRMED" {
				txid := strings.TrimSpace(tx.TxHash)
				if txid != "" {
					n.triggerTelegramBackup("onchain_receive_confirmed", txid, "")
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

		conn, release, err := n.lnd.BorrowLightning(context.Background(), true)
		if err != nil {
			n.logger.Printf("notifications: channel stream dial failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		client := lnrpc.NewLightningClient(conn)
		stream, err := client.SubscribeChannelEvents(context.Background(), &lnrpc.ChannelEventSubscription{})
		if err != nil {
			n.logger.Printf("notifications: channel stream subscribe failed: %v", err)
			release()
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			update, err := stream.Recv()
			if err != nil {
				n.logger.Printf("notifications: channel stream ended: %v", err)
				release()
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
			peerAlias := strings.TrimSpace(item.PeerAlias)
			if peerAlias == "" && strings.TrimSpace(item.RemotePubkey) != "" {
				peerAlias = n.lookupNodeAlias(item.RemotePubkey)
			}
			n.triggerTelegramBackup(reason, channelPoint, peerAlias)
			if item.Status == "waiting_close" && strings.TrimSpace(item.ClosingTxid) == "" && n.markWaitingCloseRecoveryAttempt(channelPoint) {
				recoverCtx, recoverCancel := context.WithTimeout(context.Background(), 8*time.Second)
				recoveredTxid, attempted, recoverErr := n.lnd.RecoverWaitingCloseTx(recoverCtx, channelPoint)
				recoverCancel()
				if recoverErr != nil {
					n.updateWaitingCloseRecoveryResult(channelPoint, "recover_failed", recoverErr.Error(), "")
					n.logger.Printf("notifications: waiting close recovery failed for %s: %v", channelPoint, recoverErr)
				} else if strings.TrimSpace(recoveredTxid) != "" {
					item.ClosingTxid = strings.TrimSpace(recoveredTxid)
					result := "closing_txid_detected"
					if attempted {
						result = "rebroadcast_ok"
						n.logger.Printf("notifications: waiting close recovery broadcasted for %s (%s)", channelPoint, recoveredTxid)
					}
					n.updateWaitingCloseRecoveryResult(channelPoint, result, "", recoveredTxid)
				} else if attempted {
					n.updateWaitingCloseRecoveryResult(channelPoint, "recovery_submitted_no_txid", "", "")
				} else {
					n.updateWaitingCloseRecoveryResult(channelPoint, "no_raw_tx_available", "", "")
				}
			}
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
	closingTxid := strings.TrimSpace(item.ClosingTxid)
	dedupeKey := fmt.Sprintf("%s|%s|%s", eventKey, status, strings.ToLower(closingTxid))
	if !n.markPendingNotification(dedupeKey) {
		return
	}

	evt := Notification{
		OccurredAt:   time.Now().UTC(),
		Type:         "channel",
		Action:       "closing",
		Direction:    "neutral",
		Status:       status,
		AmountSat:    amount,
		PeerPubkey:   item.RemotePubkey,
		PeerAlias:    item.PeerAlias,
		ChannelPoint: item.ChannelPoint,
		Txid:         closingTxid,
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

func (n *Notifier) markWaitingCloseRecoveryAttempt(channelPoint string) bool {
	key := normalizeChannelPointKey(channelPoint)
	if key == "" {
		return false
	}

	n.pendingMu.Lock()
	defer n.pendingMu.Unlock()

	now := time.Now().UTC()
	state := n.waitingCloseRecoveries[key]
	if !state.LastAttemptAt.IsZero() {
		if now.Sub(state.LastAttemptAt) < waitingCloseRecoveryRetryInterval {
			return false
		}
	}
	state.Attempts++
	state.LastAttemptAt = now
	n.waitingCloseRecoveries[key] = state
	return true
}

func (n *Notifier) updateWaitingCloseRecoveryResult(channelPoint string, result string, errMsg string, recoveredTxid string) {
	key := normalizeChannelPointKey(channelPoint)
	if key == "" {
		return
	}

	n.pendingMu.Lock()
	defer n.pendingMu.Unlock()

	state := n.waitingCloseRecoveries[key]
	state.LastResult = strings.TrimSpace(result)
	state.LastError = strings.TrimSpace(errMsg)
	state.LastRecoveredTxid = strings.TrimSpace(recoveredTxid)
	n.waitingCloseRecoveries[key] = state
}

func (n *Notifier) getWaitingCloseRecoveryInfo(channelPoint string) (waitingCloseRecoveryInfo, bool) {
	key := normalizeChannelPointKey(channelPoint)
	if key == "" {
		return waitingCloseRecoveryInfo{}, false
	}

	n.pendingMu.Lock()
	defer n.pendingMu.Unlock()

	info, ok := n.waitingCloseRecoveries[key]
	if !ok {
		return waitingCloseRecoveryInfo{}, false
	}
	return info, true
}

func normalizeChannelPointKey(channelPoint string) string {
	return strings.ToLower(strings.TrimSpace(channelPoint))
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
			OccurredAt:   now,
			Type:         "channel",
			Action:       "open",
			Direction:    "neutral",
			Status:       "OPENED",
			AmountSat:    ch.Capacity,
			PeerPubkey:   ch.RemotePubkey,
			PeerAlias:    ch.PeerAlias,
			ChannelID:    int64(ch.ChanId),
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
			OccurredAt:   now,
			Type:         "channel",
			Action:       "close",
			Direction:    "neutral",
			Status:       "CLOSED",
			AmountSat:    ch.SettledBalance,
			PeerPubkey:   ch.RemotePubkey,
			ChannelID:    int64(ch.ChanId),
			ChannelPoint: ch.ChannelPoint,
			Txid:         ch.ClosingTxHash,
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
			OccurredAt:   now,
			Type:         "channel",
			Action:       "opening",
			Direction:    "neutral",
			Status:       "PENDING",
			AmountSat:    0,
			ChannelPoint: channelPoint,
			Txid:         txid,
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if alias := n.lookupGraphNodeAlias(ctx, trimmed); alias != "" {
		return alias
	}
	return shortPubKey(trimmed)
}

func (n *Notifier) lookupGraphNodeAlias(ctx context.Context, pubkey string) string {
	if n == nil || n.db == nil {
		return ""
	}
	normalized := graphExplorerNormalizePubkey(pubkey)
	if normalized == "" {
		return ""
	}

	var alias string
	if err := n.db.QueryRow(ctx, `
select coalesce(alias, '')
from graph_nodes
where pubkey = $1
`, normalized).Scan(&alias); err != nil {
		return ""
	}
	return strings.TrimSpace(alias)
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

		channelMap := n.channelMap(context.Background())

		conn, release, err := n.lnd.BorrowLightning(context.Background(), false)
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
				StartTime:       after,
				EndTime:         endTime,
				IndexOffset:     indexOffset,
				NumMaxEvents:    200,
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
				if feeMsat == 0 && fee != 0 {
					feeMsat = fee * 1000
				}
				amtInMsat := int64(fwd.AmtInMsat)
				amtOutMsat := int64(fwd.AmtOutMsat)
				if amtInMsat == 0 && fwd.AmtIn != 0 {
					amtInMsat = int64(fwd.AmtIn) * 1000
				}
				if amtOutMsat == 0 && fwd.AmtOut != 0 {
					amtOutMsat = int64(fwd.AmtOut) * 1000
				}
				if feeMsat == 0 && amtInMsat > 0 && amtOutMsat > 0 && amtInMsat > amtOutMsat {
					feeMsat = amtInMsat - amtOutMsat
				}
				inInfo, _ := channelMap[uint64(fwd.ChanIdIn)]
				outInfo, _ := channelMap[uint64(fwd.ChanIdOut)]
				evt := Notification{
					OccurredAt:      occurredAt,
					Type:            "forward",
					Action:          "forwarded",
					Direction:       "neutral",
					Status:          "SETTLED",
					AmountSat:       amount,
					FeeSat:          fee,
					FeeMsat:         feeMsat,
					PeerAlias:       strings.TrimSpace(fmt.Sprintf("%s -> %s", fwd.PeerAliasIn, fwd.PeerAliasOut)),
					ChannelID:       int64(fwd.ChanIdOut),
					ChanIDIn:        int64(fwd.ChanIdIn),
					ChanIDOut:       int64(fwd.ChanIdOut),
					AmountInMsat:    amtInMsat,
					AmountOutMsat:   amtOutMsat,
					PeerPubkeyIn:    inInfo.RemotePubkey,
					PeerPubkeyOut:   outInfo.RemotePubkey,
					ChannelPointIn:  inInfo.ChannelPoint,
					ChannelPointOut: outInfo.ChannelPoint,
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
		release()

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

func paymentRouteType(pay *lnrpc.Payment, isKeysend bool, isRebalance bool) string {
	if isRebalance {
		return "rebalance"
	}
	if isKeysend {
		return "keysend"
	}
	if isProbePayment(pay) {
		return "probe"
	}
	return "lightning"
}

func routeTotalAmtMsat(route *lnrpc.Route) int64 {
	if route == nil {
		return 0
	}
	if route.TotalAmtMsat != 0 {
		return route.TotalAmtMsat
	}
	if route.TotalAmt != 0 {
		return route.TotalAmt * 1000
	}
	return 0
}

func routeTotalFeeMsat(route *lnrpc.Route) int64 {
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

func hopAmtToForwardMsat(hop *lnrpc.Hop) int64 {
	if hop == nil {
		return 0
	}
	if hop.AmtToForwardMsat != 0 {
		return hop.AmtToForwardMsat
	}
	if hop.AmtToForward != 0 {
		return hop.AmtToForward * 1000
	}
	return 0
}

func hopFeeMsat(hop *lnrpc.Hop) int64 {
	if hop == nil {
		return 0
	}
	if hop.FeeMsat != 0 {
		return hop.FeeMsat
	}
	if hop.Fee != 0 {
		return hop.Fee * 1000
	}
	return 0
}

func unixNanoToTime(ns int64) time.Time {
	if ns <= 0 {
		return time.Time{}
	}
	return time.Unix(0, ns).UTC()
}

func buildPaymentRouteHistory(pay *lnrpc.Payment, paymentType string, paymentCreatedAt time.Time) ([]paymentRouteAttemptRecord, []paymentRouteHopRecord) {
	if pay == nil {
		return nil, nil
	}

	paymentHash := normalizeHash(pay.PaymentHash)
	if paymentHash == "" {
		return nil, nil
	}

	paymentStatus := strings.TrimSpace(pay.Status.String())
	attempts := make([]paymentRouteAttemptRecord, 0, len(pay.Htlcs))
	hops := make([]paymentRouteHopRecord, 0)
	for _, attempt := range pay.Htlcs {
		if attempt == nil {
			continue
		}

		route := attempt.GetRoute()
		routeHops := route.GetHops()
		attemptRecord := paymentRouteAttemptRecord{
			PaymentHash:       paymentHash,
			PaymentIndex:      int64(pay.PaymentIndex),
			PaymentType:       paymentType,
			PaymentStatus:     paymentStatus,
			PaymentCreatedAt:  paymentCreatedAt,
			AttemptID:         int64(attempt.AttemptId),
			AttemptStatus:     strings.TrimSpace(attempt.Status.String()),
			AttemptStartedAt:  unixNanoToTime(attempt.AttemptTimeNs),
			AttemptResolvedAt: unixNanoToTime(attempt.ResolveTimeNs),
			TotalAmtMsat:      routeTotalAmtMsat(route),
			TotalFeeMsat:      routeTotalFeeMsat(route),
			TotalTimeLock:     int64(route.GetTotalTimeLock()),
			HopCount:          len(routeHops),
		}
		if failure := attempt.GetFailure(); failure != nil {
			attemptRecord.FailureCode = strings.TrimSpace(failure.Code.String())
			attemptRecord.FailureSourceIndex = int64(failure.FailureSourceIndex)
		}
		attempts = append(attempts, attemptRecord)

		costToMsat := int64(0)
		for hopIdx, hop := range routeHops {
			if hop == nil {
				continue
			}
			hops = append(hops, paymentRouteHopRecord{
				PaymentHash:      paymentHash,
				AttemptID:        int64(attempt.AttemptId),
				HopIndex:         hopIdx + 1,
				NodePubkey:       strings.TrimSpace(hop.PubKey),
				ChannelID:        int64(hop.ChanId),
				ChannelCapacity:  hop.ChanCapacity,
				AmtToForwardMsat: hopAmtToForwardMsat(hop),
				FeeMsat:          hopFeeMsat(hop),
				Expiry:           int64(hop.Expiry),
				CostToMsat:       costToMsat,
				IsFirstHop:       hopIdx == 0,
				IsFinalHop:       hopIdx == len(routeHops)-1,
			})
			costToMsat += hopFeeMsat(hop)
		}
	}

	return attempts, hops
}

func (n *Notifier) replacePaymentRouteHistory(ctx context.Context, pay *lnrpc.Payment, paymentType string, paymentCreatedAt time.Time) error {
	if n == nil || n.db == nil || pay == nil {
		return nil
	}

	paymentHash := normalizeHash(pay.PaymentHash)
	if paymentHash == "" {
		return nil
	}

	attempts, hops := buildPaymentRouteHistory(pay, paymentType, paymentCreatedAt)
	tx, err := n.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
delete from payment_route_attempts
where payment_hash=$1
`, paymentHash); err != nil {
		return err
	}

	for _, attempt := range attempts {
		_, err := tx.Exec(ctx, `
insert into payment_route_attempts (
  payment_hash, payment_index, payment_type, payment_status, payment_created_at,
  attempt_id, attempt_status, attempt_started_at, attempt_resolved_at,
  failure_code, failure_source_index, total_amt_msat, total_fee_msat, total_time_lock, hop_count
) values (
  $1,$2,$3,$4,$5,
  $6,$7,$8,$9,
  $10,$11,$12,$13,$14,$15
)
`, attempt.PaymentHash, attempt.PaymentIndex, attempt.PaymentType, attempt.PaymentStatus, attempt.PaymentCreatedAt,
			attempt.AttemptID, attempt.AttemptStatus, nullableTime(attempt.AttemptStartedAt), nullableTime(attempt.AttemptResolvedAt),
			nullableString(attempt.FailureCode), nullableInt(attempt.FailureSourceIndex), attempt.TotalAmtMsat, attempt.TotalFeeMsat, attempt.TotalTimeLock, attempt.HopCount)
		if err != nil {
			return err
		}
	}

	for _, hop := range hops {
		_, err := tx.Exec(ctx, `
insert into payment_route_hops (
  payment_hash, attempt_id, hop_index, node_pubkey, node_alias, channel_id,
  channel_capacity_sat, amt_to_forward_msat, fee_msat, expiry, cost_to_msat,
  is_first_hop, is_final_hop
) values (
  $1,$2,$3,$4,$5,$6,
  $7,$8,$9,$10,$11,
  $12,$13
)
`, hop.PaymentHash, hop.AttemptID, hop.HopIndex, nullableString(hop.NodePubkey), nullableString(hop.NodeAlias), nullableInt(hop.ChannelID),
			hop.ChannelCapacity, hop.AmtToForwardMsat, hop.FeeMsat, hop.Expiry, hop.CostToMsat,
			hop.IsFirstHop, hop.IsFinalHop)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (n *Notifier) updatePaymentRouteHistoryType(ctx context.Context, paymentHash string, paymentType string) error {
	if n == nil || n.db == nil {
		return nil
	}

	normalized := normalizeHash(paymentHash)
	if normalized == "" {
		return nil
	}

	_, err := n.db.Exec(ctx, `
update payment_route_attempts
set payment_type=$2
where payment_hash=$1
`, normalized, strings.TrimSpace(paymentType))
	return err
}

func (n *Notifier) lookupPendingPayment(ctx context.Context, entry pendingPaymentEntry) (*lnrpc.Payment, error) {
	normalized := normalizeHash(entry.Hash)
	if normalized == "" {
		return nil, nil
	}
	if entry.PaymentIndex > 0 {
		if pay, ok, err := n.lookupPaymentByIndex(ctx, normalized, entry.PaymentIndex); err != nil {
			return nil, err
		} else if ok {
			return pay, nil
		}
	}
	return n.lookupPaymentByHash(ctx, normalized)
}

func (n *Notifier) lookupPaymentByIndex(ctx context.Context, paymentHash string, paymentIndex uint64) (*lnrpc.Payment, bool, error) {
	normalized := normalizeHash(paymentHash)
	if normalized == "" || paymentIndex == 0 {
		return nil, false, nil
	}
	if n == nil || n.lnd == nil {
		return nil, false, errors.New("lnd unavailable")
	}

	conn, release, err := n.lnd.BorrowLightning(ctx, false)
	if err != nil {
		return nil, false, err
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)
	res, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true,
		IndexOffset:       paymentIndex - 1,
		MaxPayments:       1,
		Reversed:          false,
	})
	if err != nil {
		return nil, false, err
	}

	for _, pay := range res.GetPayments() {
		if pay == nil {
			continue
		}
		if normalizeHash(pay.PaymentHash) == normalized {
			return pay, true, nil
		}
	}
	return nil, false, nil
}

func (n *Notifier) lookupPaymentByHash(ctx context.Context, paymentHash string) (*lnrpc.Payment, error) {
	normalized := normalizeHash(paymentHash)
	if normalized == "" {
		return nil, nil
	}
	if n == nil || n.lnd == nil {
		return nil, errors.New("lnd unavailable")
	}

	// Fallback path: TrackPaymentV2 resolves by hash without depending on
	// ListPayments pagination. The pending loop prefers payment_index first so
	// this stream is not opened on every poll for every pending payment.
	if pay, err := n.lnd.TrackPaymentSnapshot(ctx, normalized); err == nil {
		return pay, nil
	} else if n.logger != nil {
		n.logger.Printf("notifications: TrackPaymentV2 lookup failed for %s, falling back to ListPayments: %v", normalized, err)
	}

	conn, release, err := n.lnd.BorrowLightning(ctx, false)
	if err != nil {
		return nil, err
	}
	defer release()

	client := lnrpc.NewLightningClient(conn)
	res, err := client.ListPayments(ctx, &lnrpc.ListPaymentsRequest{
		IncludeIncomplete: true,
		MaxPayments:       400,
		Reversed:          true,
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
	return n.lnd.LookupPayment(ctx, normalized, paymentsPendingMaxAge)
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
		PeerLabel:    peerLabel,
		ChannelLabel: channelLabel,
	}
}

func (n *Notifier) rebalanceEvent(ctx context.Context, pay *lnrpc.Payment, occurredAt time.Time) Notification {
	paymentHash := ""
	status := "SETTLED"
	if pay != nil {
		paymentHash = normalizeHash(pay.PaymentHash)
		normalized := strings.ToUpper(strings.TrimSpace(pay.Status.String()))
		switch normalized {
		case "SUCCEEDED":
			status = "SETTLED"
		case "FAILED":
			status = "FAILED"
		case "IN_FLIGHT":
			status = "IN_FLIGHT"
		default:
			if normalized != "" {
				status = normalized
			}
		}
	}
	evt := Notification{
		OccurredAt:  occurredAt,
		Type:        "rebalance",
		Action:      "rebalanced",
		Direction:   "neutral",
		Status:      status,
		AmountSat:   0,
		FeeSat:      0,
		FeeMsat:     0,
		PaymentHash: paymentHash,
	}
	if pay != nil {
		evt.AmountSat = pay.ValueSat
		feeMsat := paymentFeeMsat(pay)
		evt.FeeMsat = feeMsat
		evt.FeeSat = feeMsat / 1000
	}
	if pay != nil {
		route := rebalanceRouteFromPayment(pay)
		if route != nil && len(route.Hops) > 0 {
			channelMap := n.channelMap(ctx)
			outHop := route.Hops[0]
			inHop := route.Hops[len(route.Hops)-1]
			evt.RebalSourceChanID = int64(outHop.ChanId)
			evt.RebalTargetChanID = int64(inHop.ChanId)
			if info, ok := channelMap[outHop.ChanId]; ok {
				evt.RebalSourcePoint = info.ChannelPoint
				evt.RebalSourcePubkey = info.RemotePubkey
			}
			if info, ok := channelMap[inHop.ChanId]; ok {
				evt.RebalTargetPoint = info.ChannelPoint
				evt.RebalTargetPubkey = info.RemotePubkey
			}
		}
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
	_, _, _, peerPubkey, peerAlias := n.invoiceReceiveChannelInfo(ctx, invoice)
	return peerPubkey, peerAlias
}

func (n *Notifier) invoiceReceiveChannel(ctx context.Context, invoice *lnrpc.Invoice) (int64, string, string) {
	chanID, channelPoint, channelAlias, _, _ := n.invoiceReceiveChannelInfo(ctx, invoice)
	return chanID, channelPoint, channelAlias
}

func (n *Notifier) invoiceReceiveChannelInfo(ctx context.Context, invoice *lnrpc.Invoice) (int64, string, string, string, string) {
	if n == nil || invoice == nil {
		return 0, "", "", "", ""
	}
	if len(invoice.Htlcs) == 0 {
		return 0, "", "", "", ""
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
		peerAlias := strings.TrimSpace(info.PeerAlias)
		return int64(htlc.ChanId), info.ChannelPoint, pickAlias(peerAlias, info.RemotePubkey, ""), info.RemotePubkey, peerAlias
	}
	return 0, "", "", "", ""
}

func (n *Notifier) paymentSendChannel(ctx context.Context, pay *lnrpc.Payment) (int64, string, string) {
	if n == nil || pay == nil {
		return 0, "", ""
	}
	route := rebalanceRouteFromPayment(pay)
	if route == nil || len(route.Hops) == 0 {
		return 0, "", ""
	}
	firstHop := route.Hops[0]
	if firstHop.ChanId == 0 {
		return 0, "", pickAlias("", "", firstHop.PubKey)
	}
	channelMap := n.channelMap(ctx)
	info, ok := channelMap[firstHop.ChanId]
	if !ok {
		return int64(firstHop.ChanId), "", pickAlias("", "", firstHop.PubKey)
	}
	return int64(firstHop.ChanId), info.ChannelPoint, pickAlias(info.PeerAlias, info.RemotePubkey, firstHop.PubKey)
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

func isManagedRebalanceMemo(memo string) bool {
	normalized := strings.ToLower(strings.TrimSpace(memo))
	if !strings.HasPrefix(normalized, "rebalance:") {
		return false
	}
	parts := strings.Split(normalized, ":")
	if len(parts) != 4 {
		return false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return false
	}
	if _, err := strconv.ParseUint(parts[2], 10, 64); err != nil {
		return false
	}
	if _, err := strconv.ParseUint(parts[3], 10, 64); err != nil {
		return false
	}
	return true
}

func shouldSuppressExternalFailedRebalance(status string, memo string) bool {
	if strings.EqualFold(strings.TrimSpace(status), "SUCCEEDED") {
		return false
	}
	return !isManagedRebalanceMemo(memo)
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
	var channelAlias pgtype.Text
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
		&channelAlias,
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
	if channelAlias.Valid {
		evt.ChannelAlias = channelAlias.String
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

func scanNotificationWithInserted(scanner notificationRowScanner) (Notification, bool, error) {
	var evt Notification
	var inserted bool
	var peerPubkey pgtype.Text
	var peerAlias pgtype.Text
	var channelPoint pgtype.Text
	var channelAlias pgtype.Text
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
		&channelAlias,
		&txid,
		&paymentHash,
		&memo,
		&inserted,
	)
	if err != nil {
		return Notification{}, false, err
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
	if channelAlias.Valid {
		evt.ChannelAlias = channelAlias.String
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
	return evt, inserted, nil
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
