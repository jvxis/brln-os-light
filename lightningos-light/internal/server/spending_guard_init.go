package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const spendingGuardInitRetryCooldown = 10 * time.Second

func (s *Server) initSpendingGuard() {
	s.spendingGuardMu.Lock()
	defer s.spendingGuardMu.Unlock()

	if s.spendingGuard != nil && s.spendingGuardErr == "" {
		return
	}
	if !s.spendingGuardInitAt.IsZero() && time.Since(s.spendingGuardInitAt) < spendingGuardInitRetryCooldown {
		return
	}
	s.spendingGuardInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.spendingGuardErr = fmt.Sprintf("spending guard unavailable: %v", err)
		return
	}
	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.spendingGuardErr = fmt.Sprintf("spending guard unavailable: failed to connect to postgres: %v", err)
			return
		}
		s.db = pool
	}

	svc := NewSpendingGuardService(pool, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.spendingGuardErr = fmt.Sprintf("spending guard unavailable: failed to initialize schema: %v", err)
		return
	}
	s.spendingGuard = svc
	s.spendingGuardErr = ""
	if s.chat != nil {
		s.chat.AttachSpendingGuard(svc)
	}
}

func (s *Server) spendingGuardService() (*SpendingGuardService, string) {
	s.initSpendingGuard()
	return s.spendingGuard, s.spendingGuardErr
}

func (s *Server) startSpendingGuardReconciler() {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			s.reconcileSpendingGuard()
			select {
			case <-s.shutdownContext().Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *Server) reconcileSpendingGuard() {
	svc, _ := s.spendingGuardService()
	if svc == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	items, err := svc.pending(ctx, 100)
	if err != nil {
		return
	}
	for _, item := range items {
		if strings.TrimSpace(item.PaymentHash) == "" {
			if time.Since(item.CreatedAt) > 10*time.Minute {
				_ = svc.Release(ctx, item.SpendingReservation, "orphaned before payment submission")
			}
			continue
		}
		details, found, trackErr := s.lnd.TrackPaymentDetails(ctx, item.PaymentHash)
		if trackErr != nil || !found {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(details.Status)) {
		case "SUCCEEDED":
			_ = svc.Settle(ctx, item.SpendingReservation, paymentDetailsFeeSat(details, item.MaxFeeSat), item.PaymentHash)
		case "FAILED":
			_ = svc.Release(ctx, item.SpendingReservation, "LND reported payment failed")
		}
	}
}
