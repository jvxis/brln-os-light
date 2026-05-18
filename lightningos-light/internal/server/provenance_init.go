package server

import (
	"context"
	"fmt"
	"time"

	"lightningos-light/internal/electrs"

	"github.com/jackc/pgx/v5/pgxpool"
)

const provenanceInitRetryCooldown = 10 * time.Second

func (s *Server) initProvenance() {
	s.provenanceMu.Lock()
	defer s.provenanceMu.Unlock()

	if s.provenance != nil && s.provenanceErr == "" {
		return
	}
	if !s.provenanceInitAt.IsZero() && time.Since(s.provenanceInitAt) < provenanceInitRetryCooldown {
		return
	}
	s.provenanceInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.provenanceErr = fmt.Sprintf("provenance unavailable: %v", err)
		s.logger.Printf("%s", s.provenanceErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.provenanceErr = fmt.Sprintf("provenance unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.provenanceErr)
			return
		}
		s.db = pool
	}

	electrsClient := electrs.New("")
	svc := NewProvenanceService(pool, s.logger, s.lnd, electrsClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.provenanceErr = fmt.Sprintf("provenance unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.provenanceErr)
		return
	}

	s.provenance = svc
	s.provenanceErr = ""
}

func (s *Server) provenanceService() (*ProvenanceService, string) {
	s.initProvenance()
	return s.provenance, s.provenanceErr
}
