package server

import (
  "context"
  "fmt"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
)

const htlcManagerInitRetryCooldown = 10 * time.Second

func (s *Server) initHTLCManager() {
  s.htlcManagerMu.Lock()
  defer s.htlcManagerMu.Unlock()

  if s.htlcManager != nil && s.htlcManagerErr == "" {
    return
  }
  if !s.htlcManagerInitAt.IsZero() && time.Since(s.htlcManagerInitAt) < htlcManagerInitRetryCooldown {
    return
  }
  s.htlcManagerInitAt = time.Now()

  dsn, err := ResolveNotificationsDSN(s.logger)
  if err != nil {
    s.htlcManagerErr = fmt.Sprintf("htlc manager unavailable: %v", err)
    s.logger.Printf("%s", s.htlcManagerErr)
    return
  }

  pool := s.db
  if pool == nil {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    pool, err = pgxpool.New(ctx, dsn)
    if err != nil {
      s.htlcManagerErr = fmt.Sprintf("htlc manager unavailable: failed to connect to postgres: %v", err)
      s.logger.Printf("%s", s.htlcManagerErr)
      return
    }
    s.db = pool
  }

  svc := NewHtlcManager(pool, s.lnd, s.logger)
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.htlcManagerErr = fmt.Sprintf("htlc manager unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.htlcManagerErr)
		return
	}
	svc.Start()

	s.htlcManager = svc
	s.htlcManagerErr = ""
}

func (s *Server) htlcManagerService() (*HtlcManager, string) {
  s.initHTLCManager()
  return s.htlcManager, s.htlcManagerErr
}
