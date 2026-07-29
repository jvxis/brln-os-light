package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const magmaInitRetryCooldown = 10 * time.Second

func (s *Server) initMagma() {
	s.magmaMu.Lock()
	defer s.magmaMu.Unlock()
	if s.magma != nil && s.magmaErr == "" {
		return
	}
	if !s.magmaInitAt.IsZero() && time.Since(s.magmaInitAt) < magmaInitRetryCooldown {
		return
	}
	s.magmaInitAt = time.Now()
	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.magmaErr = fmt.Sprintf("Magma Inbound Sales unavailable: %v", err)
		return
	}
	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.magmaErr = fmt.Sprintf("Magma Inbound Sales unavailable: failed to connect to postgres: %v", err)
			return
		}
		s.db = pool
	}
	svc := NewMagmaService(pool, s.lnd, s.logger)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.magmaErr = fmt.Sprintf("Magma Inbound Sales unavailable: failed to init schema: %v", err)
		return
	}
	s.magma = svc
	s.magmaErr = ""

	// Start the poller here rather than only from the server's startup block.
	// That block runs once and skips the app when this init failed at boot — a
	// Postgres that is a second late is enough. The next HTTP request then
	// re-inits successfully and everything looks healthy while the worker was
	// never launched, which reads as "the poller is dead" with no error anywhere.
	// Start is a sync.Once, so calling it from both places is harmless.
	svc.Start(s.shutdownContext())
}

func (s *Server) magmaService() (*MagmaService, string) {
	s.initMagma()
	return s.magma, s.magmaErr
}
