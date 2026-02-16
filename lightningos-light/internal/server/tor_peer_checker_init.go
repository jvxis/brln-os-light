package server

import (
  "context"
  "fmt"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
)

const torPeerCheckerInitRetryCooldown = 10 * time.Second

func (s *Server) initTorPeerChecker() {
  s.torPeerCheckerMu.Lock()
  defer s.torPeerCheckerMu.Unlock()

  if s.torPeerChecker != nil && s.torPeerCheckerErr == "" {
    return
  }
  if !s.torPeerCheckerInitAt.IsZero() && time.Since(s.torPeerCheckerInitAt) < torPeerCheckerInitRetryCooldown {
    return
  }
  s.torPeerCheckerInitAt = time.Now()

  dsn, err := ResolveNotificationsDSN(s.logger)
  if err != nil {
    s.torPeerCheckerErr = fmt.Sprintf("tor peer checker unavailable: %v", err)
    s.logger.Printf("%s", s.torPeerCheckerErr)
    return
  }

  pool := s.db
  if pool == nil {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    pool, err = pgxpool.New(ctx, dsn)
    if err != nil {
      s.torPeerCheckerErr = fmt.Sprintf("tor peer checker unavailable: failed to connect to postgres: %v", err)
      s.logger.Printf("%s", s.torPeerCheckerErr)
      return
    }
    s.db = pool
  }

  svc := NewTorPeerChecker(pool, s.lnd, s.logger)
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  if err := svc.EnsureSchema(ctx); err != nil {
    s.torPeerCheckerErr = fmt.Sprintf("tor peer checker unavailable: failed to init schema: %v", err)
    s.logger.Printf("%s", s.torPeerCheckerErr)
    return
  }
  svc.Start()

  s.torPeerChecker = svc
  s.torPeerCheckerErr = ""
}

func (s *Server) torPeerCheckerService() (*TorPeerChecker, string) {
  s.initTorPeerChecker()
  return s.torPeerChecker, s.torPeerCheckerErr
}
