package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const utxoManagerInitRetryCooldown = 10 * time.Second

func (s *Server) initUtxoManager() {
	s.utxoManagerMu.Lock()
	defer s.utxoManagerMu.Unlock()

	if s.utxoManager != nil && s.utxoManagerErr == "" {
		return
	}
	if !s.utxoManagerInitAt.IsZero() && time.Since(s.utxoManagerInitAt) < utxoManagerInitRetryCooldown {
		return
	}
	s.utxoManagerInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.utxoManagerErr = fmt.Sprintf("utxo manager unavailable: %v", err)
		s.logger.Printf("%s", s.utxoManagerErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.utxoManagerErr = fmt.Sprintf("utxo manager unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.utxoManagerErr)
			return
		}
		s.db = pool
	}

	svc := NewUtxoManagerService(pool, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.utxoManagerErr = fmt.Sprintf("utxo manager unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.utxoManagerErr)
		return
	}

	s.utxoManager = svc
	s.utxoManagerErr = ""
}

func (s *Server) utxoManagerService() (*UtxoManagerService, string) {
	s.initUtxoManager()
	return s.utxoManager, s.utxoManagerErr
}
