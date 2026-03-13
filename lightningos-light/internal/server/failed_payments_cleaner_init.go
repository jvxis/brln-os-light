package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const failedPaymentsCleanerInitRetryCooldown = 10 * time.Second

func (s *Server) initFailedPaymentsCleaner() {
	s.failedPaymentsCleanerMu.Lock()
	defer s.failedPaymentsCleanerMu.Unlock()

	if s.failedPaymentsCleaner != nil && s.failedPaymentsCleanerErr == "" {
		return
	}
	if !s.failedPaymentsCleanerInitAt.IsZero() && time.Since(s.failedPaymentsCleanerInitAt) < failedPaymentsCleanerInitRetryCooldown {
		return
	}
	s.failedPaymentsCleanerInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.failedPaymentsCleanerErr = fmt.Sprintf("failed payments cleaner unavailable: %v", err)
		s.logger.Printf("%s", s.failedPaymentsCleanerErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.failedPaymentsCleanerErr = fmt.Sprintf("failed payments cleaner unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.failedPaymentsCleanerErr)
			return
		}
		s.db = pool
	}

	svc := NewFailedPaymentsCleaner(pool, s.lnd, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.failedPaymentsCleanerErr = fmt.Sprintf("failed payments cleaner unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.failedPaymentsCleanerErr)
		return
	}
	svc.Start()

	s.failedPaymentsCleaner = svc
	s.failedPaymentsCleanerErr = ""
}

func (s *Server) failedPaymentsCleanerService() (*FailedPaymentsCleaner, string) {
	s.initFailedPaymentsCleaner()
	return s.failedPaymentsCleaner, s.failedPaymentsCleanerErr
}
