package server

import (
  "context"
  "fmt"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
)

const autofeeInitRetryCooldown = 10 * time.Second

func (s *Server) initAutofee() {
  s.autofeeMu.Lock()
  defer s.autofeeMu.Unlock()

  if s.autofee != nil && s.autofeeErr == "" {
    return
  }
  if !s.autofeeInitAt.IsZero() && time.Since(s.autofeeInitAt) < autofeeInitRetryCooldown {
    return
  }
  s.autofeeInitAt = time.Now()

  dsn, err := ResolveNotificationsDSN(s.logger)
  if err != nil {
    s.autofeeErr = fmt.Sprintf("autofee unavailable: %v", err)
    s.logger.Printf("%s", s.autofeeErr)
    return
  }

  pool := s.db
  if pool == nil {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    pool, err = pgxpool.New(ctx, dsn)
    if err != nil {
      s.autofeeErr = fmt.Sprintf("autofee unavailable: failed to connect to postgres: %v", err)
      s.logger.Printf("%s", s.autofeeErr)
      return
    }
    s.db = pool
  }

  svc := NewAutofeeService(pool, s.lnd, s.notifier, s.htlcManager, s.logger)
  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()
  if err := svc.EnsureSchema(ctx); err != nil {
    s.autofeeErr = fmt.Sprintf("autofee unavailable: failed to init schema: %v", err)
    s.logger.Printf("%s", s.autofeeErr)
    return
  }

  s.autofee = svc
  s.autofeeErr = ""
}

func (s *Server) autofeeService() (*AutofeeService, string) {
  s.initAutofee()
  return s.autofee, s.autofeeErr
}
