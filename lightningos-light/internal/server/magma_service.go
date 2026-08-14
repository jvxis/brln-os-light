package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	magmaModeMonitor = "monitor"
	// magmaModeAssisted enables the per-order buttons. Every action still needs an
	// explicit click; nothing here acts on its own.
	magmaModeAssisted = "assisted"
	// magmaModeAuto lets the policy engine accept, reject and fund on its own.
	magmaModeAuto = "auto"

	// magmaPaymentSuccessful is the Amboss payment status that means the buyer
	// paid and the revenue is ours.
	magmaPaymentSuccessful = "SUCCESSFUL_PAYMENT"

	magmaDefaultPollInterval = 90 * time.Second
	// magmaCycleTimeout bounds one poll cycle. Generous enough for a real cycle
	// (two Amboss calls plus LND work), short enough that a stall self-heals
	// instead of parking the poller for good.
	magmaCycleTimeout    = 4 * time.Minute
	magmaMinPollInterval = 30
	magmaMaxPollInterval = 3600

	// magmaTokenExpiryWarnDays is when we start warning. Phase 1 only reports it;
	// later phases must refuse to open a channel inside this window, because a
	// token that dies between the channel open and sellerAddTransaction leaves
	// capital committed against an unconfirmed sale.
	magmaTokenExpiryWarnDays = 7
)

// magmaActionableStatuses are the states where the seller is the one holding up
// the order. These drive the alerts in monitor mode.
var magmaActionableStatuses = map[string]string{
	"WAITING_FOR_SELLER_APPROVAL": "waiting_seller_approval",
	"WAITING_FOR_CHANNEL_OPEN":    "waiting_channel_open",
}

// magmaSellerFailureStatuses are recorded failures against the seller. They are
// not cosmetic: Amboss keeps them on the account.
var magmaSellerFailureStatuses = map[string]bool{
	"SELLER_FAILED_TO_REACT":        true,
	"SELLER_FAILED_TO_OPEN_CHANNEL": true,
	"SELLER_FAILED_TO_SEND_SWAP":    true,
	"INVALID_CHANNEL_OPENING":       true,
}

// MagmaSettings holds Phase 1 configuration. Mode is persisted but monitor is
// the only accepted value until the assisted/auto phases land.
type MagmaSettings struct {
	Installed       bool   `json:"installed"`
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"mode"`
	PollIntervalSec int    `json:"poll_interval_sec"`
	NotifyTelegram  bool   `json:"notify_telegram"`
}

// MagmaTokenState reports credential health without ever exposing the token.
type MagmaTokenState struct {
	Configured   bool       `json:"configured"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	DaysToExpiry *int       `json:"days_to_expiry,omitempty"`
	Expired      bool       `json:"expired"`
	ExpiringSoon bool       `json:"expiring_soon"`
}

// MagmaOverview is the payload behind the app page.
type MagmaOverview struct {
	Settings      MagmaSettings       `json:"settings"`
	Token         MagmaTokenState     `json:"token"`
	Market        *MagmaMarketSummary `json:"market,omitempty"`
	Orders        []MagmaOrder        `json:"orders"`
	ActionNeeded  []MagmaOrder        `json:"action_needed"`
	Capacity      *MagmaCapacity      `json:"capacity,omitempty"`
	TokenWarning  string              `json:"token_warning,omitempty"`
	Policy        *MagmaPolicy        `json:"policy,omitempty"`
	PolicySummary string              `json:"policy_summary,omitempty"`
	// PolicyWarnings are self-inflicted contradictions: settings that are each
	// valid alone but together refuse orders the operator meant to take.
	PolicyWarnings []string          `json:"policy_warnings,omitempty"`
	PnL            *MagmaPnL         `json:"pnl,omitempty"`
	LastSyncAt     *time.Time        `json:"last_sync_at,omitempty"`
	LastSyncError  string            `json:"last_sync_error,omitempty"`
	Poller         MagmaPollerHealth `json:"poller"`
}

// MagmaPollerHealth makes a dead worker visible. Without it the only symptom is
// an empty last sync, which reads as "quiet" rather than "broken".
type MagmaPollerHealth struct {
	Started          bool       `json:"started"`
	LastTickAt       *time.Time `json:"last_tick_at,omitempty"`
	LastTickNote     string     `json:"last_tick_note,omitempty"`
	ConsecutiveFails int        `json:"consecutive_failures"`
}

// MagmaOrderEvent is the append-only audit trail per order.
type MagmaOrderEvent struct {
	ID        int64           `json:"id"`
	OrderID   string          `json:"order_id"`
	Kind      string          `json:"kind"`
	Level     string          `json:"level"`
	Message   string          `json:"message"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type MagmaService struct {
	db     *pgxpool.Pool
	lnd    magmaLND
	amboss *magmaAmbossClient
	logger *log.Logger
	wake   chan struct{}
	start  sync.Once
	workMu sync.Mutex

	stateMu       sync.RWMutex
	lastSyncAt    time.Time
	lastSyncError string
	lastMarket    *MagmaMarketSummary
	// Gate for the expensive order listing. In memory on purpose: after a restart
	// the baseline is gone and the next tick fetches, which is the safe default.
	fetchBaseline     bool
	lastPendingOrders int64
	lastOrderFetchAt  time.Time
	// Poller health. lastTickAt moves on every cycle, including skipped and
	// failed ones, so "the worker is alive" is observable separately from
	// "the last sync succeeded".
	pollerStarted    bool
	lastTickAt       time.Time
	lastTickNote     string
	consecutiveFails int
}

func NewMagmaService(db *pgxpool.Pool, lnd magmaLND, logger *log.Logger) *MagmaService {
	return &MagmaService{
		db:     db,
		lnd:    lnd,
		amboss: newMagmaAmbossClient(),
		logger: logger,
		wake:   make(chan struct{}, 1),
	}
}

func (s *MagmaService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("Magma database unavailable")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists magma_settings (
  id smallint primary key check (id = 1),
  app_installed boolean not null default false,
  app_enabled boolean not null default false,
  mode text not null default 'monitor',
  poll_interval_sec integer not null default 90,
  notify_telegram boolean not null default true,
  updated_at timestamptz not null default now()
);
alter table magma_settings
  add column if not exists min_revenue_sat bigint not null default 5000,
  add column if not exists min_price_ppm bigint not null default 2000,
  add column if not exists min_price_ppm_per_day bigint not null default 25,
  add column if not exists min_fee_rate_cap_ppm bigint not null default 100,
  add column if not exists min_channel_size_sat bigint not null default 1000000,
  add column if not exists max_channel_size_sat bigint not null default 10000000,
  add column if not exists max_commitment_days bigint not null default 180,
  add column if not exists max_sat_per_vbyte bigint not null default 50,
  add column if not exists max_onchain_cost_pct bigint not null default 50,
  add column if not exists min_onchain_reserve_sat bigint not null default 100000,
  add column if not exists max_concurrent_opens integer not null default 2,
  add column if not exists max_daily_orders integer not null default 5,
  add column if not exists max_daily_size_sat bigint not null default 20000000,
  add column if not exists auto_reject_declined boolean not null default true;
insert into magma_settings (id) values (1) on conflict (id) do nothing;

create table if not exists magma_orders (
  order_id text primary key,
  buyer_pubkey text not null default '',
  offer_id text not null default '',
  size_sat bigint not null default 0,
  revenue_sat bigint not null default 0,
  buyer_pays_sat bigint not null default 0,
  amboss_fee_ppm bigint not null default 0,
  price_fixed_sat bigint not null default 0,
  price_variable_sat bigint not null default 0,
  price_ppm bigint not null default 0,
  fee_rate_cap_ppm bigint not null default 0,
  base_fee_cap_sat bigint not null default 0,
  commitment_blocks bigint not null default 0,
  blocks_until_can_be_closed bigint,
  closed_blocks_before_min bigint,
  fee_above_cap_seconds bigint,
  magma_status text not null default '',
  payment_status text not null default '',
  payment_hash text not null default '',
  channel_scid text not null default '',
  channel_point text not null default '',
  cancellation_reason text not null default '',
  seller_close_side text not null default '',
  buyer_close_side text not null default '',
  is_automated boolean not null default false,
  chat_enabled boolean not null default false,
  local_state text not null default 'observed',
  notified_status text not null default '',
  order_created_at timestamptz,
  order_updated_at timestamptz,
  first_seen_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists magma_order_events (
  id bigserial primary key,
  order_id text not null references magma_orders(order_id) on delete cascade,
  kind text not null,
  level text not null default 'info',
  message text not null default '',
  metadata jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

alter table magma_orders
  add column if not exists invoice_payment_request text not null default '',
  add column if not exists invoice_hash text not null default '',
  add column if not exists funding_txid text not null default '',
  add column if not exists attempt_count integer not null default 0,
  add column if not exists last_error text not null default '',
  add column if not exists fee_guard_applied boolean not null default false,
  add column if not exists revenue_settled_at timestamptz,
  add column if not exists onchain_fee_sat bigint,
  add column if not exists buyer_alias text;

create table if not exists magma_offer_state (
  offer_id text primary key,
  auto_disabled_at timestamptz,
  available_sat bigint not null default 0,
  required_sat bigint not null default 0,
  updated_at timestamptz not null default now()
);

create index if not exists magma_orders_revenue_settled_idx
  on magma_orders (revenue_settled_at) where revenue_settled_at is not null;

create index if not exists magma_orders_status_idx on magma_orders (magma_status);
create index if not exists magma_orders_local_state_idx on magma_orders (local_state);
create index if not exists magma_orders_updated_idx on magma_orders (updated_at desc);
create index if not exists magma_order_events_order_idx on magma_order_events (order_id, id desc);
`)
	return err
}

func (s *MagmaService) AppState(ctx context.Context) (bool, bool, error) {
	var installed, enabled bool
	err := s.db.QueryRow(ctx, `select app_installed, app_enabled from magma_settings where id=1`).Scan(&installed, &enabled)
	return installed, enabled, err
}

func (s *MagmaService) SetAppInstalled(ctx context.Context, installed, enabled bool) error {
	if !installed {
		enabled = false
	}
	if _, err := s.db.Exec(ctx,
		`update magma_settings set app_installed=$1, app_enabled=$2, updated_at=now() where id=1`,
		installed, enabled,
	); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *MagmaService) SetAppEnabled(ctx context.Context, enabled bool) error {
	var installed bool
	if err := s.db.QueryRow(ctx, `select app_installed from magma_settings where id=1`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return errors.New("Magma Inbound Sales is not installed")
	}
	if _, err := s.db.Exec(ctx,
		`update magma_settings set app_enabled=$1, updated_at=now() where id=1`, enabled,
	); err != nil {
		return err
	}
	s.signal()
	return nil
}

func (s *MagmaService) Settings(ctx context.Context) (MagmaSettings, error) {
	var settings MagmaSettings
	err := s.db.QueryRow(ctx, `
select app_installed, app_enabled, mode, poll_interval_sec, notify_telegram
from magma_settings where id=1
`).Scan(&settings.Installed, &settings.Enabled, &settings.Mode, &settings.PollIntervalSec, &settings.NotifyTelegram)
	if err != nil {
		return MagmaSettings{}, err
	}
	return settings, nil
}

// MagmaSettingsUpdate carries only the fields Phase 1 lets the operator change.
type MagmaSettingsUpdate struct {
	Mode *string `json:"mode,omitempty"`
	// Enabled is the switch's Off position. It maps onto the app's existing
	// enabled flag rather than adding a fourth mode: two ways to be idle would
	// mean two things to check when the poller looks asleep.
	Enabled         *bool `json:"enabled,omitempty"`
	PollIntervalSec *int  `json:"poll_interval_sec,omitempty"`
	NotifyTelegram  *bool `json:"notify_telegram,omitempty"`
}

func (s *MagmaService) UpdateSettings(ctx context.Context, update MagmaSettingsUpdate) (MagmaSettings, error) {
	current, err := s.Settings(ctx)
	if err != nil {
		return MagmaSettings{}, err
	}
	if update.PollIntervalSec != nil {
		interval := *update.PollIntervalSec
		if interval < magmaMinPollInterval || interval > magmaMaxPollInterval {
			return MagmaSettings{}, fmt.Errorf("poll_interval_sec must be between %d and %d", magmaMinPollInterval, magmaMaxPollInterval)
		}
		current.PollIntervalSec = interval
	}
	if update.NotifyTelegram != nil {
		current.NotifyTelegram = *update.NotifyTelegram
	}
	if update.Mode != nil {
		mode := strings.TrimSpace(*update.Mode)
		if mode != magmaModeMonitor && mode != magmaModeAssisted && mode != magmaModeAuto {
			return MagmaSettings{}, fmt.Errorf("mode must be %q, %q or %q",
				magmaModeMonitor, magmaModeAssisted, magmaModeAuto)
		}
		current.Mode = mode
	}
	if _, err := s.db.Exec(ctx, `
update magma_settings set mode=$1, poll_interval_sec=$2, notify_telegram=$3, updated_at=now() where id=1
`, current.Mode, current.PollIntervalSec, current.NotifyTelegram); err != nil {
		return MagmaSettings{}, err
	}
	if update.Enabled != nil && *update.Enabled != current.Enabled {
		// Reuses the app lifecycle so the App Store card and this switch never
		// disagree about whether the app is running.
		if err := s.SetAppEnabled(ctx, *update.Enabled); err != nil {
			return MagmaSettings{}, err
		}
		current.Enabled = *update.Enabled
	}
	s.signal()
	return current, nil
}

// token reads the Amboss credential from the Autofee configuration, which is
// where this deployment already stores it. Read-only on purpose: the Fee Center
// stays the single place that writes it, so there is only one thing to rotate.
func (s *MagmaService) token(ctx context.Context) (string, error) {
	var raw *string
	err := s.db.QueryRow(ctx, `select amboss_token from autofee_config where id=$1`, autofeeConfigID).Scan(&raw)
	switch {
	case err == nil:
	case errors.Is(err, pgx.ErrNoRows):
		return "", nil
	case magmaIsUndefinedTable(err):
		// The Autofee schema is created by its own lazy init, which may not have
		// run yet on a fresh install. Absent table means "no token configured",
		// not a failure worth surfacing.
		return "", nil
	default:
		return "", err
	}
	if raw == nil {
		return "", nil
	}
	return strings.TrimSpace(*raw), nil
}

func magmaIsUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}

func (s *MagmaService) TokenState(ctx context.Context) MagmaTokenState {
	token, err := s.token(ctx)
	if err != nil || strings.TrimSpace(token) == "" {
		return MagmaTokenState{}
	}
	state := MagmaTokenState{Configured: true}
	expiry, ok := magmaTokenExpiry(token)
	if !ok {
		return state
	}
	state.ExpiresAt = &expiry
	remaining := int(time.Until(expiry).Hours() / 24)
	state.DaysToExpiry = &remaining
	state.Expired = !time.Now().Before(expiry)
	state.ExpiringSoon = !state.Expired && remaining <= magmaTokenExpiryWarnDays
	return state
}

func (s *MagmaService) Start(ctx context.Context) {
	if s == nil {
		return
	}
	// Logged because a poller that never started looks exactly like a healthy
	// one: the API answers, the settings read fine, and nothing errors. The only
	// symptom is that last_sync_at stays empty.
	s.start.Do(func() {
		s.stateMu.Lock()
		s.pollerStarted = true
		s.stateMu.Unlock()
		if s.logger != nil {
			s.logger.Printf("magma: poller started")
		}
		go s.run(ctx)
	})
}

func (s *MagmaService) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *MagmaService) run(ctx context.Context) {
	timer := time.NewTimer(magmaDefaultPollInterval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		interval := s.pollInterval(ctx)
		s.tick(ctx)
		timer.Reset(interval)
	}
}

// tick runs one poll cycle so that no single bad cycle can stop the poller.
//
// Three things have already gone wrong here in production, and each is guarded:
// the worker never started, it deadlocked on its own mutex, and a silent early
// return left no trace. A dead poller is the worst failure this app has, because
// everything else keeps looking healthy while orders quietly expire.
func (s *MagmaService) tick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			// A panic in this goroutine would otherwise take the whole manager
			// down, or at best kill the poller for good.
			s.noteTick(fmt.Sprintf("panic: %v", r))
			if s.logger != nil {
				s.logger.Printf("magma: poller recovered from panic: %v", r)
			}
		}
	}()

	// Never block on the mutex. If an operator action is mid-flight, skipping
	// this cycle costs 90 seconds; queueing behind a stuck holder costs every
	// cycle from here on.
	if !s.workMu.TryLock() {
		s.noteTick("skipped: another operation is in progress")
		return
	}
	defer s.workMu.Unlock()

	// Bounds a cycle that stalls inside a slow external call.
	cycleCtx, cancel := context.WithTimeout(ctx, magmaCycleTimeout)
	defer cancel()

	err := s.syncLocked(cycleCtx)
	switch {
	case err == nil:
		s.noteTick("")
	case errors.Is(err, context.Canceled):
		s.noteTick("cancelled")
	default:
		s.noteTick(err.Error())
		if s.logger != nil {
			s.logger.Printf("magma: sync failed: %v", err)
		}
	}
}

func (s *MagmaService) pollInterval(ctx context.Context) time.Duration {
	settings, err := s.Settings(ctx)
	if err != nil || settings.PollIntervalSec <= 0 {
		return magmaDefaultPollInterval
	}
	return time.Duration(settings.PollIntervalSec) * time.Second
}

// SyncOnce pulls the seller order book and reconciles it into the local tables.
// Phase 1 never mutates anything on the Amboss side.
func (s *MagmaService) SyncOnce(ctx context.Context) error {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	return s.syncLocked(ctx)
}

// syncLocked requires the caller to hold workMu.
func (s *MagmaService) syncLocked(ctx context.Context) error {
	installed, enabled, err := s.AppState(ctx)
	if err != nil {
		return err
	}
	if !installed || !enabled {
		return nil
	}

	token, err := s.token(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {
		s.recordSyncResult(nil, errors.New("Amboss API token is not configured in the Fee Center"))
		return nil
	}

	summary, err := s.amboss.MarketSummary(ctx, token)
	if err != nil {
		s.recordSyncResult(nil, err)
		return err
	}

	// The full order list is the expensive call and it grows with sales history;
	// pending_seller_orders is a scalar that moves whenever an order needs us.
	// Skipping the list when nothing can have changed turns most ticks into one
	// cheap query.
	if !s.needsOrderFetch(ctx, summary) {
		// Local upkeep still runs: it reads the database, not Amboss, and a
		// pending fee guard or an unresolved funding fee must not wait for the
		// next order to show up.
		if settings, err := s.Settings(ctx); err == nil &&
			(settings.Mode == magmaModeAssisted || settings.Mode == magmaModeAuto) {
			s.syncFeeGuards(ctx)
			s.resolveOnchainCosts(ctx)
		}
		s.resolveBuyerAliases(ctx)
		s.refreshCommitmentCache(ctx)
		s.recordSyncResult(&summary, nil)
		return nil
	}

	orders, err := s.amboss.SellerOrders(ctx, token)
	if err != nil {
		s.recordSyncResult(&summary, err)
		return err
	}
	s.noteOrderFetch(summary.PendingSellerOrders)

	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	for _, order := range orders {
		if err := s.upsertOrder(ctx, order, settings.NotifyTelegram); err != nil {
			s.recordSyncResult(&summary, err)
			return err
		}
	}
	// Resume anything interrupted between steps. Runs after the upsert so it works
	// from the freshest view, and only in assisted mode: monitor mode must never
	// touch Amboss with a mutation.
	if settings.Mode == magmaModeAssisted || settings.Mode == magmaModeAuto {
		s.reconcileExecution(ctx, token)
		// Applying and releasing the fee guard are retried from persisted state:
		// right after the open the channel has no short channel id yet, so the
		// guard cannot land on the first try.
		s.syncFeeGuards(ctx)
		// Funding fees land in the wallet transaction list a moment after the
		// broadcast, so this is retried rather than read inline at open time.
		s.resolveOnchainCosts(ctx)
	}
	// Aliases are cosmetic, so they are resolved in every mode - including
	// monitor, where the whole point is reading the order book.
	s.resolveBuyerAliases(ctx)
	// Refreshed in every mode: Autofee consults this cache on its own schedule,
	// and the channel list shows the badge regardless of what Magma may do.
	s.refreshCommitmentCache(ctx)
	// Auto mode runs last, after reconciliation has settled anything in flight, so
	// the policy never decides against a half-finished picture.
	if settings.Mode == magmaModeAuto {
		// Before deciding on orders, make sure what is advertised is still backed
		// by the wallet: an offer we cannot fund only collects seller failures.
		s.syncOfferBalanceGuard(ctx, token)
		s.runAutoMode(ctx, token)
	} else {
		// The modes that cannot answer on their own still have a deadline; the
		// operator is the one who has to meet it, so warn while it is still useful.
		s.warnExpiringApprovals(ctx)
	}
	s.recordSyncResult(&summary, nil)
	return nil
}

// magmaTerminalStatuses are the Amboss states an order never leaves. While any
// order sits outside this set, its status is still worth re-reading even when no
// new order is waiting on us — the real sale went from SELLER_SENT_TRANSACTION to
// settled payment with the pending counter at zero the whole time.
var magmaTerminalStatuses = map[string]bool{
	"CHANNEL_MONITORING_FINISHED":   true,
	"BUYER_REJECTED":                true,
	"BUYER_FAILED_TO_PAY":           true,
	"SELLER_REJECTED":               true,
	"SELLER_FAILED_TO_REACT":        true,
	"SELLER_FAILED_TO_OPEN_CHANNEL": true,
	"SELLER_FAILED_TO_SEND_SWAP":    true,
	"INVALID_CHANNEL_OPENING":       true,
	"ADMIN_CLOSED":                  true,
}

// magmaTerminalStatusList is the same set as a slice, for the SQL queries that
// have to exclude orders Amboss has already closed out. It exists so that a new
// query cannot quietly forget the exclusion: every caller reaches for this.
func magmaTerminalStatusList() []string {
	statuses := make([]string, 0, len(magmaTerminalStatuses))
	for status := range magmaTerminalStatuses {
		statuses = append(statuses, status)
	}
	return statuses
}

// magmaFullFetchMaxInterval bounds how long the cheap path can run on its own.
// The counter is a signal, not a guarantee; this makes a missed signal cost
// minutes rather than forever.
const magmaFullFetchMaxInterval = 15 * time.Minute

// needsOrderFetch decides whether this tick has to pull the whole order list.
// It errs towards fetching: skipping wrongly means missing a sale, while
// fetching wrongly only costs one query.
func (s *MagmaService) needsOrderFetch(ctx context.Context, summary MagmaMarketSummary) bool {
	s.stateMu.RLock()
	hasBaseline := s.fetchBaseline
	lastPending := s.lastPendingOrders
	lastFetch := s.lastOrderFetchAt
	s.stateMu.RUnlock()

	switch {
	case !hasBaseline:
		return true
	case summary.PendingSellerOrders > 0:
		return true
	case summary.PendingSellerOrders != lastPending:
		return true
	case time.Since(lastFetch) >= magmaFullFetchMaxInterval:
		return true
	}
	return s.hasOrdersInFlight(ctx)
}

// hasOrdersInFlight reports whether any locally known order is still moving on
// the Amboss side.
func (s *MagmaService) hasOrdersInFlight(ctx context.Context) bool {
	terminal := make([]string, 0, len(magmaTerminalStatuses))
	for status := range magmaTerminalStatuses {
		terminal = append(terminal, status)
	}
	var count int
	if err := s.db.QueryRow(ctx, `
select count(*) from magma_orders where magma_status <> '' and not (magma_status = any($1))
`, terminal).Scan(&count); err != nil {
		// Unknown means fetch: a database hiccup must not silence the poller.
		return true
	}
	return count > 0
}

// noteTick records that a cycle ran, whatever its outcome.
func (s *MagmaService) noteTick(note string) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.lastTickAt = time.Now().UTC()
	s.lastTickNote = note
	if note == "" {
		s.consecutiveFails = 0
		return
	}
	s.consecutiveFails++
}

func (s *MagmaService) noteOrderFetch(pending int64) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.fetchBaseline = true
	s.lastPendingOrders = pending
	s.lastOrderFetchAt = time.Now()
}

func (s *MagmaService) recordSyncResult(summary *MagmaMarketSummary, err error) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	s.lastSyncAt = time.Now().UTC()
	if summary != nil {
		s.lastMarket = summary
	}
	if err != nil {
		s.lastSyncError = err.Error()
		return
	}
	s.lastSyncError = ""
}

// upsertOrder writes the order snapshot and emits an event only when the Amboss
// status actually changed, so a 90s poll does not produce a 90s alert stream.
func (s *MagmaService) upsertOrder(ctx context.Context, order MagmaOrder, notifyTelegram bool) error {
	var previousStatus, notifiedStatus, previousPaymentStatus string
	err := s.db.QueryRow(ctx,
		`select magma_status, notified_status, payment_status from magma_orders where order_id=$1`, order.ID,
	).Scan(&previousStatus, &notifiedStatus, &previousPaymentStatus)
	isNew := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !isNew {
		return err
	}

	if _, err := s.db.Exec(ctx, `
insert into magma_orders (
  order_id, buyer_pubkey, offer_id, size_sat, revenue_sat, buyer_pays_sat, amboss_fee_ppm,
  price_fixed_sat, price_variable_sat, price_ppm, fee_rate_cap_ppm, base_fee_cap_sat,
  commitment_blocks, blocks_until_can_be_closed, closed_blocks_before_min, fee_above_cap_seconds,
  magma_status, payment_status, payment_hash, channel_scid, channel_point,
  cancellation_reason, seller_close_side, buyer_close_side, is_automated, chat_enabled,
  order_created_at, order_updated_at, updated_at
) values (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28, now()
)
on conflict (order_id) do update set
  buyer_pubkey=excluded.buyer_pubkey,
  offer_id=excluded.offer_id,
  size_sat=excluded.size_sat,
  revenue_sat=excluded.revenue_sat,
  buyer_pays_sat=excluded.buyer_pays_sat,
  amboss_fee_ppm=excluded.amboss_fee_ppm,
  price_fixed_sat=excluded.price_fixed_sat,
  price_variable_sat=excluded.price_variable_sat,
  price_ppm=excluded.price_ppm,
  fee_rate_cap_ppm=excluded.fee_rate_cap_ppm,
  base_fee_cap_sat=excluded.base_fee_cap_sat,
  commitment_blocks=excluded.commitment_blocks,
  blocks_until_can_be_closed=excluded.blocks_until_can_be_closed,
  closed_blocks_before_min=excluded.closed_blocks_before_min,
  fee_above_cap_seconds=excluded.fee_above_cap_seconds,
  magma_status=excluded.magma_status,
  payment_status=excluded.payment_status,
  payment_hash=excluded.payment_hash,
  channel_scid=excluded.channel_scid,
  -- Amboss only reports transaction_id once it has registered the sale. Copying
  -- a blank over a channel point we set ourselves during the open would lose the
  -- outpoint we still have to confirm.
  channel_point=case when excluded.channel_point <> '' then excluded.channel_point
                     else magma_orders.channel_point end,
  cancellation_reason=excluded.cancellation_reason,
  seller_close_side=excluded.seller_close_side,
  buyer_close_side=excluded.buyer_close_side,
  is_automated=excluded.is_automated,
  chat_enabled=excluded.chat_enabled,
  order_created_at=excluded.order_created_at,
  order_updated_at=excluded.order_updated_at,
  updated_at=now()
`,
		order.ID, order.BuyerPubkey, order.OfferID, order.SizeSat, order.RevenueSat, order.BuyerPaysSat,
		order.AmbossFeePPM, order.PriceFixedSat, order.PriceVariableSat, order.PricePPM,
		order.FeeRateCapPPM, order.BaseFeeCapSat, order.CommitmentBlocks,
		order.BlocksUntilCanBeClosed, order.ClosedBlocksBeforeMin, order.FeeAboveCapSeconds,
		order.Status, order.PaymentStatus, order.PaymentHash, order.ChannelSCID, order.ChannelPoint,
		order.CancellationReason, order.SellerCloseSide, order.BuyerCloseSide,
		order.IsAutomated, order.ChatEnabled, order.CreatedAt, order.UpdatedAt,
	); err != nil {
		return err
	}

	// Stamp the moment the sale is paid, but only on a transition we actually
	// observed. A first sync imports years of already-settled orders; stamping
	// those would inject their whole revenue history into today's report.
	if !isNew &&
		previousPaymentStatus != magmaPaymentSuccessful &&
		order.PaymentStatus == magmaPaymentSuccessful {
		if _, err := s.db.Exec(ctx, `
update magma_orders set revenue_settled_at=now()
where order_id=$1 and revenue_settled_at is null
`, order.ID); err != nil {
			return err
		}
		s.appendEvent(ctx, order.ID, "revenue_settled", "info", fmt.Sprintf(
			"Buyer payment confirmed: %s sats", formatInt(order.RevenueSat)), nil)
	}

	if !isNew && previousStatus == order.Status {
		return nil
	}

	kind, level, message := magmaOrderEventFor(order, isNew, previousStatus)
	s.appendEvent(ctx, order.ID, kind, level, message, map[string]any{
		"status":          order.Status,
		"previous_status": previousStatus,
		"size_sat":        order.SizeSat,
		"revenue_sat":     order.RevenueSat,
	})

	if notifyTelegram && magmaShouldNotify(order, isNew, previousStatus) {
		s.notifyTelegram(ctx, order, message)
		if _, err := s.db.Exec(ctx,
			`update magma_orders set notified_status=$2 where order_id=$1`, order.ID, order.Status,
		); err != nil {
			return err
		}
	}
	return nil
}

func magmaOrderEventFor(order MagmaOrder, isNew bool, previousStatus string) (string, string, string) {
	switch {
	case magmaSellerFailureStatuses[order.Status]:
		return "seller_failure", "error", fmt.Sprintf(
			"Magma order %s failed on our side: %s", order.ID, order.Status)
	case order.Status == "WAITING_FOR_SELLER_APPROVAL":
		return "action_needed", "warning", fmt.Sprintf(
			"Magma order %s awaits seller approval: %s sats of inbound for %s sats",
			order.ID, formatInt(order.SizeSat), formatInt(order.RevenueSat))
	case order.Status == "WAITING_FOR_CHANNEL_OPEN":
		return "action_needed", "warning", fmt.Sprintf(
			"Magma order %s is paid and awaits the channel open to %s",
			order.ID, magmaShortPubkey(order.BuyerPubkey))
	case isNew:
		return "discovered", "info", fmt.Sprintf(
			"Magma order %s recorded with status %s", order.ID, order.Status)
	default:
		return "status_changed", "info", fmt.Sprintf(
			"Magma order %s moved from %s to %s", order.ID, previousStatus, order.Status)
	}
}

// magmaShouldNotify keeps the alerting narrow in monitor mode: states where the
// seller must act, and recorded failures. Historical bulk on first sync would
// otherwise flood Telegram with years of finished orders.
func magmaShouldNotify(order MagmaOrder, isNew bool, previousStatus string) bool {
	if _, actionable := magmaActionableStatuses[order.Status]; actionable {
		return true
	}
	if magmaSellerFailureStatuses[order.Status] {
		return true
	}
	if isNew {
		return false
	}
	return previousStatus != "" && previousStatus != order.Status && magmaIsTerminalFailure(order.Status)
}

func magmaIsTerminalFailure(status string) bool {
	switch status {
	case "BUYER_FAILED_TO_PAY", "BUYER_REJECTED", "ADMIN_CLOSED":
		return true
	default:
		return false
	}
}

func (s *MagmaService) notifyTelegram(ctx context.Context, order MagmaOrder, message string) {
	cfg := readTelegramBackupConfig()
	if !cfg.configured() {
		return
	}
	// Telegram output is English throughout, matching the rest of the bot.
	text := "⚡ Magma\n" + message
	if order.CommitmentBlocks > 0 {
		text += fmt.Sprintf("\nCommitment: %s blocks (~%.0f days)",
			formatInt(order.CommitmentBlocks), float64(order.CommitmentBlocks)/144.0)
	}
	if order.FeeRateCapPPM > 0 {
		text += fmt.Sprintf("\nFee ceiling: %s ppm / base %s sat",
			formatInt(order.FeeRateCapPPM), formatInt(order.BaseFeeCapSat))
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	if err := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, text); err != nil && s.logger != nil {
		s.logger.Printf("magma: telegram notification failed: %v", err)
	}
}

func magmaShortPubkey(pubkey string) string {
	trimmed := strings.TrimSpace(pubkey)
	if len(trimmed) <= 16 {
		return trimmed
	}
	return trimmed[:8] + "…" + trimmed[len(trimmed)-8:]
}

func (s *MagmaService) appendEvent(ctx context.Context, orderID, kind, level, message string, metadata map[string]any) {
	payload := []byte("{}")
	if len(metadata) > 0 {
		if encoded, err := json.Marshal(metadata); err == nil {
			payload = encoded
		}
	}
	if _, err := s.db.Exec(ctx, `
insert into magma_order_events (order_id, kind, level, message, metadata)
values ($1,$2,$3,$4,$5)
`, orderID, kind, level, message, payload); err != nil && s.logger != nil {
		s.logger.Printf("magma: failed to append event for order %s: %v", orderID, err)
	}
}

func (s *MagmaService) Overview(ctx context.Context) (MagmaOverview, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return MagmaOverview{}, err
	}
	orders, err := s.ListOrders(ctx, 200)
	if err != nil {
		return MagmaOverview{}, err
	}
	overview := MagmaOverview{
		Settings: settings,
		Token:    s.TokenState(ctx),
		Orders:   orders,
	}
	for _, order := range orders {
		if _, actionable := magmaActionableStatuses[order.Status]; actionable {
			overview.ActionNeeded = append(overview.ActionNeeded, order)
		}
	}
	// Capacity drives the accept decision, so the operator sees it before clicking
	// rather than discovering the shortfall after the buyer has already paid.
	if capacity, err := s.Capacity(ctx); err == nil {
		overview.Capacity = &capacity
	}
	if token, err := s.token(ctx); err == nil {
		overview.TokenWarning = magmaTokenExpiryWarning(token, time.Now())
	}
	if policy, err := s.loadPolicy(ctx); err == nil {
		overview.Policy = &policy
		overview.PolicySummary = magmaPolicySummary(policy)
		overview.PolicyWarnings = magmaPolicyWarnings(policy)
	}
	if pnl, err := s.PnL(ctx, time.Time{}); err == nil {
		overview.PnL = &pnl
	}
	s.stateMu.RLock()
	if !s.lastSyncAt.IsZero() {
		syncedAt := s.lastSyncAt
		overview.LastSyncAt = &syncedAt
	}
	overview.LastSyncError = s.lastSyncError
	overview.Market = s.lastMarket
	overview.Poller = MagmaPollerHealth{
		Started:          s.pollerStarted,
		LastTickNote:     s.lastTickNote,
		ConsecutiveFails: s.consecutiveFails,
	}
	if !s.lastTickAt.IsZero() {
		tickedAt := s.lastTickAt
		overview.Poller.LastTickAt = &tickedAt
	}
	s.stateMu.RUnlock()
	return overview, nil
}

func (s *MagmaService) ListOrders(ctx context.Context, limit int) ([]MagmaOrder, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
select order_id, buyer_pubkey, offer_id, size_sat, revenue_sat, buyer_pays_sat, amboss_fee_ppm,
       price_fixed_sat, price_variable_sat, price_ppm, fee_rate_cap_ppm, base_fee_cap_sat,
       commitment_blocks, blocks_until_can_be_closed, closed_blocks_before_min, fee_above_cap_seconds,
       magma_status, payment_status, payment_hash, channel_scid, channel_point,
       cancellation_reason, seller_close_side, buyer_close_side, is_automated, chat_enabled,
       order_created_at, order_updated_at, local_state, funding_txid, last_error,
       coalesce(buyer_alias, '')
from magma_orders
order by coalesce(order_created_at, first_seen_at) desc
limit $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	orders := make([]MagmaOrder, 0, limit)
	for rows.Next() {
		var order MagmaOrder
		if err := rows.Scan(
			&order.ID, &order.BuyerPubkey, &order.OfferID, &order.SizeSat, &order.RevenueSat,
			&order.BuyerPaysSat, &order.AmbossFeePPM, &order.PriceFixedSat, &order.PriceVariableSat,
			&order.PricePPM, &order.FeeRateCapPPM, &order.BaseFeeCapSat, &order.CommitmentBlocks,
			&order.BlocksUntilCanBeClosed, &order.ClosedBlocksBeforeMin, &order.FeeAboveCapSeconds,
			&order.Status, &order.PaymentStatus, &order.PaymentHash, &order.ChannelSCID,
			&order.ChannelPoint, &order.CancellationReason, &order.SellerCloseSide,
			&order.BuyerCloseSide, &order.IsAutomated, &order.ChatEnabled,
			&order.CreatedAt, &order.UpdatedAt,
			&order.LocalState, &order.FundingTxid, &order.LastError, &order.BuyerAlias,
		); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

// ListRecentEvents returns the newest events across every order. Auto mode makes
// its decisions between page loads, so without a visible timeline the operator
// opens the app and finds only the current state, with no record of why it got
// there — a deferral reason lives in last_error and is cleared by the next
// transition.
func (s *MagmaService) ListRecentEvents(ctx context.Context, limit int) ([]MagmaOrderEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 60
	}
	rows, err := s.db.Query(ctx, `
select id, order_id, kind, level, message, metadata, created_at
from magma_order_events
order by id desc
limit $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]MagmaOrderEvent, 0, limit)
	for rows.Next() {
		var event MagmaOrderEvent
		if err := rows.Scan(&event.ID, &event.OrderID, &event.Kind, &event.Level,
			&event.Message, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *MagmaService) ListOrderEvents(ctx context.Context, orderID string, limit int) ([]MagmaOrderEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
select id, order_id, kind, level, message, metadata, created_at
from magma_order_events
where order_id=$1
order by id desc
limit $2
`, strings.TrimSpace(orderID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]MagmaOrderEvent, 0, limit)
	for rows.Next() {
		var event MagmaOrderEvent
		if err := rows.Scan(&event.ID, &event.OrderID, &event.Kind, &event.Level,
			&event.Message, &event.Metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].ID > events[j].ID })
	return events, nil
}

// RefreshNow lets the UI force a sync without waiting for the poll tick.
func (s *MagmaService) RefreshNow(ctx context.Context) error {
	return s.SyncOnce(ctx)
}
