package server

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/electrs"
)

const provenanceMetricsSampleLimit = 256

type ProvenanceMetrics struct {
	mu      sync.Mutex
	buckets map[string]*provenanceMetricsBucket
}

type provenanceMetricsBucket struct {
	source            string
	labels            map[string]struct{}
	callsTotal        uint64
	hitsTotal         uint64
	errorsTotal       uint64
	unavailableTotal  uint64
	fallthroughsTotal uint64
	lastError         string
	lastErrorAt       time.Time
	latenciesMs       []int64
	nextLatency       int
}

type ProvenanceMetricsSnapshot struct {
	GeneratedAt       time.Time                         `json:"generated_at"`
	FallthroughsTotal uint64                            `json:"fallthroughs_total"`
	Sources           []ProvenanceSourceMetricsSnapshot `json:"sources"`
}

type ProvenanceSourceMetricsSnapshot struct {
	Source            string     `json:"source"`
	Labels            []string   `json:"labels,omitempty"`
	CallsTotal        uint64     `json:"calls_total"`
	HitsTotal         uint64     `json:"hits_total"`
	ErrorsTotal       uint64     `json:"errors_total"`
	UnavailableTotal  uint64     `json:"unavailable_total"`
	FallthroughsTotal uint64     `json:"fallthroughs_total"`
	LatencyMSP95      int64      `json:"latency_ms_p95"`
	RecentSamples     int        `json:"recent_samples"`
	LastError         string     `json:"last_error,omitempty"`
	LastErrorAt       *time.Time `json:"last_error_at,omitempty"`
}

type provenanceMetricsSource struct {
	source  electrs.TxSource
	metrics *ProvenanceMetrics
	hasNext bool
}

func NewProvenanceMetrics() *ProvenanceMetrics {
	return &ProvenanceMetrics{buckets: make(map[string]*provenanceMetricsBucket)}
}

func wrapProvenanceSourcesWithMetrics(sources []electrs.TxSource, metrics *ProvenanceMetrics) []electrs.TxSource {
	if metrics == nil || len(sources) == 0 {
		return sources
	}
	out := make([]electrs.TxSource, 0, len(sources))
	for i, src := range sources {
		if src == nil {
			continue
		}
		metrics.RegisterSource(src.Name())
		out = append(out, &provenanceMetricsSource{
			source:  src,
			metrics: metrics,
			hasNext: i < len(sources)-1,
		})
	}
	return out
}

func (m *ProvenanceMetrics) RegisterSource(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucketLocked(name)
	if label := strings.TrimSpace(name); label != "" {
		bucket.labels[label] = struct{}{}
	}
}

func (m *ProvenanceMetrics) RecordSourceCall(name string, latency time.Duration, err error, shouldFallThrough bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	bucket := m.bucketLocked(name)
	bucket.callsTotal++
	bucket.recordLatency(latency)
	if label := strings.TrimSpace(name); label != "" {
		bucket.labels[label] = struct{}{}
	}
	if err == nil {
		bucket.hitsTotal++
		return
	}

	bucket.errorsTotal++
	bucket.lastError = err.Error()
	bucket.lastErrorAt = time.Now().UTC()
	if errors.Is(err, electrs.ErrSourceUnavailable) {
		bucket.unavailableTotal++
	}
	if shouldFallThrough {
		bucket.fallthroughsTotal++
	}
}

func (m *ProvenanceMetrics) Snapshot() ProvenanceMetricsSnapshot {
	snapshot := ProvenanceMetricsSnapshot{GeneratedAt: time.Now().UTC()}
	if m == nil {
		return snapshot
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sources := make([]string, 0, len(m.buckets))
	for source := range m.buckets {
		sources = append(sources, source)
	}
	sort.Strings(sources)

	for _, source := range sources {
		bucket := m.buckets[source]
		var lastErrorAt *time.Time
		if !bucket.lastErrorAt.IsZero() {
			value := bucket.lastErrorAt
			lastErrorAt = &value
		}
		entry := ProvenanceSourceMetricsSnapshot{
			Source:            bucket.source,
			Labels:            sortedProvenanceMetricLabels(bucket.labels),
			CallsTotal:        bucket.callsTotal,
			HitsTotal:         bucket.hitsTotal,
			ErrorsTotal:       bucket.errorsTotal,
			UnavailableTotal:  bucket.unavailableTotal,
			FallthroughsTotal: bucket.fallthroughsTotal,
			LatencyMSP95:      percentileInt64(bucket.latencySamplesLocked(), 95),
			RecentSamples:     len(bucket.latenciesMs),
			LastError:         bucket.lastError,
			LastErrorAt:       lastErrorAt,
		}
		snapshot.FallthroughsTotal += bucket.fallthroughsTotal
		snapshot.Sources = append(snapshot.Sources, entry)
	}
	return snapshot
}

func (m *ProvenanceMetrics) bucketLocked(name string) *provenanceMetricsBucket {
	if m.buckets == nil {
		m.buckets = make(map[string]*provenanceMetricsBucket)
	}
	source := provenanceSourceClass(name)
	bucket := m.buckets[source]
	if bucket == nil {
		bucket = &provenanceMetricsBucket{
			source: source,
			labels: make(map[string]struct{}),
		}
		m.buckets[source] = bucket
	}
	return bucket
}

func (b *provenanceMetricsBucket) recordLatency(latency time.Duration) {
	ms := latency.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if len(b.latenciesMs) < provenanceMetricsSampleLimit {
		b.latenciesMs = append(b.latenciesMs, ms)
		return
	}
	b.latenciesMs[b.nextLatency] = ms
	b.nextLatency = (b.nextLatency + 1) % provenanceMetricsSampleLimit
}

func (b *provenanceMetricsBucket) latencySamplesLocked() []int64 {
	out := append([]int64(nil), b.latenciesMs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *provenanceMetricsSource) Name() string {
	if s == nil || s.source == nil {
		return "unknown"
	}
	return s.source.Name()
}

func (s *provenanceMetricsSource) Available(ctx context.Context) bool {
	if s == nil || s.source == nil {
		return false
	}
	return s.source.Available(ctx)
}

func (s *provenanceMetricsSource) GetTransaction(ctx context.Context, txid string) (electrs.VerboseTx, error) {
	if s == nil || s.source == nil {
		return electrs.VerboseTx{}, electrs.ErrSourceUnavailable
	}
	start := time.Now()
	tx, err := s.source.GetTransaction(ctx, txid)
	shouldFallThrough := s.hasNext && errors.Is(err, electrs.ErrSourceUnavailable)
	s.metrics.RecordSourceCall(s.source.Name(), time.Since(start), err, shouldFallThrough)
	return tx, err
}

func provenanceSourceClass(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case name == "bitcoind", strings.Contains(name, "bitcoin core"):
		return "bitcoind"
	case strings.HasPrefix(name, "public:"):
		return "public"
	case strings.Contains(name, "electrs"), strings.HasPrefix(name, "electrum"):
		return "electrs"
	case name == "":
		return "unknown"
	default:
		return name
	}
}

func sortedProvenanceMetricLabels(labels map[string]struct{}) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	for label := range labels {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}

func percentileInt64(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if percentile <= 0 {
		return sorted[0]
	}
	if percentile >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := (percentile*len(sorted) + 99) / 100
	if idx <= 0 {
		return sorted[0]
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}
