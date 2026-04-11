package reports

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultLiveTTL                  = 60 * time.Second
	defaultLiveBuildTimeout         = 2 * time.Minute
	defaultLivePersistedFallbackAge = 15 * time.Minute
)

type Service struct {
	db     *pgxpool.Pool
	lnd    *lndclient.Client
	logger *log.Logger

	liveTTL          time.Duration
	liveBuildTimeout time.Duration
	liveMu           sync.Mutex
	liveCache        liveSnapshot
	liveInFlight     map[string]*liveCall
}

type liveSnapshot struct {
	ExpiresAt     time.Time
	UpdatedAt     time.Time
	Timezone      string
	Range         TimeRange
	Metrics       Metrics
	LookbackHours int
}

type liveCall struct {
	done     chan struct{}
	snapshot liveSnapshot
	err      error
}

func NewService(db *pgxpool.Pool, lnd *lndclient.Client, logger *log.Logger) *Service {
	return &Service{
		db:               db,
		lnd:              lnd,
		logger:           logger,
		liveTTL:          defaultLiveTTL,
		liveBuildTimeout: defaultLiveBuildTimeout,
		liveInFlight:     map[string]*liveCall{},
	}
}

func (s *Service) SetLiveBuildTimeout(timeout time.Duration) {
	if s == nil || timeout <= 0 {
		return
	}
	s.liveMu.Lock()
	s.liveBuildTimeout = timeout
	s.liveMu.Unlock()
}

func (s *Service) EnsureSchema(ctx context.Context) error {
	return EnsureSchema(ctx, s.db)
}

func (s *Service) RunDaily(ctx context.Context, reportDate time.Time, loc *time.Location, rebalanceOverride *RebalanceOverride, paymentOverride *PaymentOverride, keysendOverride *KeysendReceivedOverride, onchainOverride *OnchainOverride) (Row, error) {
	tr := BuildTimeRangeForDate(reportDate, loc)
	metrics, err := ComputeMetrics(ctx, s.lnd, tr, false, rebalanceOverride, paymentOverride, keysendOverride, onchainOverride)
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
		if err != nil {
			return Summary{}, dr, err
		}
		targetSat, err := FetchMovementTargetAllSum(ctx, s.db)
		if err != nil {
			return Summary{}, dr, err
		}
		summary.MovementTargetSat = targetSat
		summary.MovementPct = movementPct(summary.Totals, targetSat)
		return summary, dr, nil
	}
	summary, err := FetchSummaryRange(ctx, s.db, dr.StartDate, dr.EndDate)
	if err != nil {
		return Summary{}, dr, err
	}
	targetSat, err := FetchMovementTargetRangeSum(ctx, s.db, dr.StartDate, dr.EndDate)
	if err != nil {
		return Summary{}, dr, err
	}
	summary.MovementTargetSat = targetSat
	summary.MovementPct = movementPct(summary.Totals, targetSat)
	return summary, dr, nil
}

func (s *Service) CustomRange(ctx context.Context, startDate, endDate time.Time) ([]Row, error) {
	return FetchRange(ctx, s.db, startDate, endDate)
}

func (s *Service) CustomSummary(ctx context.Context, startDate, endDate time.Time) (Summary, error) {
	summary, err := FetchSummaryRange(ctx, s.db, startDate, endDate)
	if err != nil {
		return Summary{}, err
	}
	targetSat, err := FetchMovementTargetRangeSum(ctx, s.db, startDate, endDate)
	if err != nil {
		return Summary{}, err
	}
	summary.MovementTargetSat = targetSat
	summary.MovementPct = movementPct(summary.Totals, targetSat)
	return summary, nil
}

func (s *Service) Live(ctx context.Context, now time.Time, loc *time.Location, lookbackHours int) (TimeRange, Metrics, error) {
	if loc == nil {
		loc = time.Local
	}

	requestRange := BuildTimeRangeForLookback(now, loc, lookbackHours)
	key := liveSnapshotKey(loc, lookbackHours)
	if cached, ok := s.memoryLiveSnapshot(key); ok {
		return cached.Range, cached.Metrics, nil
	}

	call, leader := s.ensureLiveCall(key)
	if leader {
		go s.runLiveComputation(call, key, now, loc, lookbackHours)
	}

	if fallback, ok := s.usablePersistedLiveSnapshot(ctx, now, requestRange, loc, lookbackHours); ok {
		return fallback.Range, fallback.Metrics, nil
	}

	select {
	case <-call.done:
		if call.err == nil {
			return call.snapshot.Range, call.snapshot.Metrics, nil
		}
		if fallback, ok := s.usablePersistedLiveSnapshot(ctx, now, requestRange, loc, lookbackHours); ok {
			if s.logger != nil {
				s.logger.Printf("reports: live build failed, using persisted snapshot fallback: %v", call.err)
			}
			return fallback.Range, fallback.Metrics, nil
		}
		return TimeRange{}, Metrics{}, call.err
	case <-ctx.Done():
		if fallback, ok := s.usablePersistedLiveSnapshot(ctx, now, requestRange, loc, lookbackHours); ok {
			if s.logger != nil {
				s.logger.Printf("reports: live request timed out, using persisted snapshot fallback: %v", ctx.Err())
			}
			return fallback.Range, fallback.Metrics, nil
		}
		return TimeRange{}, Metrics{}, ctx.Err()
	}
}

func (s *Service) MovementLive(ctx context.Context, now time.Time, loc *time.Location) (MovementLive, error) {
	if loc == nil {
		loc = time.Local
	}
	reportDate := dateOnly(now, loc)
	targetSat, err := s.EnsureMovementTargetForDate(ctx, reportDate, loc)
	if err != nil {
		return MovementLive{}, err
	}
	tr, metrics, err := s.Live(ctx, now, loc, 0)
	if err != nil {
		return MovementLive{}, err
	}
	routed := routedVolumeSat(metrics)
	pct := 0.0
	if targetSat > 0 {
		pct = (routed / float64(targetSat)) * 100
	}
	return MovementLive{
		Date:            reportDate,
		Start:           tr.StartLocal,
		End:             tr.EndLocal,
		Timezone:        loc.String(),
		TargetSat:       targetSat,
		RoutedVolumeSat: routed,
		MovementPct:     pct,
	}, nil
}

func (s *Service) CaptureMovementTargetForDate(ctx context.Context, reportDate time.Time, loc *time.Location) (int64, error) {
	if s.lnd == nil {
		return 0, nil
	}
	if loc == nil {
		loc = time.Local
	}
	targetDate := dateOnly(reportDate, loc)
	targetSat, err := s.computeOutboundTarget(ctx)
	if err != nil {
		return 0, err
	}
	if err := UpsertMovementTargetDaily(ctx, s.db, targetDate, targetSat); err != nil {
		return 0, err
	}
	return targetSat, nil
}

func (s *Service) EnsureMovementTargetForDate(ctx context.Context, reportDate time.Time, loc *time.Location) (int64, error) {
	if loc == nil {
		loc = time.Local
	}
	targetDate := dateOnly(reportDate, loc)
	targetSat, ok, err := FetchMovementTargetDaily(ctx, s.db, targetDate)
	if err != nil {
		return 0, err
	}
	if ok {
		return targetSat, nil
	}
	return s.CaptureMovementTargetForDate(ctx, targetDate, loc)
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

func (s *Service) computeOutboundTarget(ctx context.Context) (int64, error) {
	if s.lnd == nil {
		return 0, nil
	}
	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, ch := range channels {
		total += ch.LocalBalanceSat
	}
	return total, nil
}

func movementPct(metrics Metrics, targetSat int64) float64 {
	if targetSat <= 0 {
		return 0
	}
	return (routedVolumeSat(metrics) / float64(targetSat)) * 100
}

func routedVolumeSat(metrics Metrics) float64 {
	if metrics.RoutedVolumeMsat != 0 {
		return float64(metrics.RoutedVolumeMsat) / 1000
	}
	return float64(metrics.RoutedVolumeSat)
}

func (s *Service) memoryLiveSnapshot(key string) (liveSnapshot, bool) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()

	cached := s.liveCache
	if cached.ExpiresAt.IsZero() || time.Now().After(cached.ExpiresAt) {
		return liveSnapshot{}, false
	}
	if liveSnapshotKeyParts(cached.Timezone, cached.LookbackHours) != key {
		return liveSnapshot{}, false
	}
	return cached, true
}

func (s *Service) ensureLiveCall(key string) (*liveCall, bool) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()

	if s.liveInFlight == nil {
		s.liveInFlight = map[string]*liveCall{}
	}
	if call, ok := s.liveInFlight[key]; ok {
		return call, false
	}

	call := &liveCall{done: make(chan struct{})}
	s.liveInFlight[key] = call
	return call, true
}

func (s *Service) runLiveComputation(call *liveCall, key string, now time.Time, loc *time.Location, lookbackHours int) {
	buildTimeout := s.liveBuildTimeout
	if buildTimeout <= 0 {
		buildTimeout = defaultLiveBuildTimeout
	}

	buildCtx, cancel := context.WithTimeout(context.Background(), buildTimeout)
	snapshot, err := s.computeLiveSnapshot(buildCtx, now, loc, lookbackHours)
	cancel()

	s.liveMu.Lock()
	if err == nil {
		s.liveCache = snapshot
	}
	call.snapshot = snapshot
	call.err = err
	if s.liveInFlight != nil {
		delete(s.liveInFlight, key)
	}
	close(call.done)
	s.liveMu.Unlock()

	if err == nil {
		go s.persistLiveSnapshot(snapshot)
	}
}

func (s *Service) computeLiveSnapshot(ctx context.Context, now time.Time, loc *time.Location, lookbackHours int) (liveSnapshot, error) {
	if loc == nil {
		loc = time.Local
	}

	tr := BuildTimeRangeForLookback(now, loc, lookbackHours)
	onchainOverride := OnchainOverride{}
	if quickOnchain, onchainErr := FetchOnchainFeeMetricsFast(ctx, s.lnd, tr.StartUnix(), tr.EndUnixInclusive()); onchainErr == nil {
		onchainOverride = quickOnchain
	} else if s.logger != nil {
		s.logger.Printf("reports: live onchain quick scan failed, defaulting to 0 onchain cost: %v", onchainErr)
	}

	metrics, err := ComputeMetrics(ctx, s.lnd, tr, false, nil, nil, nil, &onchainOverride)
	if err != nil {
		return liveSnapshot{}, err
	}
	metrics = s.attachBalances(ctx, metrics)

	builtAt := time.Now()
	return liveSnapshot{
		ExpiresAt:     builtAt.Add(s.liveTTL),
		UpdatedAt:     builtAt,
		Timezone:      liveSnapshotTimezone(loc),
		Range:         tr,
		Metrics:       metrics,
		LookbackHours: lookbackHours,
	}, nil
}

func (s *Service) usablePersistedLiveSnapshot(_ context.Context, now time.Time, requestRange TimeRange, loc *time.Location, lookbackHours int) (liveSnapshot, bool) {
	lookupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	snapshot, ok, err := FetchLiveSnapshot(lookupCtx, s.db, liveSnapshotTimezone(loc), lookbackHours)
	if err != nil {
		if s.logger != nil && !isContextError(err) {
			s.logger.Printf("reports: failed to load persisted live snapshot: %v", err)
		}
		return liveSnapshot{}, false
	}
	if !ok {
		return liveSnapshot{}, false
	}
	if !canUsePersistedLiveSnapshot(now, requestRange, snapshot, lookbackHours, loc) {
		return liveSnapshot{}, false
	}
	return snapshot, true
}

func (s *Service) persistLiveSnapshot(snapshot liveSnapshot) {
	if s == nil || s.db == nil {
		return
	}

	storeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := UpsertLiveSnapshot(storeCtx, s.db, snapshot); err != nil && s.logger != nil {
		s.logger.Printf("reports: failed to persist live snapshot: %v", err)
	}
}

func liveSnapshotTimezone(loc *time.Location) string {
	if loc == nil {
		return time.Local.String()
	}
	return loc.String()
}

func liveSnapshotKey(loc *time.Location, lookbackHours int) string {
	return liveSnapshotKeyParts(liveSnapshotTimezone(loc), lookbackHours)
}

func liveSnapshotKeyParts(timezone string, lookbackHours int) string {
	return timezone + "|" + strconv.Itoa(lookbackHours)
}

func canUsePersistedLiveSnapshot(now time.Time, requestRange TimeRange, snapshot liveSnapshot, lookbackHours int, loc *time.Location) bool {
	if loc == nil {
		loc = time.Local
	}
	if snapshot.Timezone != liveSnapshotTimezone(loc) || snapshot.LookbackHours != lookbackHours {
		return false
	}
	if snapshot.Range.StartLocal.IsZero() || snapshot.Range.EndLocal.IsZero() {
		return false
	}
	staleness := requestRange.EndLocal.Sub(snapshot.Range.EndLocal)
	if staleness > defaultLivePersistedFallbackAge {
		return false
	}
	if lookbackHours <= 0 {
		return dateOnly(snapshot.Range.StartLocal, loc).Equal(dateOnly(requestRange.StartLocal, loc))
	}
	if snapshot.UpdatedAt.IsZero() {
		return false
	}
	age := now.Sub(snapshot.UpdatedAt)
	return age <= defaultLivePersistedFallbackAge || age < 0
}

func isContextError(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}
