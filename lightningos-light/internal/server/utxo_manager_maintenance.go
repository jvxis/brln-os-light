package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	utxoLeaseCacheTTL     = 10 * time.Second
	utxoPruneInterval     = 5 * time.Minute
	utxoPruneQueryTimeout = 10 * time.Second
)

func (s *Server) startUtxoManagerMaintenance() {
	if s == nil {
		return
	}
	s.utxoMaintenanceOnce.Do(func() {
		go s.runUtxoPruneLoop(s.shutdownContext())
	})
}

func (s *Server) runUtxoPruneLoop(ctx context.Context) {
	ticker := time.NewTicker(utxoPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pruneStaleUtxoMetadata(ctx)
		}
	}
}

func (s *Server) pruneStaleUtxoMetadata(ctx context.Context) {
	live, ok := s.latestUtxoPruneLive()
	if !ok {
		return
	}
	svc, _ := s.utxoManagerService()
	if svc == nil {
		return
	}
	pruneCtx, cancel := context.WithTimeout(ctx, utxoPruneQueryTimeout)
	defer cancel()
	if err := svc.Prune(pruneCtx, live); err != nil && s.logger != nil {
		s.logger.Printf("utxo manager: background prune failed: %v", err)
	}
}

func (s *Server) recordUtxoPruneLive(live []string) {
	if s == nil {
		return
	}
	cleaned := make([]string, 0, len(live))
	for _, outpoint := range live {
		outpoint = strings.ToLower(strings.TrimSpace(outpoint))
		if outpoint != "" {
			cleaned = append(cleaned, outpoint)
		}
	}
	s.utxoPruneMu.Lock()
	s.utxoPruneLive = cleaned
	s.utxoPruneSeen = true
	s.utxoPruneMu.Unlock()
}

func (s *Server) latestUtxoPruneLive() ([]string, bool) {
	if s == nil {
		return nil, false
	}
	s.utxoPruneMu.Lock()
	defer s.utxoPruneMu.Unlock()
	if !s.utxoPruneSeen {
		return nil, false
	}
	live := append([]string(nil), s.utxoPruneLive...)
	return live, true
}

func (s *Server) listCachedUtxoLeases(ctx context.Context) (map[string]lndclient.LeaseInfo, error) {
	if s == nil || s.lnd == nil {
		return nil, errors.New("lnd unavailable")
	}
	now := time.Now()
	s.utxoLeaseCacheMu.Lock()
	if s.utxoLeaseCache != nil && now.Sub(s.utxoLeaseCacheAt) < utxoLeaseCacheTTL {
		leases := cloneUtxoLeaseMap(s.utxoLeaseCache)
		s.utxoLeaseCacheMu.Unlock()
		return leases, nil
	}
	s.utxoLeaseCacheMu.Unlock()

	leases, err := s.lnd.ListLeases(ctx)
	if err != nil {
		return nil, err
	}
	leases = cloneUtxoLeaseMap(leases)

	s.utxoLeaseCacheMu.Lock()
	s.utxoLeaseCache = cloneUtxoLeaseMap(leases)
	s.utxoLeaseCacheAt = now
	s.utxoLeaseCacheMu.Unlock()
	return leases, nil
}

func (s *Server) invalidateUtxoLeaseCache() {
	if s == nil {
		return
	}
	s.utxoLeaseCacheMu.Lock()
	s.utxoLeaseCache = nil
	s.utxoLeaseCacheAt = time.Time{}
	s.utxoLeaseCacheMu.Unlock()
}

func cloneUtxoLeaseMap(in map[string]lndclient.LeaseInfo) map[string]lndclient.LeaseInfo {
	if in == nil {
		return nil
	}
	out := make(map[string]lndclient.LeaseInfo, len(in))
	for key, value := range in {
		key = strings.ToLower(strings.TrimSpace(key))
		if key != "" {
			out[key] = value
		}
	}
	return out
}
