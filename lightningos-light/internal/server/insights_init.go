package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const insightsInitRetryCooldown = 10 * time.Second

func (s *Server) initInsights() {
	s.insightsMu.Lock()
	defer s.insightsMu.Unlock()
	if s.insights != nil && s.insightsErr == "" {
		return
	}
	if !s.insightsInitAt.IsZero() && time.Since(s.insightsInitAt) < insightsInitRetryCooldown {
		return
	}
	s.insightsInitAt = time.Now()
	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.insightsErr = fmt.Sprintf("insights unavailable: %v", err)
		s.logger.Printf("%s", s.insightsErr)
		return
	}
	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.insightsErr = fmt.Sprintf("insights unavailable: %v", err)
			s.logger.Printf("%s", s.insightsErr)
			return
		}
		s.db = pool
	}
	svc := NewInsightsService(pool, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.insightsErr = fmt.Sprintf("insights unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.insightsErr)
		return
	}
	s.insights = svc
	s.insightsErr = ""
}

func (s *Server) insightsService() (*InsightsService, string) {
	s.initInsights()
	return s.insights, s.insightsErr
}
