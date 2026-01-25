package reports

import (
  "context"
  "log"
  "sync"
  "time"

  "lightningos-light/internal/lndclient"

  "github.com/jackc/pgx/v5/pgxpool"
)

const defaultLiveTTL = 60 * time.Second

type Service struct {
  db *pgxpool.Pool
  lnd *lndclient.Client
  logger *log.Logger

  liveTTL time.Duration
  liveMu sync.Mutex
  liveCache liveSnapshot
}

type liveSnapshot struct {
  ExpiresAt time.Time
  Range TimeRange
  Metrics Metrics
  LookbackHours int
}

func NewService(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *Service {
  return &Service{
    db: db,
    lnd: lnd,
    logger: logger,
    liveTTL: defaultLiveTTL,
  }
}

func (s *Service) EnsureSchema(ctx context.Context) error {
  return EnsureSchema(ctx, s.db)
}

func (s *Service) RunDaily(ctx context.Context, reportDate time.Time, loc *time.Location) (Row, error) {
  tr := BuildTimeRangeForDate(reportDate, loc)
  metrics, err := ComputeMetrics(ctx, s.lnd, tr, false)
  if err != nil {
    return Row{}, err
  }
  if shouldAttachBalances(reportDate, loc) {
    metrics = s.attachBalances(ctx, metrics)
  }

  row := Row{ReportDate: dateOnly(reportDate, loc), Metrics: metrics}
  if err := UpsertDaily(ctx, s.db, row); err != nil {
    return Row{}, err
  }
  return row, nil
}

func (s *Service) Range(ctx context.Context, key string, now time.Time, loc *time.Location) ([]Row, DateRange, error) {
  dr, err := ResolveRangeWindow(now, loc, key)
  if err != nil {
    return nil, dr, err
  }
  if dr.All {
    items, err := FetchAll(ctx, s.db)
    return items, dr, err
  }
  items, err := FetchRange(ctx, s.db, dr.StartDate, dr.EndDate)
  return items, dr, err
}

func (s *Service) Summary(ctx context.Context, key string, now time.Time, loc *time.Location) (Summary, DateRange, error) {
  dr, err := ResolveRangeWindow(now, loc, key)
  if err != nil {
    return Summary{}, dr, err
  }
  if dr.All {
    summary, err := FetchSummaryAll(ctx, s.db)
    return summary, dr, err
  }
  summary, err := FetchSummaryRange(ctx, s.db, dr.StartDate, dr.EndDate)
  return summary, dr, err
}

func (s *Service) CustomRange(ctx context.Context, startDate, endDate time.Time) ([]Row, error) {
  return FetchRange(ctx, s.db, startDate, endDate)
}

func (s *Service) CustomSummary(ctx context.Context, startDate, endDate time.Time) (Summary, error) {
  return FetchSummaryRange(ctx, s.db, startDate, endDate)
}

func (s *Service) Live(ctx context.Context, now time.Time, loc *time.Location, lookbackHours int) (TimeRange, Metrics, error) {
  if loc == nil {
    loc = time.Local
  }
  s.liveMu.Lock()
  cached := s.liveCache
  if time.Now().Before(cached.ExpiresAt) && cached.LookbackHours == lookbackHours {
    s.liveMu.Unlock()
    return cached.Range, cached.Metrics, nil
  }
  s.liveMu.Unlock()

  tr := BuildTimeRangeForLookback(now, loc, lookbackHours)
  metrics, err := ComputeMetrics(ctx, s.lnd, tr, false)
  if err != nil {
    return TimeRange{}, Metrics{}, err
  }
  metrics = s.attachBalances(ctx, metrics)

  s.liveMu.Lock()
  s.liveCache = liveSnapshot{
    ExpiresAt: time.Now().Add(s.liveTTL),
    Range: tr,
    Metrics: metrics,
    LookbackHours: lookbackHours,
  }
  s.liveMu.Unlock()

  return tr, metrics, nil
}

func shouldAttachBalances(reportDate time.Time, loc *time.Location) bool {
  if loc == nil {
    loc = time.Local
  }
  today := dateOnly(time.Now(), loc)
  target := dateOnly(reportDate, loc)
  return target.Equal(today.AddDate(0, 0, -1))
}

func (s *Service) attachBalances(ctx context.Context, metrics Metrics) Metrics {
  if s.lnd == nil {
    return metrics
  }
  balCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
  defer cancel()

  summary, err := s.lnd.GetBalances(balCtx)
  if err != nil {
    if s.logger != nil {
      s.logger.Printf("reports: balances unavailable: %v", err)
    }
    return metrics
  }

  onchain := summary.OnchainSat
  lightning := summary.LightningSat
  total := summary.OnchainSat + summary.LightningSat

  metrics.OnchainBalanceSat = &onchain
  metrics.LightningBalanceSat = &lightning
  metrics.TotalBalanceSat = &total
  return metrics
}
