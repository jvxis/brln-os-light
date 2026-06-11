package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	graphExplorerInitRetryCooldown = 10 * time.Second
	graphExplorerInitSchemaTimeout = 30 * time.Second
)

func (s *Server) initGraphExplorer() {
	s.graphExplorerMu.Lock()
	var startAfterUnlock *GraphExplorerService
	defer func() {
		s.graphExplorerMu.Unlock()
		if startAfterUnlock != nil {
			startAfterUnlock.Start()
		}
	}()

	if s.graphExplorer != nil && s.graphExplorerErr == "" {
		return
	}
	if !s.graphExplorerInitAt.IsZero() && time.Since(s.graphExplorerInitAt) < graphExplorerInitRetryCooldown {
		return
	}
	s.graphExplorerInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.graphExplorerErr = fmt.Sprintf("graph explorer unavailable: %v", err)
		s.logger.Printf("%s", s.graphExplorerErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.graphExplorerErr = fmt.Sprintf("graph explorer unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.graphExplorerErr)
			return
		}
		s.db = pool
	}

	svc := NewGraphExplorerService(pool, s.logger, s.lnd)
	ctx, cancel := context.WithTimeout(context.Background(), graphExplorerInitSchemaTimeout)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.graphExplorerErr = fmt.Sprintf("graph explorer unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.graphExplorerErr)
		return
	}

	s.graphExplorer = svc
	s.graphExplorerErr = ""
	if s.graphExplorerRuntimeActive {
		startAfterUnlock = svc
	}
}

func (s *Server) graphExplorerService() (*GraphExplorerService, string) {
	s.initGraphExplorer()
	s.graphExplorerMu.Lock()
	svc := s.graphExplorer
	errMsg := s.graphExplorerErr
	runtimeActive := s.graphExplorerRuntimeActive
	s.graphExplorerMu.Unlock()
	if runtimeActive && svc != nil {
		svc.Start()
	}
	return svc, errMsg
}

func (s *Server) enableGraphExplorerRuntime() {
	if s == nil {
		return
	}
	s.graphExplorerMu.Lock()
	s.graphExplorerRuntimeActive = true
	svc := s.graphExplorer
	s.graphExplorerMu.Unlock()
	if svc != nil {
		svc.Start()
	}
}
