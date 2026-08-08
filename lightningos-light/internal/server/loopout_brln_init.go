package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const loopOutBRLNInitRetryCooldown = 10 * time.Second

func (s *Server) initLoopOutBRLN() {
	s.loopOutBRLNMu.Lock()
	defer s.loopOutBRLNMu.Unlock()
	if s.loopOutBRLN != nil && s.loopOutBRLNErr == "" {
		return
	}
	if !s.loopOutBRLNInitAt.IsZero() && time.Since(s.loopOutBRLNInitAt) < loopOutBRLNInitRetryCooldown {
		return
	}
	s.loopOutBRLNInitAt = time.Now()
	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.loopOutBRLNErr = fmt.Sprintf("Loop Out BRLN unavailable: %v", err)
		return
	}
	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.loopOutBRLNErr = fmt.Sprintf("Loop Out BRLN unavailable: failed to connect to postgres: %v", err)
			return
		}
		s.db = pool
	}
	svc := NewLoopOutBRLNService(pool, s.lnd, s.logger)
	if guard, _ := s.spendingGuardService(); guard != nil {
		svc.AttachSpendingGuard(guard)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.loopOutBRLNErr = fmt.Sprintf("Loop Out BRLN unavailable: failed to init schema: %v", err)
		return
	}
	s.loopOutBRLN = svc
	s.loopOutBRLNErr = ""
}

func (s *Server) loopOutBRLNService() (*LoopOutBRLNService, string) {
	s.initLoopOutBRLN()
	return s.loopOutBRLN, s.loopOutBRLNErr
}
