package server

import (
  "context"
  "fmt"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
)

const rebalanceInitRetryCooldown = 10 * time.Second

func (s *Server) initRebalance() {
  if s.rebalance != nil && s.rebalanceErr == "" {
    return
  }
  if !s.rebalanceInitAt.IsZero() && time.Since(s.rebalanceInitAt) < rebalanceInitRetryCooldown {
    return
  }
  s.rebalanceInitAt = time.Now()

  dsn, err := ResolveNotificationsDSN(s.logger)
  if err != nil {
    s.rebalanceErr = fmt.Sprintf("rebalance unavailable: %v", err)
    s.logger.Printf("%s", s.rebalanceErr)
    return
  }

  pool := s.db
  if pool == nil {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    pool, err = pgxpool.New(ctx, dsn)
    if err != nil {
      s.rebalanceErr = fmt.Sprintf("rebalance unavailable: failed to connect to postgres: %v", err)
      s.logger.Printf("%s", s.rebalanceErr)
      return
    }
    s.db = pool
  }

  svc := NewRebalanceService(pool, s.lnd, s.logger)
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  if err := svc.ensureSchema(ctx); err != nil {
    s.rebalanceErr = fmt.Sprintf("rebalance unavailable: failed to init schema: %v", err)
    s.logger.Printf("%s", s.rebalanceErr)
    return
  }

  s.rebalance = svc
  s.rebalanceErr = ""
}

