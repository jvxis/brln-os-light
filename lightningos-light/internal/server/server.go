package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/electrs"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/reports"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/singleflight"
)

type Server struct {
	cfg                         *config.Config
	logger                      *log.Logger
	shutdownCtx                 context.Context
	shutdownCancel              context.CancelFunc
	lnd                         *lndclient.Client
	networkMapMu                sync.Mutex
	networkMapGeoCache          map[string]networkMapGeoCacheEntry
	db                          *pgxpool.Pool
	dbMaintenanceMu             sync.Mutex
	graphHistoryCompact         dbGraphHistoryCompactJob
	notifier                    *Notifier
	notifierErr                 string
	chat                        *ChatService
	amboss                      *AmbossHealthChecker
	chanHealer                  *ChanStatusHealer
	reports                     *reports.Service
	reportsErr                  string
	reportsMu                   sync.Mutex
	reportsInitAt               time.Time
	reportsWarmupStarted        bool
	auth                        *AuthService
	auditLogInitAt              time.Time
	auditLogMu                  sync.Mutex
	auditLog                    *AuditService
	auditLogErr                 string
	auditRetentionOnce          sync.Once
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
	channelRankingInitAt        time.Time
	channelRankingMu            sync.Mutex
	channelRanking              *ChannelRankingService
	channelRankingErr           string
	channelOpenCandidatesInitAt time.Time
	channelOpenCandidatesMu     sync.Mutex
	channelOpenCandidates       *ChannelOpenCandidatesService
	channelOpenCandidatesErr    string
	graphExplorerInitAt         time.Time
	graphExplorerMu             sync.Mutex
	graphExplorer               *GraphExplorerService
	graphExplorerErr            string
	graphExplorerRuntimeActive  bool
	balancedOpenInitAt          time.Time
	balancedOpenMu              sync.Mutex
	balancedOpen                *BalancedOpenService
	balancedOpenErr             string
	closeManagerInitAt          time.Time
	closeManagerMu              sync.Mutex
	closeManager                *CloseManagerService
	closeManagerErr             string
	utxoManagerInitAt           time.Time
	utxoManagerMu               sync.Mutex
	utxoManager                 *UtxoManagerService
	utxoManagerErr              string
	utxoLeaseCacheMu            sync.Mutex
	utxoLeaseCacheAt            time.Time
	utxoLeaseCache              map[string]lndclient.LeaseInfo
	utxoPruneMu                 sync.Mutex
	utxoPruneLive               []string
	utxoPruneSeen               bool
	utxoMaintenanceOnce         sync.Once
	provenanceInitAt            time.Time
	provenanceMu                sync.Mutex
	provenance                  *ProvenanceService
	provenanceErr               string
	provenanceChain             *electrs.ChainedSource
	provenanceBitcoind          *BitcoinCoreSource
	provenanceMetrics           *ProvenanceMetrics
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
	bitcoinStatusMu             sync.Mutex
	bitcoinStatusGroup          singleflight.Group
	bitcoinActiveCache          map[string]cachedBitcoinStatus
	bitcoinLocalCache           cachedBitcoinLocalStatus
	bitcoinMarketMu             sync.Mutex
	bitcoinMarketCache          cachedBitcoinMarketStatus
}

func New(cfg *config.Config, logger *log.Logger) *Server {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	srv := &Server{
		cfg:                cfg,
		logger:             logger,
		shutdownCtx:        shutdownCtx,
		shutdownCancel:     shutdownCancel,
		lnd:                lndclient.New(cfg, logger),
		networkMapGeoCache: make(map[string]networkMapGeoCacheEntry),
		bitcoinActiveCache: make(map[string]cachedBitcoinStatus),
		provenanceMetrics:  NewProvenanceMetrics(),
	}
	srv.auth = NewAuthService(cfg, logger)
	srv.chat = NewChatService(srv.lnd, logger)
	srv.amboss = NewAmbossHealthChecker(srv.lnd, logger)
	srv.chanHealer = NewChanStatusHealer(srv.lnd, logger)
	return srv
}

func (s *Server) Run() error {
	if s.shutdownCancel != nil {
		defer s.shutdownCancel()
	}
	runCtx, stopSignals := signal.NotifyContext(s.shutdownContext(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	s.startAppUpgradeChecker()
	s.startPublicPoolRuntimeReconciler()
	s.initNotifications()
	s.initAuditLog()
	s.initReports()
	s.startTelegramNotifications()
	s.initRebalance()
	s.initHTLCManager()
	s.initFailedPaymentsCleaner()
	s.initTorPeerChecker()
	s.initDepix()
	s.initAutofee()
	s.initShortcuts()
	s.initChannelRanking()
	s.initChannelOpenCandidates()
	s.initGraphExplorer()
	s.initBalancedOpen()
	s.initCloseManager()
	s.initUtxoManager()
	s.startUtxoManagerMaintenance()
	s.initProvenance()
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
	if s.channelRanking != nil {
		s.channelRanking.Start()
	}
	if s.channelOpenCandidates != nil {
		s.channelOpenCandidates.Start()
	}
	s.enableGraphExplorerRuntime()
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
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServeTLS(s.cfg.Server.TLSCert, s.cfg.Server.TLSKey)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		if s.shutdownCancel != nil {
			s.shutdownCancel()
		}
		return err
	case <-runCtx.Done():
		if s.shutdownCancel != nil {
			s.shutdownCancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			return err
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (s *Server) shutdownContext() context.Context {
	if s != nil && s.shutdownCtx != nil {
		return s.shutdownCtx
	}
	return context.Background()
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
	if err := s.ensureChannelNotesSchema(ctx); err != nil {
		s.logger.Printf("channel notes disabled: failed to init schema: %v", err)
	}
	if s.chat != nil {
		chatCtx, chatCancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := s.chat.AttachDB(chatCtx, pool); err != nil {
			s.logger.Printf("chat db persistence disabled: failed to init schema: %v", err)
		}
		chatCancel()
	}
	s.notifier = NewNotifier(pool, s.lnd, s.logger)
	s.notifierErr = ""
	s.notifier.Start()
	if s.chat != nil {
		s.chat.AttachNotifier(s.notifier)
	}
}
