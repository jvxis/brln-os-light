package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const balancedOpenInitRetryCooldown = 10 * time.Second

func (s *Server) initBalancedOpen() {
	s.balancedOpenMu.Lock()
	defer s.balancedOpenMu.Unlock()

	if !s.cfg.Features.BalancedOpenEnabled() {
		s.balancedOpen = nil
		s.balancedOpenErr = "balanced open disabled"
		return
	}

	if s.balancedOpen != nil && s.balancedOpenErr == "" {
		return
	}
	if !s.balancedOpenInitAt.IsZero() && time.Since(s.balancedOpenInitAt) < balancedOpenInitRetryCooldown {
		return
	}
	s.balancedOpenInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.balancedOpenErr = fmt.Sprintf("balanced open unavailable: %v", err)
		s.logger.Printf("%s", s.balancedOpenErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.balancedOpenErr = fmt.Sprintf("balanced open unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.balancedOpenErr)
			return
		}
		s.db = pool
	}

	svc := NewBalancedOpenService(pool, s.lnd, s.logger, s.notifier)
	svc.maintenanceActive = func(ctx context.Context) bool {
		active, checkErr := s.lndMaintenanceActive(ctx)
		if checkErr != nil {
			if s.logger != nil {
				s.logger.Printf("balanced-open maintenance gate unavailable: %v", checkErr)
			}
			return false
		}
		return active
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.balancedOpenErr = fmt.Sprintf("balanced open unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.balancedOpenErr)
		return
	}

	s.balancedOpen = svc
	s.balancedOpenErr = ""
}

func (s *Server) balancedOpenService() (*BalancedOpenService, string) {
	s.initBalancedOpen()
	return s.balancedOpen, s.balancedOpenErr
}
