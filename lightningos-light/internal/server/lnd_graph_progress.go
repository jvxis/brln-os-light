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
	lndGraphProgressTimeout  = 2 * time.Second
	lndGraphProgressLogLimit = 1000
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
	Service    string
	Invocation string
	CheckedAt  time.Time
	Progress   *lndGraphSyncProgress
	Samples    []lndGraphProgressSample
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

	if ok && cache.Service == service && cache.Invocation == invocation &&
		cache.Progress != nil &&
		cache.Progress.KnownChannels > progress.KnownChannels {

		progress = cloneLNDGraphProgress(cache.Progress)
	}

	samples := []lndGraphProgressSample(nil)
	if ok {
		if cache.Service == service && cache.Invocation == invocation {
			samples = cache.Samples
		}
		samples = graphProgressSamples(samples, progress.KnownChannels, now)
		applyGraphProgressRate(progress, samples)
	}

	s.lndGraphProgressCache = lndGraphProgressCache{
		Service:    service,
		Invocation: invocation,
		CheckedAt:  now,
		Progress:   progress,
		Samples:    samples,
	}
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
	grep := `GossipSyncer\([0-9a-fA-F]+\): (filtering through [0-9]+ chans|starting query for [0-9]+ new chans|no more chans to query)`
	args := make([]string, 0, 13)
	if invocation != "" {
		// Field matches must precede options for compatibility with journalctl
		// versions that stop recognizing positional matches after --grep.
		args = append(args, "_SYSTEMD_INVOCATION_ID="+invocation)
	}
	args = append(args,
		"-u", service, "--since", "-7 days",
		"--no-pager", "--output=cat", "--grep", grep,
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

func cloneLNDGraphProgress(progress *lndGraphSyncProgress) *lndGraphSyncProgress {
	if progress == nil {
		return nil
	}
	clone := *progress
	return &clone
}
