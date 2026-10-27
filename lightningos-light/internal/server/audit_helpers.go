package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		return auditCorrelationID(session.ID)
	}
	return "anonymous"
}

const auditSessionPrefix = "audit:v1:"
const auditSessionDomain = "lightningos:audit-session:v1:"
const auditSessionPattern = `^audit:v1:[0-9a-f]{64}$`

// Session cookies are bearer credentials. Audit correlation must never carry
// them, including when a caller supplies a legacy event directly to Insert.
func auditCorrelationID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || sessionID == "anonymous" {
		return "anonymous"
	}
	if strings.HasPrefix(sessionID, auditSessionPrefix) && len(sessionID) == len(auditSessionPrefix)+64 {
		suffix := strings.TrimPrefix(sessionID, auditSessionPrefix)
		if _, err := hex.DecodeString(suffix); err == nil && strings.ToLower(suffix) == suffix {
			return sessionID
		}
	}
	digest := sha256.Sum256([]byte(auditSessionDomain + sessionID))
	return auditSessionPrefix + hex.EncodeToString(digest[:])
}

func auditClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	// No trusted-proxy configuration exists. Record the transport peer rather
	// than accepting a client-controlled forwarding header as evidence.
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
