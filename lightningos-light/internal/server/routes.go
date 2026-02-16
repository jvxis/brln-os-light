package server

import (
  "net/http"
  "path/filepath"
  "strings"

  "github.com/go-chi/chi/v5"
  "github.com/go-chi/chi/v5/middleware"
)

func (s *Server) routes() http.Handler {
  r := chi.NewRouter()
  r.Use(middleware.Recoverer)
  r.Use(s.requestLogger())

  r.Get("/api/health", s.handleHealth)
  r.Get("/api/amboss/health", s.handleAmbossHealthGet)
  r.Post("/api/amboss/health", s.handleAmbossHealthPost)
  r.Get("/api/system", s.handleSystem)
  r.Get("/api/disk", s.handleDisk)
  r.Get("/api/postgres", s.handlePostgres)
  r.Get("/api/bitcoin", s.handleBitcoin)
  r.Get("/api/bitcoin/active", s.handleBitcoinActive)
  r.Get("/api/bitcoin/source", s.handleBitcoinSourceGet)
  r.Post("/api/bitcoin/source", s.handleBitcoinSourcePost)
  r.Get("/api/mempool/fees", s.handleMempoolFees)
  r.Get("/api/bitcoin-local/status", s.handleBitcoinLocalStatus)
  r.Get("/api/bitcoin-local/config", s.handleBitcoinLocalConfigGet)
  r.Post("/api/bitcoin-local/config", s.handleBitcoinLocalConfigPost)
  r.Get("/api/elements/status", s.handleElementsStatus)
  r.Get("/api/elements/mainchain", s.handleElementsMainchainGet)
  r.Post("/api/elements/mainchain", s.handleElementsMainchainPost)
  r.Get("/api/lnd/status", s.handleLNDStatus)
  r.Get("/api/lnd/config", s.handleLNDConfigGet)
  r.Get("/api/lnd/upgrade/status", s.handleLNDUpgradeStatus)
  r.Get("/api/wizard/status", s.handleWizardStatus)
  r.Post("/api/wizard/bitcoin-remote", s.handleWizardBitcoinRemote)
  r.Post("/api/wizard/lnd/create-wallet", s.handleCreateWallet)
  r.Post("/api/wizard/lnd/init-wallet", s.handleInitWallet)
  r.Post("/api/wizard/lnd/unlock", s.handleUnlockWallet)
  r.Post("/api/actions/restart", s.handleRestart)
  r.Post("/api/actions/system", s.handleSystemAction)
  r.Get("/api/logs", s.handleLogs)
  r.Post("/api/lnd/config", s.handleLNDConfigPost)
  r.Post("/api/lnd/config/raw", s.handleLNDConfigRaw)
  r.Post("/api/lnd/upgrade", s.handleLNDUpgradeStart)
  r.Get("/api/apps", s.handleAppsList)
  r.Post("/api/apps/{id}/install", s.handleAppInstall)
  r.Post("/api/apps/{id}/uninstall", s.handleAppUninstall)
  r.Post("/api/apps/{id}/start", s.handleAppStart)
  r.Post("/api/apps/{id}/stop", s.handleAppStop)
  r.Post("/api/apps/{id}/reset-admin", s.handleAppResetAdmin)
  r.Get("/api/apps/{id}/admin-password", s.handleAppAdminPassword)
  r.Get("/api/notifications", s.handleNotificationsList)
  r.Get("/api/notifications/stream", s.handleNotificationsStream)
  r.Get("/api/notifications/telegram", s.handleTelegramNotificationsGet)
  r.Post("/api/notifications/telegram", s.handleTelegramNotificationsPost)
  r.Get("/api/notifications/backup/telegram", s.handleTelegramBackupGet)
  r.Post("/api/notifications/backup/telegram", s.handleTelegramBackupPost)
  r.Post("/api/notifications/backup/telegram/test", s.handleTelegramBackupTest)
  r.Get("/api/reports/range", s.handleReportsRange)
  r.Get("/api/reports/custom", s.handleReportsCustom)
  r.Get("/api/reports/summary", s.handleReportsSummary)
  r.Get("/api/reports/live", s.handleReportsLive)
  r.Get("/api/reports/config", s.handleReportsConfigGet)
  r.Post("/api/reports/config", s.handleReportsConfigPost)
  r.Get("/api/terminal/status", s.handleTerminalStatus)

  r.Route("/api/onchain", func(r chi.Router) {
    r.Get("/utxos", s.handleOnchainUtxos)
    r.Get("/transactions", s.handleOnchainTransactions)
  })

  r.Route("/api/wallet", func(r chi.Router) {
    r.Get("/summary", s.handleWalletSummary)
    r.Post("/address", s.handleWalletAddress)
    r.Post("/invoice", s.handleWalletInvoice)
    r.Post("/decode", s.handleWalletDecode)
    r.Post("/pay", s.handleWalletPay)
    r.Post("/send", s.handleWalletSend)
  })

  r.Route("/api/lnops", func(r chi.Router) {
    r.Get("/channels", s.handleLNChannels)
    r.Get("/peers", s.handleLNPeers)
    r.Post("/sign-message", s.handleLNSignMessage)
    r.Post("/peer", s.handleLNConnectPeer)
    r.Post("/peer/disconnect", s.handleLNDisconnectPeer)
    r.Post("/peers/boost", s.handleLNBoostPeers)
    r.Get("/autofee/config", s.handleAutofeeConfigGet)
    r.Post("/autofee/config", s.handleAutofeeConfigPost)
    r.Get("/autofee/channels", s.handleAutofeeChannelsGet)
    r.Post("/autofee/channels", s.handleAutofeeChannelsPost)
    r.Post("/autofee/run", s.handleAutofeeRun)
    r.Get("/autofee/status", s.handleAutofeeStatus)
    r.Get("/autofee/results", s.handleAutofeeResults)
    r.Get("/channel/auto-heal", s.handleLNChanHealGet)
    r.Post("/channel/auto-heal", s.handleLNChanHealPost)
    r.Get("/channel/htlc-manager", s.handleLNHTLCManagerGet)
    r.Post("/channel/htlc-manager", s.handleLNHTLCManagerPost)
    r.Get("/channel/htlc-manager/logs", s.handleLNHTLCManagerLogs)
    r.Get("/channel/htlc-manager/failed", s.handleLNHTLCManagerFailed)
    r.Get("/channel/tor-peers", s.handleLNTorPeerCheckerGet)
    r.Post("/channel/tor-peers", s.handleLNTorPeerCheckerPost)
    r.Get("/channel/tor-peers/logs", s.handleLNTorPeerCheckerLogs)
    r.Get("/channel/fees", s.handleLNChannelFees)
    r.Post("/channel/open", s.handleLNOpenChannel)
    r.Post("/channel/close", s.handleLNCloseChannel)
    r.Post("/channel/fees", s.handleLNUpdateFees)
    r.Post("/channel/status", s.handleLNUpdateChanStatus)
  })

  r.Route("/api/rebalance", func(r chi.Router) {
    r.Get("/config", s.handleRebalanceConfigGet)
    r.Post("/config", s.handleRebalanceConfigPost)
    r.Get("/overview", s.handleRebalanceOverview)
    r.Get("/channels", s.handleRebalanceChannels)
    r.Get("/queue", s.handleRebalanceQueue)
    r.Get("/history", s.handleRebalanceHistory)
    r.Post("/run", s.handleRebalanceRun)
    r.Post("/stop", s.handleRebalanceStop)
    r.Post("/channel/target", s.handleRebalanceChannelTarget)
    r.Post("/channel/auto", s.handleRebalanceChannelAuto)
    r.Post("/channel/manual-restart", s.handleRebalanceChannelManualRestart)
    r.Post("/channel/exclude", s.handleRebalanceExclude)
    r.Get("/stream", s.handleRebalanceStream)
  })

  r.Route("/api/chat", func(r chi.Router) {
    r.Get("/inbox", s.handleChatInbox)
    r.Get("/messages", s.handleChatMessages)
    r.Post("/send", s.handleChatSend)
  })

  r.HandleFunc("/terminal", s.handleTerminalProxy)
  r.HandleFunc("/terminal/ws", s.handleTerminalProxy)
  r.HandleFunc("/terminal/*", s.handleTerminalProxy)

  staticDir := s.cfg.UI.StaticDir
  r.Get("/*", s.handleSPA(staticDir))

  return r
}

func (s *Server) handleSPA(staticDir string) http.HandlerFunc {
  fileServer := http.FileServer(http.Dir(staticDir))
  indexPath := filepath.Join(staticDir, "index.html")

  return func(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" {
      path := strings.TrimPrefix(r.URL.Path, "/")
      if _, err := http.Dir(staticDir).Open(path); err == nil {
        fileServer.ServeHTTP(w, r)
        return
      }
    }

    http.ServeFile(w, r, indexPath)
  }
}
