package server

import (
  "context"
  "crypto/tls"
  "fmt"
  "log"
  "net/http"
  "os"
  "strings"
  "sync"
  "time"

  "lightningos-light/internal/config"
  "lightningos-light/internal/lndclient"

  "github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
  cfg    *config.Config
  logger *log.Logger
  lnd    *lndclient.Client
  db     *pgxpool.Pool
  notifier *Notifier
  notifierErr string
  lndRestartMu sync.RWMutex
  lastLNDRestart time.Time
}

func New(cfg *config.Config, logger *log.Logger) *Server {
  return &Server{
    cfg:    cfg,
    logger: logger,
    lnd:    lndclient.New(cfg, logger),
  }
}

func (s *Server) Run() error {
  s.initNotifications()

  addr := fmt.Sprintf("%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)

  tlsCfg := &tls.Config{
    MinVersion: tls.VersionTLS12,
  }

  httpServer := &http.Server{
    Addr:              addr,
    Handler:           s.routes(),
    ReadHeaderTimeout: 10 * time.Second,
    TLSConfig:         tlsCfg,
  }

  s.logger.Printf("listening on https://%s", addr)
  return httpServer.ListenAndServeTLS(s.cfg.Server.TLSCert, s.cfg.Server.TLSKey)
}

func (s *Server) initNotifications() {
  dsn := os.Getenv("NOTIFICATIONS_PG_DSN")
  if isPlaceholderDSN(dsn) {
    dsn = ""
  }
  if strings.TrimSpace(dsn) == "" {
    if bootstrapped, err := bootstrapNotificationsDSN(s.logger); err == nil && bootstrapped != "" {
      dsn = bootstrapped
    } else if err != nil {
      s.notifierErr = fmt.Sprintf("notifications unavailable: %v", err)
    }
  }
  if strings.TrimSpace(dsn) == "" {
    if s.notifierErr == "" {
      s.notifierErr = "notifications unavailable: NOTIFICATIONS_PG_DSN not set"
    }
    s.logger.Printf("%s", s.notifierErr)
    return
  }

  ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
  defer cancel()

  pool, err := pgxpool.New(ctx, dsn)
  if err != nil {
    s.notifierErr = fmt.Sprintf("notifications unavailable: failed to connect to postgres: %v", err)
    s.logger.Printf("%s", s.notifierErr)
    return
  }

  s.db = pool
  s.notifier = NewNotifier(pool, s.lnd, s.logger)
  s.notifierErr = ""
  s.notifier.Start()
}
