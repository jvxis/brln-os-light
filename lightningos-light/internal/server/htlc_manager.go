package server

import (
  "context"
  "errors"
  "fmt"
  "log"
  "math"
  "net/http"
  "strconv"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"
  "lightningos-light/lnrpc/routerrpc"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
)

const (
  htlcManagerConfigID = 1
  htlcManagerDefaultIntervalMinutes = 240
  htlcManagerDefaultMinHTLCSat int64 = 1
  htlcManagerDefaultMaxLocalPct = 0
  htlcManagerMaxHysteresisBaseSat int64 = 250
  htlcManagerMaxHysteresisDivisor int64 = 400 // 0.25% of effective channel capacity in sats
  htlcManagerVolatileHysteresisMultiplier int64 = 3
  htlcManagerMinIntervalMinutes = 1
  htlcManagerMaxIntervalMinutes = 48 * 60
  htlcManagerLogCapacity = 300
  htlcManagerFailedCapacity = 500
  htlcManagerDefaultLogLimit = 100
  htlcManagerMaxLogLimit = 500
  htlcManagerFailedStreamRetry = 5 * time.Second
)

var errInvalidHTLCManagerConfig = errors.New("invalid htlc manager config")

type HtlcManagerConfig struct {
  Enabled bool `json:"enabled"`
  IntervalMinutes int `json:"interval_minutes"`
  MinHtlcSat int64 `json:"min_htlc_sat"`
  MaxLocalPct int `json:"max_local_pct"`
}

type HtlcManagerConfigUpdate struct {
  Enabled *bool
  IntervalMinutes *int
  IntervalHours *int
  MinHtlcSat *int64
  MaxLocalPct *int
  RunNow bool
}

type htlcManagerStatusPayload struct {
  Enabled bool `json:"enabled"`
  Status string `json:"status"`
  IntervalMinutes int `json:"interval_minutes"`
  IntervalHours int `json:"interval_hours"`
  MinHtlcSat int64 `json:"min_htlc_sat"`
  MaxLocalPct int `json:"max_local_pct"`
  LastAttemptAt string `json:"last_attempt_at,omitempty"`
  LastOkAt string `json:"last_ok_at,omitempty"`
  LastError string `json:"last_error,omitempty"`
  LastErrorAt string `json:"last_error_at,omitempty"`
  LastChangedCount int `json:"last_changed_count,omitempty"`
}

type htlcManagerLogEntry struct {
  Timestamp string `json:"ts"`
  Alias string `json:"alias"`
  ChannelID uint64 `json:"channel_id"`
  ChannelPoint string `json:"channel_point"`
  OldMinMsat uint64 `json:"old_min_msat"`
  NewMinMsat uint64 `json:"new_min_msat"`
  OldMaxMsat uint64 `json:"old_max_msat"`
  NewMaxMsat uint64 `json:"new_max_msat"`
  Result string `json:"result"`
}

type htlcManagerFailedEntry struct {
  Timestamp string `json:"ts"`
  IncomingChannelID string `json:"incoming_channel_id"`
  IncomingAlias string `json:"incoming_alias,omitempty"`
  OutgoingChannelID string `json:"outgoing_channel_id"`
  OutgoingAlias string `json:"outgoing_alias,omitempty"`
  IncomingAmtMsat uint64 `json:"incoming_amt_msat,omitempty"`
  OutgoingAmtMsat uint64 `json:"outgoing_amt_msat,omitempty"`
  PotentialFeeMsat int64 `json:"potential_fee_msat,omitempty"`
  FailureCode string `json:"failure_code,omitempty"`
  FailureDetail string `json:"failure_detail,omitempty"`
  FailureReason string `json:"failure_reason,omitempty"`
  Event string `json:"event"`
}

type htlcManagerTrigger struct {
  force bool
}

type HtlcManager struct {
  db *pgxpool.Pool
  lnd *lndclient.Client
  logger *log.Logger

  mu sync.Mutex
  config HtlcManagerConfig
  lastAttempt time.Time
  lastOK time.Time
  lastError string
  lastErrorAt time.Time
  lastChangedCount int
  inFlight bool
  started bool
  stop chan struct{}
  wake chan htlcManagerTrigger
  intervalUpdated chan struct{}
  logs []htlcManagerLogEntry
  failed []htlcManagerFailedEntry
}

func NewHtlcManager(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *HtlcManager {
  return &HtlcManager{
    db: db,
    lnd: lnd,
    logger: logger,
    config: defaultHTLCManagerConfig(),
  }
}

func defaultHTLCManagerConfig() HtlcManagerConfig {
  return HtlcManagerConfig{
    Enabled: false,
    IntervalMinutes: htlcManagerDefaultIntervalMinutes,
    MinHtlcSat: htlcManagerDefaultMinHTLCSat,
    MaxLocalPct: htlcManagerDefaultMaxLocalPct,
  }
}

func normalizeHTLCManagerConfig(cfg HtlcManagerConfig) HtlcManagerConfig {
  if cfg.IntervalMinutes < htlcManagerMinIntervalMinutes {
    cfg.IntervalMinutes = htlcManagerMinIntervalMinutes
  }
  if cfg.IntervalMinutes > htlcManagerMaxIntervalMinutes {
    cfg.IntervalMinutes = htlcManagerMaxIntervalMinutes
  }
  if cfg.MinHtlcSat < 1 {
    cfg.MinHtlcSat = 1
  }
  if cfg.MaxLocalPct < 0 {
    cfg.MaxLocalPct = 0
  }
  return cfg
}

func validateHTLCManagerConfig(cfg HtlcManagerConfig) error {
  if cfg.IntervalMinutes < htlcManagerMinIntervalMinutes || cfg.IntervalMinutes > htlcManagerMaxIntervalMinutes {
    return fmt.Errorf("%w: interval_minutes must be between %d and %d", errInvalidHTLCManagerConfig, htlcManagerMinIntervalMinutes, htlcManagerMaxIntervalMinutes)
  }
  if cfg.MinHtlcSat < 1 {
    return fmt.Errorf("%w: min_htlc_sat must be at least 1", errInvalidHTLCManagerConfig)
  }
  if cfg.MaxLocalPct < 0 {
    return fmt.Errorf("%w: max_local_pct must be zero or positive", errInvalidHTLCManagerConfig)
  }
  return nil
}

func (m *HtlcManager) EnsureSchema(ctx context.Context) error {
  if m.db == nil {
    return errors.New("db unavailable")
  }

  if _, err := m.db.Exec(ctx, `
create table if not exists htlc_manager_config (
  id integer primary key,
  enabled boolean not null default false,
  interval_hours integer not null default 4,
  interval_minutes integer not null default 240,
  min_htlc_sat bigint not null default 1,
  max_local_pct integer not null default 0,
  updated_at timestamptz not null default now()
);
`); err != nil {
    return err
  }

  if _, err := m.db.Exec(ctx, `
alter table htlc_manager_config
  add column if not exists interval_minutes integer;
`); err != nil {
    return err
  }

  if _, err := m.db.Exec(ctx, `
alter table htlc_manager_config
  alter column interval_minutes set default 240;
`); err != nil {
    return err
  }

  if _, err := m.db.Exec(ctx, `
update htlc_manager_config
set interval_minutes = greatest(interval_hours * 60, $1)
where interval_minutes is null or interval_minutes <= 0
`, htlcManagerMinIntervalMinutes); err != nil {
    return err
  }

  if _, err := m.db.Exec(ctx, `
alter table htlc_manager_config
  alter column interval_minutes set not null;
`); err != nil {
    return err
  }

  _, err := m.db.Exec(ctx, `
insert into htlc_manager_config (id)
values ($1)
on conflict (id) do nothing
`, htlcManagerConfigID)
  return err
}

func (m *HtlcManager) GetConfig(ctx context.Context) (HtlcManagerConfig, error) {
  cfg := defaultHTLCManagerConfig()
  if m.db == nil {
    return cfg, errors.New("db unavailable")
  }

  err := m.db.QueryRow(ctx, `
select enabled, coalesce(interval_minutes, interval_hours * 60), min_htlc_sat, max_local_pct
from htlc_manager_config
where id = $1
`, htlcManagerConfigID).Scan(
    &cfg.Enabled,
    &cfg.IntervalMinutes,
    &cfg.MinHtlcSat,
    &cfg.MaxLocalPct,
  )
  if err != nil {
    if errors.Is(err, pgx.ErrNoRows) {
      return cfg, nil
    }
    return cfg, err
  }
  return normalizeHTLCManagerConfig(cfg), nil
}

func (m *HtlcManager) upsertConfig(ctx context.Context, cfg HtlcManagerConfig) error {
  if m.db == nil {
    return errors.New("db unavailable")
  }
  _, err := m.db.Exec(ctx, `
insert into htlc_manager_config (id, enabled, interval_hours, interval_minutes, min_htlc_sat, max_local_pct, updated_at)
values ($1, $2, $3, $4, $5, $6, now())
on conflict (id) do update set
  enabled = excluded.enabled,
  interval_hours = excluded.interval_hours,
  interval_minutes = excluded.interval_minutes,
  min_htlc_sat = excluded.min_htlc_sat,
  max_local_pct = excluded.max_local_pct,
  updated_at = now()
`, htlcManagerConfigID, cfg.Enabled, minutesToHoursCeil(cfg.IntervalMinutes), cfg.IntervalMinutes, cfg.MinHtlcSat, cfg.MaxLocalPct)
  return err
}

func (m *HtlcManager) Start() {
  m.mu.Lock()
  if m.started {
    m.mu.Unlock()
    return
  }
  m.started = true
  m.stop = make(chan struct{})
  m.wake = make(chan htlcManagerTrigger, 1)
  m.intervalUpdated = make(chan struct{}, 1)
  m.mu.Unlock()

  if err := m.reloadConfig(); err != nil && m.logger != nil {
    m.logger.Printf("htlc-manager: config load failed: %v", err)
  }

  go m.run()
  go m.runFailedHTLCStream()
}

func (m *HtlcManager) Stop() {
  m.mu.Lock()
  if !m.started || m.stop == nil {
    m.mu.Unlock()
    return
  }
  close(m.stop)
  m.stop = nil
  m.started = false
  m.mu.Unlock()
}

func (m *HtlcManager) reloadConfig() error {
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  cfg, err := m.GetConfig(ctx)
  if err != nil {
    return err
  }
  m.mu.Lock()
  m.config = cfg
  m.mu.Unlock()
  return nil
}

func (m *HtlcManager) UpdateConfig(ctx context.Context, update HtlcManagerConfigUpdate) (htlcManagerStatusPayload, error) {
  current, err := m.GetConfig(ctx)
  if err != nil {
    return htlcManagerStatusPayload{}, err
  }

  if update.Enabled != nil {
    current.Enabled = *update.Enabled
  }
  if update.IntervalMinutes != nil {
    current.IntervalMinutes = *update.IntervalMinutes
  } else if update.IntervalHours != nil {
    current.IntervalMinutes = *update.IntervalHours * 60
  }
  if update.MinHtlcSat != nil {
    current.MinHtlcSat = *update.MinHtlcSat
  }
  if update.MaxLocalPct != nil {
    current.MaxLocalPct = *update.MaxLocalPct
  }

  if err := validateHTLCManagerConfig(current); err != nil {
    return htlcManagerStatusPayload{}, err
  }

  if err := m.upsertConfig(ctx, current); err != nil {
    return htlcManagerStatusPayload{}, err
  }

  m.mu.Lock()
  m.config = current
  intervalUpdated := m.intervalUpdated
  m.mu.Unlock()

  if intervalUpdated != nil {
    select {
    case intervalUpdated <- struct{}{}:
    default:
    }
  }
  if update.RunNow {
    m.trigger(true)
  }
  return m.Snapshot(), nil
}

func (m *HtlcManager) Snapshot() htlcManagerStatusPayload {
  m.mu.Lock()
  cfg := m.config
  lastAttempt := m.lastAttempt
  lastOK := m.lastOK
  lastError := m.lastError
  lastErrorAt := m.lastErrorAt
  lastChanged := m.lastChangedCount
  inFlight := m.inFlight
  m.mu.Unlock()

  status := "disabled"
  if cfg.Enabled {
    status = "checking"
    interval := time.Duration(localMaxInt(cfg.IntervalMinutes, 1)) * time.Minute
    if inFlight {
      status = "checking"
    } else if lastError != "" && (lastOK.IsZero() || lastErrorAt.After(lastOK)) {
      status = "warn"
    } else if !lastOK.IsZero() {
      status = "ok"
      if time.Since(lastOK) > interval*2 {
        status = "warn"
      }
    }
  }

  payload := htlcManagerStatusPayload{
    Enabled: cfg.Enabled,
    Status: status,
    IntervalMinutes: cfg.IntervalMinutes,
    IntervalHours: minutesToHoursCeil(cfg.IntervalMinutes),
    MinHtlcSat: cfg.MinHtlcSat,
    MaxLocalPct: cfg.MaxLocalPct,
    LastChangedCount: lastChanged,
  }
  if !lastAttempt.IsZero() {
    payload.LastAttemptAt = lastAttempt.UTC().Format(time.RFC3339)
  }
  if !lastOK.IsZero() {
    payload.LastOkAt = lastOK.UTC().Format(time.RFC3339)
  }
  if lastError != "" {
    payload.LastError = lastError
  }
  if !lastErrorAt.IsZero() {
    payload.LastErrorAt = lastErrorAt.UTC().Format(time.RFC3339)
  }
  return payload
}

func (m *HtlcManager) Logs(limit int) []htlcManagerLogEntry {
  if limit <= 0 {
    limit = htlcManagerDefaultLogLimit
  }
  if limit > htlcManagerMaxLogLimit {
    limit = htlcManagerMaxLogLimit
  }

  m.mu.Lock()
  defer m.mu.Unlock()

  if len(m.logs) == 0 {
    return []htlcManagerLogEntry{}
  }
  out := make([]htlcManagerLogEntry, 0, localMinInt(limit, len(m.logs)))
  for i := len(m.logs) - 1; i >= 0 && len(out) < limit; i-- {
    out = append(out, m.logs[i])
  }
  return out
}

func (m *HtlcManager) appendLog(entry htlcManagerLogEntry) {
  m.mu.Lock()
  defer m.mu.Unlock()
  if len(m.logs) >= htlcManagerLogCapacity {
    copy(m.logs, m.logs[1:])
    m.logs[len(m.logs)-1] = entry
    return
  }
  m.logs = append(m.logs, entry)
}

func (m *HtlcManager) Failed(limit int) []htlcManagerFailedEntry {
  if limit <= 0 {
    limit = htlcManagerDefaultLogLimit
  }
  if limit > htlcManagerMaxLogLimit {
    limit = htlcManagerMaxLogLimit
  }

  m.mu.Lock()
  defer m.mu.Unlock()

  if len(m.failed) == 0 {
    return []htlcManagerFailedEntry{}
  }
  out := make([]htlcManagerFailedEntry, 0, localMinInt(limit, len(m.failed)))
  for i := len(m.failed) - 1; i >= 0 && len(out) < limit; i-- {
    out = append(out, m.failed[i])
  }
  return out
}

func (m *HtlcManager) appendFailed(entry htlcManagerFailedEntry) {
  m.mu.Lock()
  defer m.mu.Unlock()
  if len(m.failed) >= htlcManagerFailedCapacity {
    copy(m.failed, m.failed[1:])
    m.failed[len(m.failed)-1] = entry
    return
  }
  m.failed = append(m.failed, entry)
}

func (m *HtlcManager) trigger(force bool) {
  m.mu.Lock()
  wake := m.wake
  m.mu.Unlock()
  if wake == nil {
    return
  }
  select {
  case wake <- htlcManagerTrigger{force: force}:
  default:
  }
}

func (m *HtlcManager) currentInterval() time.Duration {
  m.mu.Lock()
  interval := m.config.IntervalMinutes
  m.mu.Unlock()
  if interval < htlcManagerMinIntervalMinutes {
    interval = htlcManagerMinIntervalMinutes
  }
  if interval > htlcManagerMaxIntervalMinutes {
    interval = htlcManagerMaxIntervalMinutes
  }
  return time.Duration(interval) * time.Minute
}

func (m *HtlcManager) run() {
  timer := time.NewTimer(m.currentInterval())
  defer timer.Stop()

  for {
    select {
    case <-timer.C:
      m.tick(false)
      timer.Reset(m.currentInterval())
    case trigger := <-m.wake:
      m.tick(trigger.force)
    case <-m.intervalUpdated:
      if !timer.Stop() {
        select {
        case <-timer.C:
        default:
        }
      }
      timer.Reset(m.currentInterval())
    case <-m.stop:
      return
    }
  }
}

func (m *HtlcManager) runFailedHTLCStream() {
  for {
    if m.shouldStop() {
      return
    }

    dialCtx, dialCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    conn, err := m.lnd.DialLightning(dialCtx)
    dialCancel()
    if err != nil {
      if m.logger != nil {
        m.logger.Printf("htlc-manager: failed htlc stream dial failed: %v", err)
      }
      if !m.waitOrStop(htlcManagerFailedStreamRetry) {
        return
      }
      continue
    }

    routerClient := routerrpc.NewRouterClient(conn)
    streamCtx, streamCancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    go func() {
      select {
      case <-m.stop:
        streamCancel()
      case <-done:
      }
    }()

    stream, err := routerClient.SubscribeHtlcEvents(streamCtx, &routerrpc.SubscribeHtlcEventsRequest{})
    if err != nil {
      close(done)
      streamCancel()
      _ = conn.Close()
      if m.logger != nil {
        m.logger.Printf("htlc-manager: failed htlc stream subscribe failed: %v", err)
      }
      if !m.waitOrStop(htlcManagerFailedStreamRetry) {
        return
      }
      continue
    }

    for {
      evt, recvErr := stream.Recv()
      if recvErr != nil {
        if m.shouldStop() || isContextCanceledError(recvErr) {
          close(done)
          streamCancel()
          _ = conn.Close()
          return
        }
        if m.logger != nil {
          m.logger.Printf("htlc-manager: failed htlc stream recv failed: %v", recvErr)
        }
        break
      }
      m.captureFailedHTLC(evt)
    }

    close(done)
    streamCancel()
    _ = conn.Close()
    if !m.waitOrStop(htlcManagerFailedStreamRetry) {
      return
    }
  }
}

func (m *HtlcManager) shouldStop() bool {
  m.mu.Lock()
  stop := m.stop
  m.mu.Unlock()
  if stop == nil {
    return true
  }
  select {
  case <-stop:
    return true
  default:
    return false
  }
}

func (m *HtlcManager) waitOrStop(d time.Duration) bool {
  m.mu.Lock()
  stop := m.stop
  m.mu.Unlock()
  if stop == nil {
    return false
  }
  timer := time.NewTimer(d)
  defer timer.Stop()
  select {
  case <-timer.C:
    return true
  case <-stop:
    return false
  }
}

func (m *HtlcManager) captureFailedHTLC(evt *routerrpc.HtlcEvent) {
  if evt == nil {
    return
  }
  linkFail := evt.GetLinkFailEvent()
  forwardFail := evt.GetForwardFailEvent()
  if linkFail == nil && forwardFail == nil {
    return
  }

  ts := time.Now().UTC()
  if tsNs := evt.GetTimestampNs(); tsNs > 0 {
    sec := int64(tsNs / uint64(time.Second))
    nsec := int64(tsNs % uint64(time.Second))
    parsed := time.Unix(sec, nsec).UTC()
    if !parsed.IsZero() {
      ts = parsed
    }
  }

  entry := htlcManagerFailedEntry{
    Timestamp: ts.Format(time.RFC3339),
    IncomingChannelID: formatShortChanID(evt.GetIncomingChannelId()),
    OutgoingChannelID: formatShortChanID(evt.GetOutgoingChannelId()),
  }

  if linkFail != nil {
    info := linkFail.GetInfo()
    if info != nil {
      entry.IncomingAmtMsat = info.GetIncomingAmtMsat()
      entry.OutgoingAmtMsat = info.GetOutgoingAmtMsat()
      if entry.IncomingAmtMsat > entry.OutgoingAmtMsat {
        diff := entry.IncomingAmtMsat - entry.OutgoingAmtMsat
        if diff <= uint64(math.MaxInt64) {
          entry.PotentialFeeMsat = int64(diff)
        }
      }
    }
    entry.FailureCode = normalizeFailureLabel(linkFail.GetWireFailure().String())
    entry.FailureDetail = normalizeFailureLabel(linkFail.GetFailureDetail().String())
    entry.FailureReason = strings.TrimSpace(linkFail.GetFailureString())
    entry.Event = "link_fail"
  } else {
    entry.Event = "forward_fail"
    entry.FailureCode = "FORWARD_FAIL"
  }

  m.appendFailed(entry)
}

func (m *HtlcManager) tick(force bool) {
  m.mu.Lock()
  cfg := m.config
  if (!cfg.Enabled && !force) || m.inFlight {
    m.mu.Unlock()
    return
  }
  m.inFlight = true
  m.lastAttempt = time.Now().UTC()
  m.mu.Unlock()

  defer func() {
    m.mu.Lock()
    m.inFlight = false
    m.mu.Unlock()
  }()

  ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
  channels, err := m.lnd.ListChannels(ctx)
  cancel()
  if err != nil {
    m.recordFailure(err, 0)
    return
  }

  changed := 0
  failures := 0
  var lastErr error

  for _, ch := range channels {
    if !ch.Active || strings.TrimSpace(ch.ChannelPoint) == "" {
      continue
    }

    targetMinMsat, targetMaxMsat, calcErr := computeHTLCTargets(ch, cfg)
    if calcErr != nil {
      failures++
      lastErr = calcErr
      if m.logger != nil {
        m.logger.Printf("htlc-manager: skip %s: %v", ch.ChannelPoint, calcErr)
      }
      continue
    }

    policyCtx, policyCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    policy, policyErr := m.lnd.GetChannelPolicy(policyCtx, ch.ChannelPoint)
    policyCancel()
    if policyErr != nil {
      failures++
      lastErr = policyErr
      if m.logger != nil {
        m.logger.Printf("htlc-manager: policy lookup failed for %s: %v", ch.ChannelPoint, policyErr)
      }
      continue
    }

    effectiveCapSat := maxHTLCUpperBoundSat(ch)
    volatileBalance := ch.PendingHtlcCount > 0 || ch.UnsettledBalanceSat > 0
    if policy.MinHtlcMsat == targetMinMsat && withinMaxHTLCHysteresis(policy.MaxHtlcMsat, targetMaxMsat, effectiveCapSat, volatileBalance) {
      continue
    }

    updateCtx, updateCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
    updateErr := m.lnd.UpdateChannelPolicy(updateCtx, lndclient.UpdateChannelPolicyParams{
      ChannelPoint: ch.ChannelPoint,
      ApplyAll: false,
      BaseFeeMsat: policy.BaseFeeMsat,
      FeeRatePpm: policy.FeeRatePpm,
      TimeLockDelta: policy.TimeLockDelta,
      InboundEnabled: true,
      InboundBaseMsat: policy.InboundBaseMsat,
      InboundFeeRatePpm: policy.InboundFeeRatePpm,
      MaxHtlcMsat: &targetMaxMsat,
      MinHtlcMsat: &targetMinMsat,
      MinHtlcMsatSpecified: true,
    })
    updateCancel()
    if updateErr != nil {
      failures++
      lastErr = updateErr
      if m.logger != nil {
        m.logger.Printf("htlc-manager: update failed for %s: %v", ch.ChannelPoint, updateErr)
      }
      continue
    }

    changed++
    alias := strings.TrimSpace(ch.PeerAlias)
    if alias == "" {
      alias = shortIdentifier(ch.RemotePubkey)
    }
    if alias == "" {
      alias = ch.ChannelPoint
    }

    entry := htlcManagerLogEntry{
      Timestamp: time.Now().UTC().Format(time.RFC3339),
      Alias: alias,
      ChannelID: ch.ChannelID,
      ChannelPoint: ch.ChannelPoint,
      OldMinMsat: policy.MinHtlcMsat,
      NewMinMsat: targetMinMsat,
      OldMaxMsat: policy.MaxHtlcMsat,
      NewMaxMsat: targetMaxMsat,
      Result: "updated",
    }
    m.appendLog(entry)
    if m.logger != nil {
      m.logger.Printf("htlc-manager: updated %s (%s) min %d->%d msat max %d->%d msat", alias, ch.ChannelPoint, policy.MinHtlcMsat, targetMinMsat, policy.MaxHtlcMsat, targetMaxMsat)
    }
  }

  if failures > 0 {
    if lastErr == nil {
      lastErr = errors.New("htlc manager run completed with failures")
    } else {
      lastErr = fmt.Errorf("htlc manager run completed with failures: %w", lastErr)
    }
    m.recordFailure(lastErr, changed)
    return
  }
  m.recordSuccess(changed)
}

func computeHTLCTargets(ch lndclient.ChannelInfo, cfg HtlcManagerConfig) (uint64, uint64, error) {
  minMsat, err := satToMsat(cfg.MinHtlcSat)
  if err != nil {
    return 0, 0, err
  }
  effectiveCapSat := maxHTLCUpperBoundSat(ch)
  capMsat, err := satToMsat(effectiveCapSat)
  if err != nil {
    return 0, 0, fmt.Errorf("capacity conversion failed: %w", err)
  }
  if capMsat == 0 {
    return 0, 0, errors.New("effective capacity unavailable")
  }
  if minMsat > capMsat {
    return 0, 0, fmt.Errorf("min_htlc (%d msat) above effective channel capacity (%d msat)", minMsat, capMsat)
  }

  localSat := spendableLocalSat(ch)

  extraSat := int64(math.Floor(float64(localSat) * float64(cfg.MaxLocalPct) / 100.0))
  if extraSat < 0 {
    extraSat = 0
  }
  rawMaxSat := localSat + extraSat
  if rawMaxSat < 0 {
    rawMaxSat = 0
  }
  if rawMaxSat > effectiveCapSat {
    rawMaxSat = effectiveCapSat
  }

  maxMsat, err := satToMsat(rawMaxSat)
  if err != nil {
    return 0, 0, fmt.Errorf("max_htlc conversion failed: %w", err)
  }
  if maxMsat < minMsat {
    maxMsat = minMsat
  }
  return minMsat, maxMsat, nil
}

func satToMsat(sat int64) (uint64, error) {
  if sat < 0 {
    return 0, errors.New("negative sat value")
  }
  maxSat := uint64(math.MaxUint64 / 1000)
  if uint64(sat) > maxSat {
    return 0, errors.New("sat value out of range")
  }
  return uint64(sat) * 1000, nil
}

func minutesToHoursCeil(minutes int) int {
  if minutes <= 0 {
    return 0
  }
  return (minutes + 59) / 60
}

func withinMaxHTLCHysteresis(currentMaxMsat uint64, targetMaxMsat uint64, effectiveCapacitySat int64, volatileBalance bool) bool {
  diff := absUint64Diff(currentMaxMsat, targetMaxMsat)
  thresholdSat := htlcManagerMaxHysteresisBaseSat
  if effectiveCapacitySat > 0 {
    relSat := effectiveCapacitySat / htlcManagerMaxHysteresisDivisor
    if relSat > thresholdSat {
      thresholdSat = relSat
    }
  }
  if volatileBalance && thresholdSat > 0 {
    if thresholdSat > math.MaxInt64/htlcManagerVolatileHysteresisMultiplier {
      thresholdSat = math.MaxInt64
    } else {
      thresholdSat *= htlcManagerVolatileHysteresisMultiplier
    }
  }
  thresholdMsat, err := satToMsat(thresholdSat)
  if err != nil {
    return currentMaxMsat == targetMaxMsat
  }
  return diff <= thresholdMsat
}

func absUint64Diff(a uint64, b uint64) uint64 {
  if a >= b {
    return a - b
  }
  return b - a
}

func localReserveSat(ch lndclient.ChannelInfo) int64 {
  reserveSat := ch.LocalChanReserveSat
  if reserveSat < 0 {
    reserveSat = 0
  }
  if reserveSat > ch.CapacitySat {
    return ch.CapacitySat
  }
  return reserveSat
}

func maxHTLCUpperBoundSat(ch lndclient.ChannelInfo) int64 {
  upper := ch.CapacitySat - localReserveSat(ch)
  if upper < 0 {
    return 0
  }
  return upper
}

func spendableLocalSat(ch lndclient.ChannelInfo) int64 {
  local := ch.LocalBalanceSat - localReserveSat(ch)
  if local < 0 {
    return 0
  }
  upper := maxHTLCUpperBoundSat(ch)
  if local > upper {
    return upper
  }
  return local
}

func shortIdentifier(value string) string {
  trimmed := strings.TrimSpace(value)
  if trimmed == "" {
    return ""
  }
  if len(trimmed) <= 16 {
    return trimmed
  }
  return fmt.Sprintf("%s...%s", trimmed[:8], trimmed[len(trimmed)-8:])
}

func formatShortChanID(chanID uint64) string {
  if chanID == 0 {
    return ""
  }
  block := chanID >> 40
  tx := (chanID >> 16) & 0xFFFFFF
  out := chanID & 0xFFFF
  return fmt.Sprintf("%dx%dx%d", block, tx, out)
}

func normalizeFailureLabel(raw string) string {
  trimmed := strings.TrimSpace(raw)
  if trimmed == "" {
    return ""
  }
  return strings.ReplaceAll(trimmed, "_", " ")
}

func isContextCanceledError(err error) bool {
  if err == nil {
    return false
  }
  msg := strings.ToLower(strings.TrimSpace(err.Error()))
  return strings.Contains(msg, "context canceled") || strings.Contains(msg, "context cancelled")
}

func (m *HtlcManager) recordFailure(err error, changed int) {
  msg := strings.TrimSpace(err.Error())
  if msg == "" {
    msg = "htlc manager failed"
  }

  m.mu.Lock()
  m.lastError = msg
  m.lastErrorAt = time.Now().UTC()
  m.lastChangedCount = changed
  m.mu.Unlock()

  if m.logger != nil {
    m.logger.Printf("htlc-manager: %s", msg)
  }
}

func (m *HtlcManager) recordSuccess(changed int) {
  m.mu.Lock()
  hadErr := m.lastError != ""
  m.lastOK = time.Now().UTC()
  m.lastError = ""
  m.lastErrorAt = time.Time{}
  m.lastChangedCount = changed
  m.mu.Unlock()

  if hadErr && m.logger != nil {
    m.logger.Printf("htlc-manager: recovered")
  }
  if m.logger != nil {
    m.logger.Printf("htlc-manager: run completed with %d channel update(s)", changed)
  }
}

func parseHTLCManagerLogLimit(raw string) int {
  raw = strings.TrimSpace(raw)
  if raw == "" {
    return htlcManagerDefaultLogLimit
  }
  n, err := strconv.Atoi(raw)
  if err != nil {
    return htlcManagerDefaultLogLimit
  }
  if n <= 0 {
    return htlcManagerDefaultLogLimit
  }
  if n > htlcManagerMaxLogLimit {
    return htlcManagerMaxLogLimit
  }
  return n
}

func (s *Server) handleLNHTLCManagerGet(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.htlcManagerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "htlc manager unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }
  writeJSON(w, http.StatusOK, svc.Snapshot())
}

func (s *Server) handleLNHTLCManagerPost(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.htlcManagerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "htlc manager unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }

  var req struct {
    Enabled *bool `json:"enabled"`
    IntervalMinutes *int `json:"interval_minutes"`
    IntervalHours *int `json:"interval_hours"`
    MinHtlcSat *int64 `json:"min_htlc_sat"`
    MaxLocalPct *int `json:"max_local_pct"`
    RunNow bool `json:"run_now"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
  defer cancel()
  payload, err := svc.UpdateConfig(ctx, HtlcManagerConfigUpdate{
    Enabled: req.Enabled,
    IntervalMinutes: req.IntervalMinutes,
    IntervalHours: req.IntervalHours,
    MinHtlcSat: req.MinHtlcSat,
    MaxLocalPct: req.MaxLocalPct,
    RunNow: req.RunNow,
  })
  if err != nil {
    if errors.Is(err, errInvalidHTLCManagerConfig) {
      writeError(w, http.StatusBadRequest, err.Error())
      return
    }
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }
  writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleLNHTLCManagerLogs(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.htlcManagerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "htlc manager unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }
  limit := parseHTLCManagerLogLimit(r.URL.Query().Get("limit"))
  entries := svc.Logs(limit)
  writeJSON(w, http.StatusOK, map[string]any{
    "entries": entries,
  })
}

func (s *Server) handleLNHTLCManagerFailed(w http.ResponseWriter, r *http.Request) {
  svc, errMsg := s.htlcManagerService()
  if svc == nil {
    if errMsg == "" {
      errMsg = "htlc manager unavailable"
    }
    writeError(w, http.StatusServiceUnavailable, errMsg)
    return
  }
  limit := parseHTLCManagerLogLimit(r.URL.Query().Get("limit"))
  entries := svc.Failed(limit)
  if len(entries) > 0 && s.lnd != nil {
    ctx, cancel := context.WithTimeout(r.Context(), lndRPCTimeout)
    channels, err := s.lnd.ListChannels(ctx)
    cancel()
    if err == nil {
      aliasByChanID := make(map[string]string, len(channels))
      for _, ch := range channels {
        id := formatShortChanID(ch.ChannelID)
        if id == "" {
          continue
        }
        alias := strings.TrimSpace(ch.PeerAlias)
        if alias == "" {
          alias = shortIdentifier(ch.RemotePubkey)
        }
        if alias == "" {
          continue
        }
        aliasByChanID[id] = alias
      }
      for i := range entries {
        if entries[i].IncomingAlias == "" && entries[i].IncomingChannelID != "" {
          entries[i].IncomingAlias = aliasByChanID[entries[i].IncomingChannelID]
        }
        if entries[i].OutgoingAlias == "" && entries[i].OutgoingChannelID != "" {
          entries[i].OutgoingAlias = aliasByChanID[entries[i].OutgoingChannelID]
        }
      }
    }
  }
  writeJSON(w, http.StatusOK, map[string]any{
    "entries": entries,
  })
}

func localMinInt(a int, b int) int {
  if a < b {
    return a
  }
  return b
}

func localMaxInt(a int, b int) int {
  if a > b {
    return a
  }
  return b
}
