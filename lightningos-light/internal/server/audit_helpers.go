package server

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const auditInsertTimeout = 3 * time.Second

const (
	auditEventsDefaultLimit = 100
	auditEventsMaxLimit     = 500
)

func (s *Server) recordAuditEvent(r *http.Request, action string, target string, metadata any) {
	if s == nil {
		return
	}
	parent := context.Background()
	if r != nil {
		parent = r.Context()
	}
	s.insertAuditEvent(parent, AuditEventInsert{
		SessionID: auditSessionID(r),
		Action:    action,
		Target:    target,
		IP:        auditClientIP(r),
		Metadata:  metadata,
	})
}

func (s *Server) recordAuditEventAsync(r *http.Request, action string, target string, metadata any) {
	if s == nil {
		return
	}
	event := AuditEventInsert{
		SessionID: auditSessionID(r),
		Action:    action,
		Target:    target,
		IP:        auditClientIP(r),
		Metadata:  metadata,
	}
	go s.insertAuditEvent(context.Background(), event)
}

func (s *Server) insertAuditEvent(parent context.Context, event AuditEventInsert) {
	if s == nil {
		return
	}
	svc, errMsg := s.auditLogService()
	if svc == nil {
		if s.logger != nil && strings.TrimSpace(errMsg) != "" {
			s.logger.Printf("%s", errMsg)
		}
		return
	}

	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, auditInsertTimeout)
	defer cancel()

	if err := svc.Insert(ctx, event); err != nil && s.logger != nil {
		s.logger.Printf("audit log insert failed action=%s target=%s: %v", event.Action, event.Target, err)
	}
}

func auditSessionID(r *http.Request) string {
	if r == nil {
		return "anonymous"
	}
	if session, ok := authSessionFromContext(r.Context()); ok && strings.TrimSpace(session.ID) != "" {
		return session.ID
	}
	return "anonymous"
}

func auditClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		for _, item := range strings.Split(forwarded, ",") {
			if ip := strings.TrimSpace(item); ip != "" {
				return ip
			}
		}
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func auditTargetForOutpoints(outpoints []string) string {
	cleaned := normalizeOutpointBatch(outpoints)
	if len(cleaned) == 1 {
		return cleaned[0]
	}
	if len(cleaned) == 0 {
		return ""
	}
	return "batch:" + strconv.Itoa(len(cleaned))
}

func auditStatusFromResult(total int, errorCount int) string {
	if errorCount <= 0 {
		return "success"
	}
	if errorCount >= total {
		return "error"
	}
	return "partial_error"
}

func normalizeAuditEventsLimit(limit int) int {
	if limit <= 0 {
		return auditEventsDefaultLimit
	}
	if limit > auditEventsMaxLimit {
		return auditEventsMaxLimit
	}
	return limit
}
