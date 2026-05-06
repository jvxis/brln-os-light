package server

import (
	"context"
	"strings"
	"time"
)

const (
	bitcoinStatusCacheOK            = 30 * time.Second
	bitcoinStatusCacheStale         = 10 * time.Second
	bitcoinStatusCacheErr           = 45 * time.Second
	bitcoinStatusStaleOKGrace       = 2 * time.Minute
	bitcoinActiveFetchTimeoutRemote = 4 * time.Second
	bitcoinActiveFetchTimeoutLocal  = 8 * time.Second
	bitcoinLocalFetchTimeout        = 10 * time.Second
)

type cachedBitcoinStatus struct {
	value     bitcoinStatus
	err       error
	expiresAt time.Time
	fetchedAt time.Time
}

type cachedBitcoinLocalStatus struct {
	value     bitcoinLocalStatus
	err       error
	expiresAt time.Time
}

func bitcoinStatusTTL(status bitcoinStatus, err error) time.Duration {
	if err == nil && status.RPCOk && status.RPCStale {
		return bitcoinStatusCacheStale
	}
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

func bitcoinActiveFetchTimeout(source string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(source), "local") {
		return bitcoinActiveFetchTimeoutLocal
	}
	return bitcoinActiveFetchTimeoutRemote
}

func bitcoinActiveHandlerTimeout(source string) time.Duration {
	return bitcoinActiveFetchTimeout(source) + time.Second
}

func bitcoinLocalHandlerTimeout() time.Duration {
	return bitcoinLocalFetchTimeout + time.Second
}

func (s *Server) cachedBitcoinActiveStatus(source string, now time.Time) (bitcoinStatus, error, bool) {
	if s == nil {
		return bitcoinStatus{}, nil, false
	}
	s.bitcoinStatusMu.Lock()
	defer s.bitcoinStatusMu.Unlock()
	entry, ok := s.bitcoinActiveCache[source]
	if !ok || !now.Before(entry.expiresAt) {
		return bitcoinStatus{}, nil, false
	}
	return entry.value, entry.err, true
}

func (s *Server) staleBitcoinActiveStatus(source string, now time.Time) (bitcoinStatus, time.Time, bool) {
	if s == nil {
		return bitcoinStatus{}, time.Time{}, false
	}
	s.bitcoinStatusMu.Lock()
	defer s.bitcoinStatusMu.Unlock()
	entry, ok := s.bitcoinActiveCache[source]
	if !ok || !entry.value.RPCOk || entry.fetchedAt.IsZero() {
		return bitcoinStatus{}, time.Time{}, false
	}
	if now.Sub(entry.fetchedAt) > bitcoinStatusStaleOKGrace {
		return bitcoinStatus{}, time.Time{}, false
	}
	return entry.value, entry.fetchedAt, true
}

func (s *Server) cachedBitcoinLocalStatus(now time.Time) (bitcoinLocalStatus, error, bool) {
	if s == nil {
		return bitcoinLocalStatus{}, nil, false
	}
	s.bitcoinStatusMu.Lock()
	defer s.bitcoinStatusMu.Unlock()
	if !now.Before(s.bitcoinLocalCache.expiresAt) {
		return bitcoinLocalStatus{}, nil, false
	}
	return s.bitcoinLocalCache.value, s.bitcoinLocalCache.err, true
}

func markBitcoinStatusStale(status bitcoinStatus, fetchedAt, now time.Time) bitcoinStatus {
	status.RPCOk = true
	status.RPCStale = true
	status.RPCLastOKAgeSeconds = 0
	if !fetchedAt.IsZero() && now.After(fetchedAt) {
		status.RPCLastOKAgeSeconds = int64(now.Sub(fetchedAt).Seconds())
	}
	return status
}

func (s *Server) bitcoinActiveStatusCached(ctx context.Context) (bitcoinStatus, error) {
	source := readBitcoinSource()
	now := time.Now()

	if status, err, ok := s.cachedBitcoinActiveStatus(source, now); ok {
		return status, err
	}

	resultCh := s.bitcoinStatusGroup.DoChan("bitcoin-active:"+source, func() (any, error) {
		now := time.Now()
		if status, err, ok := s.cachedBitcoinActiveStatus(source, now); ok {
			return status, err
		}

		fetchCtx, cancel := context.WithTimeout(context.Background(), bitcoinActiveFetchTimeout(source))
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

		now = time.Now()
		statusFetchedAt := time.Time{}
		if staleStatus, fetchedAt, ok := s.staleBitcoinActiveStatus(source, now); ok && (err != nil || !status.RPCOk) {
			status = markBitcoinStatusStale(staleStatus, fetchedAt, now)
			err = nil
			statusFetchedAt = fetchedAt
		} else if err == nil && status.RPCOk {
			statusFetchedAt = now
		}

		s.bitcoinStatusMu.Lock()
		if s.bitcoinActiveCache == nil {
			s.bitcoinActiveCache = make(map[string]cachedBitcoinStatus)
		}
		s.bitcoinActiveCache[source] = cachedBitcoinStatus{
			value:     status,
			err:       err,
			expiresAt: now.Add(bitcoinStatusTTL(status, err)),
			fetchedAt: statusFetchedAt,
		}
		s.bitcoinStatusMu.Unlock()
		return status, err
	})

	select {
	case <-ctx.Done():
		if status, err, ok := s.cachedBitcoinActiveStatus(source, time.Now()); ok {
			return status, err
		}
		now := time.Now()
		if status, fetchedAt, ok := s.staleBitcoinActiveStatus(source, now); ok {
			return markBitcoinStatusStale(status, fetchedAt, now), nil
		}
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

	if status, err, ok := s.cachedBitcoinLocalStatus(now); ok {
		return status, err
	}

	resultCh := s.bitcoinStatusGroup.DoChan("bitcoin-local-status", func() (any, error) {
		now := time.Now()
		if status, err, ok := s.cachedBitcoinLocalStatus(now); ok {
			return status, err
		}

		fetchCtx, cancel := context.WithTimeout(context.Background(), bitcoinLocalFetchTimeout)
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
		if status, err, ok := s.cachedBitcoinLocalStatus(time.Now()); ok {
			return status, err
		}
		return bitcoinLocalStatus{}, ctx.Err()
	case result := <-resultCh:
		status, _ := result.Val.(bitcoinLocalStatus)
		if result.Err != nil {
			return status, result.Err
		}
		return status, nil
	}
}
