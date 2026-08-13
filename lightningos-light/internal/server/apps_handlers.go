package server

import (
	"errors"
	"io"
	"net/http"

	"lightningos-light/internal/system"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	apps, err := s.appRegistry()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := make([]appInfo, 0, len(apps))
	var fullIndexAvailability *fullIndexAppAvailability
	for _, app := range apps {
		if isAppHiddenFromStore(app.Definition().ID) {
			continue
		}
		info, infoErr := app.Info(r.Context())
		if infoErr != nil {
			if info.ID == "" {
				info = newAppInfo(app.Definition())
			}
			if info.Installed {
				info.Status = "unknown"
			}
		}
		if info.ID == electrsAppID || info.ID == mempoolAppID {
			if fullIndexAvailability == nil {
				availability := s.fullIndexAppAvailability(r.Context())
				fullIndexAvailability = &availability
			}
			info.Available = fullIndexAvailability.Available
			info.UnavailableReason = fullIndexAvailability.Reason
			info.UnavailableMessage = fullIndexAvailability.Message
		}
		resp = append(resp, info)
	}
	writeJSON(w, http.StatusOK, resp)
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
	if err := app.Install(r.Context()); err != nil {
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
	if err := app.Start(r.Context()); err != nil {
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
