package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"

	"github.com/go-chi/chi/v5"
)

const (
	appInfoWorkerLimit = 4
	appListCacheTTL    = 10 * time.Second
)

var (
	errAppUninstallConfirmationRequired = errors.New("explicit app uninstall confirmation is required")
	errBitcoinCoreSourceSwitchRequired  = errors.New("switch LND to a verified remote Bitcoin source before uninstalling Bitcoin Core")
	errBitcoinCoreRemoteCredentials     = errors.New("remote Bitcoin RPC credentials are missing")
)

type appUninstallRequest struct {
	Confirm bool   `json:"confirm"`
	AppID   string `json:"app_id"`
}

func validateAppUninstallConfirmation(appID string, req appUninstallRequest) error {
	if !req.Confirm || req.AppID != appID {
		return errAppUninstallConfirmationRequired
	}
	return nil
}

func validateBitcoinCoreUninstallSource(source string, remote bitcoinRPCConfig) error {
	if normalizeBitcoinSource(source) != "remote" {
		return errBitcoinCoreSourceSwitchRequired
	}
	if remote.User == "" || remote.Pass == "" || remote.Host == "" {
		return errBitcoinCoreRemoteCredentials
	}
	return nil
}

type cachedAppList struct {
	at   time.Time
	apps []appInfo
}

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	resp, err := s.appListSnapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for index := range resp {
		if operation, active := s.currentAppOperation(resp[index].ID); active {
			operationCopy := operation
			resp[index].Operation = &operationCopy
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) appListSnapshot(ctx context.Context) ([]appInfo, error) {
	if cached, ok := s.cachedAppList(); ok {
		return cached, nil
	}
	result := s.appListGroup.DoChan("catalog", func() (any, error) {
		if cached, ok := s.cachedAppList(); ok {
			return cached, nil
		}
		refreshParent := s.shutdownCtx
		if refreshParent == nil {
			refreshParent = context.Background()
		}
		refreshCtx, cancel := context.WithTimeout(refreshParent, 45*time.Second)
		defer cancel()
		apps, err := s.buildAppList(refreshCtx)
		if err != nil {
			return nil, err
		}
		s.appListMu.Lock()
		s.appListCache = cachedAppList{at: time.Now(), apps: cloneAppInfos(apps)}
		s.appListMu.Unlock()
		return apps, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case completed := <-result:
		if completed.Err != nil {
			return nil, completed.Err
		}
		apps, ok := completed.Val.([]appInfo)
		if !ok {
			return nil, errors.New("invalid app catalog snapshot")
		}
		return cloneAppInfos(apps), nil
	}
}

func (s *Server) cachedAppList() ([]appInfo, bool) {
	s.appListMu.Lock()
	defer s.appListMu.Unlock()
	if s.appListCache.at.IsZero() || time.Since(s.appListCache.at) >= appListCacheTTL {
		return nil, false
	}
	return cloneAppInfos(s.appListCache.apps), true
}

func (s *Server) invalidateAppListCache() {
	if s == nil {
		return
	}
	s.appListMu.Lock()
	s.appListCache = cachedAppList{}
	s.appListMu.Unlock()
}

func cloneAppInfos(apps []appInfo) []appInfo {
	cloned := append([]appInfo(nil), apps...)
	for index := range cloned {
		cloned[index].SecurityNotices = append([]string(nil), cloned[index].SecurityNotices...)
		if cloned[index].Operation != nil {
			operation := *cloned[index].Operation
			cloned[index].Operation = &operation
		}
	}
	return cloned
}

func (s *Server) buildAppList(ctx context.Context) ([]appInfo, error) {
	apps, err := s.appRegistry()
	if err != nil {
		return nil, err
	}
	visible := make([]appHandler, 0, len(apps))
	for _, app := range apps {
		if !isAppHiddenFromStore(app.Definition().ID) {
			visible = append(visible, app)
		}
	}
	resp := collectAppInfos(ctx, visible, appInfoWorkerLimit)
	var electrsAvailability *fullIndexAppAvailability
	var mempoolAvailability *fullIndexAppAvailability
	for index := range resp {
		if resp[index].ID == electrsAppID || resp[index].ID == mempoolAppID {
			if mempoolAvailability == nil {
				availability := s.fullIndexAppAvailability(ctx)
				mempoolAvailability = &availability
			}
			if resp[index].ID == electrsAppID {
				if electrsAvailability == nil {
					availability := s.electrsAppAvailabilityFromBase(ctx, *mempoolAvailability)
					electrsAvailability = &availability
				}
				resp[index].Available = electrsAvailability.Available
				resp[index].UnavailableReason = electrsAvailability.Reason
				resp[index].UnavailableMessage = electrsAvailability.Message
			} else {
				resp[index].Available = mempoolAvailability.Available
				resp[index].UnavailableReason = mempoolAvailability.Reason
				resp[index].UnavailableMessage = mempoolAvailability.Message
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return resp, nil
}

func collectAppInfos(ctx context.Context, apps []appHandler, workerLimit int) []appInfo {
	resp := make([]appInfo, len(apps))
	if len(apps) == 0 {
		return resp
	}
	if workerLimit < 1 {
		workerLimit = 1
	}
	if workerLimit > len(apps) {
		workerLimit = len(apps)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerLimit)
	for worker := 0; worker < workerLimit; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				app := apps[index]
				info, infoErr := app.Info(ctx)
				if infoErr != nil {
					if info.ID == "" {
						info = newAppInfo(app.Definition())
					}
					if info.Installed {
						info.Status = "unknown"
					}
				}
				resp[index] = info
			}
		}()
	}
	for index := range apps {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return resp
}

func (s *Server) handleAppOperations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.appOperationSnapshot())
}

func isAppHiddenFromStore(id string) bool {
	switch id {
	case depixBuyAppID:
		return true
	default:
		return false
	}
}

func (s *Server) handleAppInstall(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	app, err := s.appByID(appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	finishOperation, started := s.beginAppOperation(appID, "install")
	if !started {
		writeErrorCode(w, http.StatusConflict, "app_operation_in_progress", "app operation already in progress")
		return
	}
	defer finishOperation()
	if err := ensureAppStorageRoots(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if appID == bitcoinCoreAppID {
		var req bitcoinCoreInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installBitcoinCoreWithOptions(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.invalidateBitcoinStatusCaches()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == elementsAppID {
		var req elementsInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installElementsWithOptions(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == peerswapAppID {
		var req peerswapInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installPeerswapWithOptions(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == electrsAppID {
		var req electrsInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installElectrsWithOptions(r.Context(), req); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errElectrsBitcoinRestartConfirmationRequired) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == mempoolAppID {
		var req mempoolInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installMempoolWithOptions(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == fedimintGatewayAppID {
		var req fedimintGatewayStartOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.installFedimintGatewayWithOptions(r.Context(), req); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errFedimintGatewayBitcoinRestartConfirmationRequired) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	appContext := r.Context()
	if appID == appmanifest.LNDgID {
		appContext = withLNDgAccessHost(appContext, r.Host)
	}
	if err := app.Install(appContext); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if appID == "bitcoincore" {
		s.invalidateBitcoinStatusCaches()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppUninstall(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	app, err := s.appByID(appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	var req appUninstallRequest
	if err := readJSON(r, &req); err != nil || validateAppUninstallConfirmation(appID, req) != nil {
		writeErrorCode(w, http.StatusConflict, "app_uninstall_confirmation_required", errAppUninstallConfirmationRequired.Error())
		return
	}
	if appID == appmanifest.BitcoinCoreID {
		remote := resolveBitcoinRemoteRPCConfig(s.cfg)
		if err := validateBitcoinCoreUninstallSource(readBitcoinSource(), remote); err != nil {
			code := "bitcoin_remote_source_required"
			if errors.Is(err, errBitcoinCoreRemoteCredentials) {
				code = "bitcoin_remote_credentials_missing"
			}
			writeErrorCode(w, http.StatusConflict, code, err.Error())
			return
		}
		probeCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		ready, reason := bitcoinRPCReady(probeCtx, remote)
		cancel()
		if !ready {
			message := "the configured remote Bitcoin RPC is unavailable; test it before uninstalling Bitcoin Core"
			if reason == "syncing" {
				message = "the configured remote Bitcoin node is not fully synchronized"
			}
			writeErrorCode(w, http.StatusConflict, "bitcoin_remote_not_ready", message)
			return
		}
	}
	finishOperation, started := s.beginAppOperation(appID, "uninstall")
	if !started {
		writeErrorCode(w, http.StatusConflict, "app_operation_in_progress", "app operation already in progress")
		return
	}
	defer finishOperation()
	if err := app.Uninstall(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if appID == "bitcoincore" {
		s.invalidateBitcoinStatusCaches()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	app, err := s.appByID(appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	finishOperation, started := s.beginAppOperation(appID, "start")
	if !started {
		writeErrorCode(w, http.StatusConflict, "app_operation_in_progress", "app operation already in progress")
		return
	}
	defer finishOperation()
	if appID == electrsAppID {
		var req electrsInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.startElectrsWithOptions(r.Context(), req); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errElectrsBitcoinRestartConfirmationRequired) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == mempoolAppID {
		var req mempoolInstallOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.startMempoolWithOptions(r.Context(), req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if appID == fedimintGatewayAppID {
		var req fedimintGatewayStartOptions
		if r.ContentLength != 0 {
			if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
		}
		if err := s.startFedimintGatewayWithOptions(r.Context(), req); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, errFedimintGatewayBitcoinRestartConfirmationRequired) {
				status = http.StatusConflict
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	appContext := r.Context()
	if appID == appmanifest.LNDgID {
		appContext = withLNDgAccessHost(appContext, r.Host)
	}
	if err := app.Start(appContext); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if appID == "bitcoincore" {
		s.invalidateBitcoinStatusCaches()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	app, err := s.appByID(appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if app == nil {
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	finishOperation, started := s.beginAppOperation(appID, "stop")
	if !started {
		writeErrorCode(w, http.StatusConflict, "app_operation_in_progress", "app operation already in progress")
		return
	}
	defer finishOperation()
	if err := app.Stop(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if appID == "bitcoincore" {
		s.invalidateBitcoinStatusCaches()
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAppResetAdmin(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	if appID != "lndg" && appID != barkWalletAppID {
		writeError(w, http.StatusBadRequest, "reset not supported for this app")
		return
	}
	var err error
	if appID == barkWalletAppID {
		err = s.resetBarkWalletAdminPassword(r.Context())
	} else {
		err = s.resetLndgAdminPassword(r.Context())
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleElectrsStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.fetchElectrsStatus(r.Context()))
}

func (s *Server) handleAppAdminPassword(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "missing app id")
		return
	}
	if appID != "lndg" && appID != fedimintGatewayAppID && appID != barkWalletAppID {
		writeError(w, http.StatusBadRequest, "admin password not available for this app")
		return
	}

	var password string
	if appID == "lndg" {
		paths := lndgAppPaths()
		password = readSecretFile(paths.AdminPasswordPath)
		if password == "" {
			password = readEnvValue(paths.EnvPath, "LNDG_ADMIN_PASSWORD")
		}
	} else if appID == fedimintGatewayAppID {
		password = readSecretFile(fedimintGatewayAppPaths().AdminPasswordPath)
	} else {
		handled, brokerPassword, err := system.ReadBarkWalletPasswordWithBroker(r.Context())
		if !handled {
			writeError(w, http.StatusInternalServerError, "Bark Wallet password requires privileged broker enforce mode")
			return
		}
		if err != nil {
			// Promote a legacy install once so the manager never reads its
			// manager-owned secret directly after the broker cutover.
			if handled, ensureErr := system.EnsureBarkWalletWithBroker(r.Context()); !handled || ensureErr != nil {
				writeError(w, http.StatusNotFound, "admin password unavailable")
				return
			}
			handled, brokerPassword, err = system.ReadBarkWalletPasswordWithBroker(r.Context())
			if !handled || err != nil {
				writeError(w, http.StatusNotFound, "admin password unavailable")
				return
			}
		}
		password = brokerPassword
	}
	if password == "" {
		writeError(w, http.StatusNotFound, "admin password unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"password": password})
}

func (s *Server) handleBarkWalletRevealAuthorization(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if !s.requireSensitiveReauth(
		w,
		r,
		authScopeBarkSeedReveal,
		"",
		"bark_seed_reauth_required",
		"password confirmation required before revealing the Bark Wallet recovery phrase",
	) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
