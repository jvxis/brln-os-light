package server

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	auditEventsRetentionEnv         = "AUDIT_EVENTS_RETENTION_DAYS"
	auditEventsDefaultRetentionDays = 365
	auditEventsRetentionInterval    = 24 * time.Hour
	auditEventsPruneTimeout         = 10 * time.Second
)

func (s *Server) startAuditLogRetention(svc *AuditService) {
	if s == nil || svc == nil || s.shutdownCtx == nil {
		return
	}
	retentionDays := auditEventsRetentionDays()
	if retentionDays <= 0 {
		return
	}
	s.auditRetentionOnce.Do(func() {
		go s.runAuditLogRetentionLoop(s.shutdownContext(), svc)
	})
}

func (s *Server) runAuditLogRetentionLoop(ctx context.Context, svc *AuditService) {
	s.pruneAuditLogRetention(ctx, svc)

	ticker := time.NewTicker(auditEventsRetentionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneAuditLogRetention(ctx, svc)
		}
	}
}

func (s *Server) pruneAuditLogRetention(ctx context.Context, svc *AuditService) {
	retentionDays := auditEventsRetentionDays()
	cutoff, ok := auditEventsRetentionCutoff(time.Now(), retentionDays)
	if !ok {
		return
	}

	pruneCtx, cancel := context.WithTimeout(ctx, auditEventsPruneTimeout)
	defer cancel()

	removed, err := svc.PruneBefore(pruneCtx, cutoff)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("audit log retention prune failed: %v", err)
		}
		return
	}
	if removed > 0 && s.logger != nil {
		s.logger.Printf("audit log retention prune: removed %d rows older than %s", removed, cutoff.Format(time.RFC3339))
	}
}

func auditEventsRetentionDays() int {
	return parseAuditEventsRetentionDays(os.Getenv(auditEventsRetentionEnv))
}

func parseAuditEventsRetentionDays(raw string) int {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return auditEventsDefaultRetentionDays
	}
	switch raw {
	case "0", "forever", "keep_forever", "keep-forever":
		return 0
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return auditEventsDefaultRetentionDays
	}
	return parsed
}

func auditEventsRetentionCutoff(now time.Time, retentionDays int) (time.Time, bool) {
	if retentionDays <= 0 {
		return time.Time{}, false
	}
	return now.UTC().AddDate(0, 0, -retentionDays), true
}
