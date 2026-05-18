package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	publicAllowed := detectProvenancePublicAllowed()
	s.provenanceBitcoind = NewBitcoinCoreSource(func(ctx context.Context) (bool, string) {
		avail := s.fullIndexAppAvailability(ctx)
		return avail.Available, avail.Reason
	})
	if s.provenanceMetrics == nil {
		s.provenanceMetrics = NewProvenanceMetrics()
	}
	chain, notes := buildProvenanceSourceChain(publicAllowed, s.provenanceBitcoind, s.provenanceMetrics)
	s.provenanceChain = chain
	s.logger.Printf("provenance source chain: %s", strings.Join(notes, " → "))

	svc := NewProvenanceService(pool, s.logger, s.lnd, chain)

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

// detectProvenancePublicAllowed returns true when the public-Electrum
// fallback step should be enabled. Public servers are mainnet-only (the
// only ones we ship defaults for); on any other network the step is a
// no-op anyway since public mainnet electrs won't know about testnet/
// signet/regtest txids.
//
// Order of resolution:
//   - PROVENANCE_NETWORK env var (mainnet / testnet / signet / regtest)
//   - Filesystem hint: /data/lnd/data/chain/bitcoin/<network>/wallet.db
//   - Default: mainnet
func detectProvenancePublicAllowed() bool {
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PROVENANCE_NETWORK"))); v != "" {
		return v == "mainnet" || v == "bitcoin"
	}
	for _, candidate := range []string{"testnet", "testnet3", "testnet4", "signet", "regtest"} {
		if _, err := os.Stat(filepath.Join("/data/lnd/data/chain/bitcoin", candidate, "wallet.db")); err == nil {
			return false
		}
	}
	return true
}
