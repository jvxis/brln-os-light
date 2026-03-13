package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const closeManagerInitRetryCooldown = 10 * time.Second

func (s *Server) initCloseManager() {
	s.closeManagerMu.Lock()
	defer s.closeManagerMu.Unlock()

	if s.closeManager != nil && s.closeManagerErr == "" {
		return
	}
	if !s.closeManagerInitAt.IsZero() && time.Since(s.closeManagerInitAt) < closeManagerInitRetryCooldown {
		return
	}
	s.closeManagerInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.closeManagerErr = fmt.Sprintf("close manager unavailable: %v", err)
		s.logger.Printf("%s", s.closeManagerErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.closeManagerErr = fmt.Sprintf("close manager unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.closeManagerErr)
			return
		}
		s.db = pool
	}

	svc := NewCloseManagerService(pool, s.logger, s.lnd, s.notifier)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.closeManagerErr = fmt.Sprintf("close manager unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.closeManagerErr)
		return
	}

	s.closeManager = svc
	s.closeManagerErr = ""
}

func (s *Server) closeManagerService() (*CloseManagerService, string) {
	s.initCloseManager()
	return s.closeManager, s.closeManagerErr
}
