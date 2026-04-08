package server

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/reports"
)

const (
	reportsLiveWarmIntervalEnv     = "REPORTS_LIVE_WARM_INTERVAL_SEC"
	reportsLiveWarmDefaultInterval = 15 * time.Minute
	reportsLiveWarmDefaultTimeout  = 2 * time.Minute
)

func (s *Server) runReportsLiveWarmup(svc *reports.Service) {
	if s == nil || svc == nil {
		return
	}

	interval := reportsLiveWarmInterval()
	if interval <= 0 {
		if s.logger != nil {
			s.logger.Printf("reports: live warmup disabled")
		}
		return
	}
	if s.logger != nil {
		s.logger.Printf("reports: live warmup enabled (interval %s)", interval)
	}

	warm := func() {
		now := time.Now()
		loc := s.reportsLocation()
		timeout := reportsLiveWarmTimeout()

		targetCtx, cancelTarget := context.WithTimeout(context.Background(), timeout)
		if _, err := svc.EnsureMovementTargetForDate(targetCtx, now, loc); err != nil && s.logger != nil {
			s.logger.Printf("reports: movement live warmup failed: %v", err)
		}
		cancelTarget()

		for _, lookbackHours := range reportsLiveWarmLookbacks() {
			liveCtx, cancelLive := context.WithTimeout(context.Background(), timeout)
			_, _, err := svc.Live(liveCtx, now, loc, lookbackHours)
			cancelLive()
			if err != nil && s.logger != nil {
				s.logger.Printf("reports: live warmup failed for lookback=%dh: %v", lookbackHours, err)
			}
		}
	}

	warm()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		warm()
	}
}

func reportsLiveWarmLookbacks() []int {
	lookbacks := []int{0}
	if configured := reportsLiveLookbackHours(); configured > 0 {
		lookbacks = append(lookbacks, configured)
	}
	return lookbacks
}

func reportsLiveWarmInterval() time.Duration {
	raw := strings.TrimSpace(os.Getenv(reportsLiveWarmIntervalEnv))
	if raw == "" {
		return reportsLiveWarmDefaultInterval
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return reportsLiveWarmDefaultInterval
	}
	if parsed <= 0 {
		return 0
	}
	return time.Duration(parsed) * time.Second
}

func reportsLiveWarmTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("REPORTS_RUN_TIMEOUT_SEC"))
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		return time.Duration(parsed) * time.Second
	}
	if liveTimeout := reportsLiveTimeout(); liveTimeout > reportsLiveWarmDefaultTimeout {
		return liveTimeout
	}
	return reportsLiveWarmDefaultTimeout
}
