package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/reports"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg                         *config.Config
	logger                      *log.Logger
	lnd                         *lndclient.Client
	db                          *pgxpool.Pool
	notifier                    *Notifier
	notifierErr                 string
	chat                        *ChatService
	amboss                      *AmbossHealthChecker
	chanHealer                  *ChanStatusHealer
	reports                     *reports.Service
	reportsErr                  string
	reportsMu                   sync.Mutex
	reportsInitAt               time.Time
	rebalanceInitAt             time.Time
	rebalance                   *RebalanceService
	rebalanceErr                string
	htlcManagerInitAt           time.Time
	htlcManagerMu               sync.Mutex
	htlcManager                 *HtlcManager
	htlcManagerErr              string
	failedPaymentsCleanerInitAt time.Time
	failedPaymentsCleanerMu     sync.Mutex
	failedPaymentsCleaner       *FailedPaymentsCleaner
	failedPaymentsCleanerErr    string
	torPeerCheckerInitAt        time.Time
	torPeerCheckerMu            sync.Mutex
	torPeerChecker              *TorPeerChecker
	torPeerCheckerErr           string
	depixInitAt                 time.Time
	depixMu                     sync.Mutex
	depix                       *DepixService
	depixErr                    string
	autofeeInitAt               time.Time
	autofeeMu                   sync.Mutex
	autofee                     *AutofeeService
	autofeeErr                  string
	shortcutsInitAt             time.Time
	shortcutsMu                 sync.Mutex
	shortcuts                   *ShortcutsService
	shortcutsErr                string
	balancedOpenInitAt          time.Time
	balancedOpenMu              sync.Mutex
	balancedOpen                *BalancedOpenService
	balancedOpenErr             string
	closeManagerInitAt          time.Time
	closeManagerMu              sync.Mutex
	closeManager                *CloseManagerService
	closeManagerErr             string
	nodeRetirementInitAt        time.Time
	nodeRetirementMu            sync.Mutex
	nodeRetirement              *NodeRetirementService
	nodeRetirementErr           string
	successionInitAt            time.Time
	successionMu                sync.Mutex
	succession                  *SuccessionService
	successionErr               string
	scbRestoreMu                sync.Mutex
	lndRestartMu                sync.RWMutex
	lastLNDRestart              time.Time
	walletActivityMu            sync.Mutex
}

func New(cfg *config.Config, logger *log.Logger) *Server {
	srv := &Server{
		cfg:    cfg,
		logger: logger,
		lnd:    lndclient.New(cfg, logger),
	}
	srv.chat = NewChatService(srv.lnd, logger)
	srv.amboss = NewAmbossHealthChecker(srv.lnd, logger)
	srv.chanHealer = NewChanStatusHealer(srv.lnd, logger)
	return srv
}

func (s *Server) Run() error {
	s.startAppUpgradeChecker()
	s.initNotifications()
	s.initReports()
	s.startTelegramNotifications()
	s.initRebalance()
	s.initHTLCManager()
	s.initFailedPaymentsCleaner()
	s.initTorPeerChecker()
	s.initDepix()
	s.initAutofee()
	s.initShortcuts()
	s.initBalancedOpen()
	s.initCloseManager()
	s.initNodeRetirement()
	s.initSuccession()
	if s.chat != nil {
		s.chat.Start()
	}
	if s.amboss != nil {
		s.amboss.Start()
	}
	if s.chanHealer != nil {
		s.chanHealer.Start()
	}
	if s.rebalance != nil {
		s.rebalance.Start()
	}
	if s.htlcManager != nil {
		s.htlcManager.Start()
	}
	if s.failedPaymentsCleaner != nil {
		s.failedPaymentsCleaner.Start()
	}
	if s.torPeerChecker != nil {
		s.torPeerChecker.Start()
	}
	if s.autofee != nil {
		s.autofee.Start()
	}
	if s.balancedOpen != nil {
		s.balancedOpen.Start()
	}
	if s.closeManager != nil {
		s.closeManager.Start()
	}
	if s.nodeRetirement != nil {
		s.nodeRetirement.Start()
	}
	if s.succession != nil {
		s.succession.Start()
	}

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
	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.notifierErr = fmt.Sprintf("notifications unavailable: %v", err)
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
	if err := s.ensureChannelDowntimeSchema(ctx); err != nil {
		s.logger.Printf("channel downtime persistence disabled: failed to init schema: %v", err)
	}
	s.notifier = NewNotifier(pool, s.lnd, s.logger)
	s.notifierErr = ""
	s.notifier.Start()
	if s.chat != nil {
		s.chat.AttachNotifier(s.notifier)
	}
}
