package server

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	lndGraphProgressCacheTTL = 30 * time.Second
	lndGraphProgressTimeout  = 3 * time.Second
	lndGraphProgressLogLimit = 5000
	lndGraphRateWindow       = 15 * time.Minute
	lndGraphRateMinSample    = 2 * time.Minute
)

var (
	lndGraphFilteringPattern = regexp.MustCompile(
		`GossipSyncer\(([0-9a-fA-F]+)\): filtering through ([0-9]+) chans`,
	)
	lndGraphQueryPattern = regexp.MustCompile(
		`GossipSyncer\(([0-9a-fA-F]+)\): starting query for ([0-9]+) new chans`,
	)
	lndGraphCompletePattern = regexp.MustCompile(
		`GossipSyncer\(([0-9a-fA-F]+)\): no more chans to query`,
	)
	lndChainBlockPattern = regexp.MustCompile(
		`New block: height=([0-9]+)`,
	)
)

type lndGraphSyncProgress struct {
	ProgressPercent   float64 `json:"progress_percent"`
	KnownChannels     int64   `json:"known_channels,omitempty"`
	TotalChannels     int64   `json:"total_channels,omitempty"`
	RemainingChannels int64   `json:"remaining_channels,omitempty"`
	ChannelsPerHour   float64 `json:"channels_per_hour,omitempty"`
	ETASeconds        int64   `json:"eta_seconds,omitempty"`
	Approximate       bool    `json:"approximate"`
}

type lndGraphProgressSample struct {
	CheckedAt     time.Time
	KnownChannels int64
}

type lndGraphProgressCache struct {
	Service     string
	Invocation  string
	CheckedAt   time.Time
	Progress    *lndGraphSyncProgress
	BlockHeight int64
	Samples     []lndGraphProgressSample
}

func activeLNDService(ctx context.Context) string {
	if system.SystemctlIsActive(ctx, "lnd") {
		return "lnd"
	}
	if system.SystemctlIsActive(ctx, "lnd@default") {
		return "lnd@default"
	}
	return ""
}

func (s *Server) startLNDGraphProgressWarmup() {
	if s == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(s.shutdownContext(), lndGraphProgressTimeout)
		service := activeLNDService(ctx)
		cancel()
		if service == "" {
			return
		}

		s.lndGraphProgressMu.Lock()
		if s.lndGraphProgressRefreshing {
			s.lndGraphProgressMu.Unlock()
			return
		}
		s.lndGraphProgressRefreshing = true
		s.lndGraphProgressMu.Unlock()
		s.refreshLNDGraphProgress(service)
	}()
}

func (s *Server) graphSyncProgress(service string, synced bool) *lndGraphSyncProgress {
	if synced {
		return &lndGraphSyncProgress{ProgressPercent: 100}
	}
	if s == nil || strings.TrimSpace(service) == "" {
		return nil
	}

	now := time.Now()
	s.lndGraphProgressMu.Lock()
	cache := s.lndGraphProgressCache
	if cache.Service == service && now.Sub(cache.CheckedAt) < lndGraphProgressCacheTTL {
		progress := cloneLNDGraphProgress(cache.Progress)
		s.lndGraphProgressMu.Unlock()
		return progress
	}
	if !s.lndGraphProgressRefreshing {
		s.lndGraphProgressRefreshing = true
		go s.refreshLNDGraphProgress(service)
	}
	progress := cloneLNDGraphProgress(cache.Progress)
	if cache.Service != service {
		progress = nil
	}
	s.lndGraphProgressMu.Unlock()
	return progress
}

func (s *Server) refreshLNDGraphProgress(service string) {
	ctx, cancel := context.WithTimeout(s.shutdownContext(), lndGraphProgressTimeout)
	defer cancel()

	invocation, _ := lndServiceInvocation(ctx, service)
	lines, err := lndGraphProgressLines(ctx, service, invocation)
	progress, ok := parseLNDGraphProgress(lines)
	blockHeight := parseLNDJournalBlockHeight(lines)
	now := time.Now()

	s.lndGraphProgressMu.Lock()
	defer s.lndGraphProgressMu.Unlock()
	s.lndGraphProgressRefreshing = false

	cache := s.lndGraphProgressCache
	if err != nil {
		if cache.Service == service {
			s.lndGraphProgressCache.CheckedAt = now
		}
		return
	}

	progress, ok, preserved := mergeLNDGraphProgress(cache, service, invocation, progress, ok)
	if cache.Service == service && cache.Invocation == invocation && cache.BlockHeight > blockHeight {
		blockHeight = cache.BlockHeight
	}

	samples := []lndGraphProgressSample(nil)
	if ok {
		if cache.Service == service && cache.Invocation == invocation {
			samples = cache.Samples
		}
		if !preserved {
			samples = graphProgressSamples(samples, progress.KnownChannels, now)
		}
		applyGraphProgressRate(progress, samples)
	}

	s.lndGraphProgressCache = lndGraphProgressCache{
		Service:     service,
		Invocation:  invocation,
		CheckedAt:   now,
		Progress:    progress,
		BlockHeight: blockHeight,
		Samples:     samples,
	}
}

func mergeLNDGraphProgress(
	cache lndGraphProgressCache,
	service, invocation string,
	progress *lndGraphSyncProgress,
	ok bool,
) (*lndGraphSyncProgress, bool, bool) {
	sameInvocation := cache.Service == service && cache.Invocation == invocation
	if !sameInvocation || cache.Progress == nil {
		return progress, ok, false
	}
	if !ok || progress == nil || cache.Progress.KnownChannels > progress.KnownChannels {
		return cloneLNDGraphProgress(cache.Progress), true, true
	}
	return progress, true, false
}

func (s *Server) lndJournalBlockHeight(service string) int64 {
	if s == nil || strings.TrimSpace(service) == "" {
		return 0
	}
	s.lndGraphProgressMu.Lock()
	defer s.lndGraphProgressMu.Unlock()
	if s.lndGraphProgressCache.Service != service {
		return 0
	}
	return s.lndGraphProgressCache.BlockHeight
}

func graphProgressSamples(samples []lndGraphProgressSample, known int64, now time.Time) []lndGraphProgressSample {
	cutoff := now.Add(-lndGraphRateWindow)
	kept := make([]lndGraphProgressSample, 0, len(samples)+1)
	for _, sample := range samples {
		if !sample.CheckedAt.Before(cutoff) && sample.KnownChannels <= known {
			kept = append(kept, sample)
		}
	}
	kept = append(kept, lndGraphProgressSample{
		CheckedAt:     now,
		KnownChannels: known,
	})
	return kept
}

func applyGraphProgressRate(progress *lndGraphSyncProgress, samples []lndGraphProgressSample) {
	if progress == nil {
		return
	}
	progress.ChannelsPerHour = 0
	progress.ETASeconds = 0
	if progress.RemainingChannels <= 0 || len(samples) < 2 {
		return
	}
	first := samples[0]
	last := samples[len(samples)-1]
	elapsed := last.CheckedAt.Sub(first.CheckedAt)
	delta := last.KnownChannels - first.KnownChannels
	if elapsed < lndGraphRateMinSample || delta <= 0 {
		return
	}

	rate := float64(delta) / elapsed.Hours()
	progress.ChannelsPerHour = math.Round(rate)
	if progress.ChannelsPerHour <= 0 {
		return
	}
	progress.ETASeconds = int64(math.Ceil(
		float64(progress.RemainingChannels) / progress.ChannelsPerHour * 3600,
	))
}

func lndServiceInvocation(ctx context.Context, service string) (string, error) {
	out, err := system.RunCommand(
		ctx, "systemctl", "show", service, "--property=InvocationID", "--value",
	)
	if err != nil {
		return "", err
	}
	invocation := strings.ToLower(strings.TrimSpace(out))
	if matched, _ := regexp.MatchString(`^[0-9a-f]{32}$`, invocation); !matched {
		return "", fmt.Errorf("invalid invocation id")
	}
	return invocation, nil
}

func lndGraphProgressLines(ctx context.Context, service, invocation string) ([]string, error) {
	args := lndGraphProgressJournalArgs(service, invocation)
	out, err := system.RunCommand(ctx, "journalctl", args...)
	if err != nil {
		lines, fallbackErr := system.JournalTailSince(
			ctx, service, 5000, "-12 hours",
		)
		if fallbackErr != nil {
			return nil, fmt.Errorf("read lnd graph sync journal: %w", err)
		}
		return lines, nil
	}

	return splitNonEmptyLines(out), nil
}

func lndGraphProgressJournalArgs(service, invocation string) []string {
	args := make([]string, 0, 9)
	if invocation != "" {
		// Field matches must precede options for compatibility across systemd
		// versions. Reading from the tail avoids scanning days of busy LND logs.
		args = append(args, "_SYSTEMD_INVOCATION_ID="+invocation)
	}
	args = append(args,
		"-u", service,
		"--no-pager", "--output=cat",
		"-n", strconv.Itoa(lndGraphProgressLogLimit),
	)
	return args
}

func splitNonEmptyLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

func parseLNDGraphProgress(lines []string) (*lndGraphSyncProgress, bool) {
	peerTotals := make(map[string]int64)
	peerRemaining := make(map[string]int64)
	var totalChannels int64
	var knownChannels int64
	complete := false

	for _, line := range lines {
		if match := lndGraphFilteringPattern.FindStringSubmatch(line); len(match) == 3 {
			total, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil || total <= 0 {
				continue
			}
			peer := strings.ToLower(match[1])
			if total > peerTotals[peer] {
				peerTotals[peer] = total
			}
			if total > totalChannels {
				totalChannels = total
			}
			continue
		}

		if match := lndGraphQueryPattern.FindStringSubmatch(line); len(match) == 3 {
			peer := strings.ToLower(match[1])
			remaining, err := strconv.ParseInt(match[2], 10, 64)
			if err != nil || remaining < 0 {
				continue
			}
			previous, exists := peerRemaining[peer]
			if !exists || remaining < previous {
				peerRemaining[peer] = remaining
			}
			continue
		}

		if lndGraphCompletePattern.MatchString(line) {
			complete = true
		}
	}
	for peer, total := range peerTotals {
		remaining, exists := peerRemaining[peer]
		if !exists || remaining > total {
			continue
		}
		known := total - remaining
		if known > knownChannels {
			knownChannels = known
		}
	}

	if complete {
		return &lndGraphSyncProgress{
			ProgressPercent:   100,
			KnownChannels:     totalChannels,
			TotalChannels:     totalChannels,
			RemainingChannels: 0,
			Approximate:       true,
		}, true
	}
	if totalChannels <= 0 {
		return nil, false
	}
	if knownChannels > totalChannels {
		knownChannels = totalChannels
	}

	percent := math.Round((float64(knownChannels)/float64(totalChannels))*1000) / 10
	return &lndGraphSyncProgress{
		ProgressPercent:   percent,
		KnownChannels:     knownChannels,
		TotalChannels:     totalChannels,
		RemainingChannels: totalChannels - knownChannels,
		Approximate:       true,
	}, true
}

func parseLNDJournalBlockHeight(lines []string) int64 {
	var height int64
	for _, line := range lines {
		match := lndChainBlockPattern.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		value, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && value > height {
			height = value
		}
	}
	return height
}

func cloneLNDGraphProgress(progress *lndGraphSyncProgress) *lndGraphSyncProgress {
	if progress == nil {
		return nil
	}
	clone := *progress
	return &clone
}
