package server

import (
	"context"
	"time"
)

const (
	bitcoinStatusCacheOK  = 30 * time.Second
	bitcoinStatusCacheErr = 45 * time.Second
)

type cachedBitcoinStatus struct {
	value     bitcoinStatus
	err       error
	expiresAt time.Time
}

type cachedBitcoinLocalStatus struct {
	value     bitcoinLocalStatus
	err       error
	expiresAt time.Time
}

func bitcoinStatusTTL(status bitcoinStatus, err error) time.Duration {
	if err != nil || !status.RPCOk {
		return bitcoinStatusCacheErr
	}
	return bitcoinStatusCacheOK
}

func bitcoinLocalStatusTTL(status bitcoinLocalStatus, err error) time.Duration {
	if err != nil || !status.RPCOk {
		return bitcoinStatusCacheErr
	}
	return bitcoinStatusCacheOK
}

func (s *Server) invalidateBitcoinStatusCaches() {
	if s == nil {
		return
	}
	s.bitcoinStatusMu.Lock()
	s.bitcoinActiveCache = make(map[string]cachedBitcoinStatus)
	s.bitcoinLocalCache = cachedBitcoinLocalStatus{}
	s.bitcoinStatusMu.Unlock()
}

func (s *Server) bitcoinActiveStatusCached(ctx context.Context) (bitcoinStatus, error) {
	source := readBitcoinSource()
	now := time.Now()

	s.bitcoinStatusMu.Lock()
	if entry, ok := s.bitcoinActiveCache[source]; ok && now.Before(entry.expiresAt) {
		status := entry.value
		err := entry.err
		s.bitcoinStatusMu.Unlock()
		return status, err
	}
	s.bitcoinStatusMu.Unlock()

	resultCh := s.bitcoinStatusGroup.DoChan("bitcoin-active:"+source, func() (any, error) {
		now := time.Now()
		s.bitcoinStatusMu.Lock()
		if entry, ok := s.bitcoinActiveCache[source]; ok && now.Before(entry.expiresAt) {
			status := entry.value
			err := entry.err
			s.bitcoinStatusMu.Unlock()
			return status, err
		}
		s.bitcoinStatusMu.Unlock()

		fetchCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		var (
			status bitcoinStatus
			err    error
		)
		if source == "local" {
			status, err = s.bitcoinLocalStatusActive(fetchCtx)
		} else {
			status, err = s.bitcoinStatus(fetchCtx)
		}

		s.bitcoinStatusMu.Lock()
		if s.bitcoinActiveCache == nil {
			s.bitcoinActiveCache = make(map[string]cachedBitcoinStatus)
		}
		s.bitcoinActiveCache[source] = cachedBitcoinStatus{
			value:     status,
			err:       err,
			expiresAt: time.Now().Add(bitcoinStatusTTL(status, err)),
		}
		s.bitcoinStatusMu.Unlock()
		return status, err
	})

	select {
	case <-ctx.Done():
		return bitcoinStatus{}, ctx.Err()
	case result := <-resultCh:
		status, _ := result.Val.(bitcoinStatus)
		if result.Err != nil {
			return status, result.Err
		}
		return status, nil
	}
}

func (s *Server) bitcoinLocalStatusCached(ctx context.Context) (bitcoinLocalStatus, error) {
	now := time.Now()

	s.bitcoinStatusMu.Lock()
	if now.Before(s.bitcoinLocalCache.expiresAt) {
		status := s.bitcoinLocalCache.value
		err := s.bitcoinLocalCache.err
		s.bitcoinStatusMu.Unlock()
		return status, err
	}
	s.bitcoinStatusMu.Unlock()

	resultCh := s.bitcoinStatusGroup.DoChan("bitcoin-local-status", func() (any, error) {
		now := time.Now()
		s.bitcoinStatusMu.Lock()
		if now.Before(s.bitcoinLocalCache.expiresAt) {
			status := s.bitcoinLocalCache.value
			err := s.bitcoinLocalCache.err
			s.bitcoinStatusMu.Unlock()
			return status, err
		}
		s.bitcoinStatusMu.Unlock()

		fetchCtx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()

		status, err := s.bitcoinLocalStatus(fetchCtx)

		s.bitcoinStatusMu.Lock()
		s.bitcoinLocalCache = cachedBitcoinLocalStatus{
			value:     status,
			err:       err,
			expiresAt: time.Now().Add(bitcoinLocalStatusTTL(status, err)),
		}
		s.bitcoinStatusMu.Unlock()
		return status, err
	})

	select {
	case <-ctx.Done():
		return bitcoinLocalStatus{}, ctx.Err()
	case result := <-resultCh:
		status, _ := result.Val.(bitcoinLocalStatus)
		if result.Err != nil {
			return status, result.Err
		}
		return status, nil
	}
}
