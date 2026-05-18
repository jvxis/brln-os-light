package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const auditLogInitRetryCooldown = 10 * time.Second

func (s *Server) initAuditLog() {
	s.auditLogMu.Lock()
	defer s.auditLogMu.Unlock()

	if s.auditLog != nil && s.auditLogErr == "" {
		return
	}
	if !s.auditLogInitAt.IsZero() && time.Since(s.auditLogInitAt) < auditLogInitRetryCooldown {
		return
	}
	s.auditLogInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.auditLogErr = fmt.Sprintf("audit log unavailable: %v", err)
		s.logger.Printf("%s", s.auditLogErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.auditLogErr = fmt.Sprintf("audit log unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.auditLogErr)
			return
		}
		s.db = pool
	}

	svc := NewAuditService(pool, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.auditLogErr = fmt.Sprintf("audit log unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.auditLogErr)
		return
	}

	s.auditLog = svc
	s.auditLogErr = ""
	s.startAuditLogRetention(svc)
}

func (s *Server) auditLogService() (*AuditService, string) {
	s.initAuditLog()
	return s.auditLog, s.auditLogErr
}
