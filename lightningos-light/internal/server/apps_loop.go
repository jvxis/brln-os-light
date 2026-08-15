package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/system"
)

const (
	loopAppID       = appmanifest.LoopID
	loopVersion     = appmanifest.LoopVersion
	loopUser        = appmanifest.LoopUser
	loopStateRoot   = appmanifest.LoopStateRoot
	loopServiceName = appmanifest.LoopService
	loopRPCPort     = appmanifest.LoopRPCPort
	loopRESTPort    = appmanifest.LoopRESTPort
)

type loopPaths = appmanifest.LoopPaths

type loopApp struct{ server *Server }

func newLoopApp(s *Server) appHandler { return loopApp{server: s} }

func loopDefinition() appDefinition {
	return appDefinition{
		ID:          loopAppID,
		Name:        "Lightning Loop",
		Description: "Optional non-custodial submarine swaps for moving liquidity between Lightning channels and the Bitcoin wallet. Manual quotes and swaps only; Autoloop is disabled.",
		Port:        0,
		SecurityNotices: []string{
			appSecurityNoticeElevatedLNDAccess,
		},
	}
}

func (a loopApp) Definition() appDefinition { return loopDefinition() }

func (a loopApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("Lightning Loop status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	if !state.Installed {
		return info, nil
	}
	info.Installed = true
	info.Status = state.Status
	return info, nil
}

func loopDisplayServiceState(ctx context.Context) (string, error) {
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return "unknown", errors.New("Lightning Loop status requires privileged broker enforce mode")
	}
	return state.Status, err
}

func parseLoopSystemdState(output string) string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if values["ActiveState"] == "active" && values["SubState"] == "running" {
		return "running"
	}
	if values["ActiveState"] == "inactive" || values["ActiveState"] == "failed" ||
		values["ActiveState"] == "activating" || values["ActiveState"] == "deactivating" {
		return "stopped"
	}
	return "unknown"
}

func (a loopApp) Install(ctx context.Context) error   { return a.server.installLoop(ctx) }
func (a loopApp) Uninstall(ctx context.Context) error { return a.server.uninstallLoop(ctx) }
func (a loopApp) Start(ctx context.Context) error     { return a.server.startLoop(ctx) }
func (a loopApp) Stop(ctx context.Context) error      { return a.server.stopLoop(ctx) }

func loopAppPaths() loopPaths {
	return appmanifest.DefaultLoopPaths()
}

type loopReleaseAsset = appmanifest.LoopReleaseAsset

func loopAssetForArch(goarch string) (loopReleaseAsset, error) {
	return appmanifest.LoopAssetForArch(goarch)
}

func (s *Server) installLoop(ctx context.Context) error {
	return s.applyLoop(ctx)
}

func (s *Server) startLoop(ctx context.Context) error {
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return errors.New("Lightning Loop lifecycle requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return errors.New("Lightning Loop is not installed")
	}
	return s.applyLoop(ctx)
}

func (s *Server) applyLoop(ctx context.Context) error {
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return errors.New("Lightning Loop preparation requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	tlsCertificate, macaroon, err := s.loopLNDMaterial(ctx, state.HasLNDMacaroon)
	if err != nil {
		return err
	}
	if handled, err := system.EnsureLoopWithBroker(ctx, tlsCertificate, macaroon); !handled {
		return errors.New("Lightning Loop preparation requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	if handled, err := system.LoopLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("Lightning Loop lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return loopServiceStartError(ctx, loopAppPaths(), err)
	}
	if err := waitForLoopServiceStable(ctx); err != nil {
		return loopServiceStartError(ctx, loopAppPaths(), err)
	}
	return nil
}

func (s *Server) stopLoop(ctx context.Context) error {
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return errors.New("Lightning Loop lifecycle requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return errors.New("Lightning Loop is not installed")
	}
	if state.Status != "stopped" {
		if err := s.ensureNoPendingLoopSwaps(ctx, "stop"); err != nil {
			return err
		}
	}
	if handled, err := system.LoopLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("Lightning Loop lifecycle requires privileged broker enforce mode")
	} else {
		return err
	}
}

func (s *Server) uninstallLoop(ctx context.Context) error {
	handled, state, err := system.LoopStatusWithBroker(ctx)
	if !handled {
		return errors.New("Lightning Loop removal requires privileged broker enforce mode")
	}
	if err != nil {
		return err
	}
	if !state.Installed {
		return nil
	}
	verifyPendingSwaps, blockUninstall := loopUninstallSafetyDecision(state.Status, state.HasPersistentState)
	if blockUninstall {
		return errors.New("start Lightning Loop before uninstalling so pending swaps can be verified")
	}
	// A daemon can be reported as active while it is still waiting for LND
	// and has not created its API certificate or swap database. With no
	// persistent swap state there is nothing to verify, so a failed or partial
	// first installation must remain removable.
	if verifyPendingSwaps {
		if err := s.ensureNoPendingLoopSwaps(ctx, "uninstall"); err != nil {
			return err
		}
	}
	if handled, err := system.RemoveLoopWithBroker(ctx); !handled {
		return errors.New("Lightning Loop removal requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	// Swap history, the loop database, TLS material, and the dedicated LND
	// macaroon deliberately remain in apps-data for a safe reinstall/recovery.
	return nil
}

func ensureLoopDirectories(ctx context.Context, paths loopPaths) error {
	handled, err := system.EnsureLoopPermissionsWithBroker(ctx)
	if !handled {
		return errors.New("Lightning Loop permissions require privileged broker enforce mode")
	}
	return err
}

// ensureLoopRuntimePermissions repairs installations affected by older
// installers that recursively reassigned /var/lib/lightningos to the manager.
// It runs once per manager process, before the first Loop API request, and
// restores the isolated daemon's ownership without deleting or recreating any
// wallet, swap, macaroon, or L402 token state.
func (s *Server) ensureLoopRuntimePermissions(ctx context.Context, paths loopPaths) error {
	s.loopPermissionsMu.Lock()
	defer s.loopPermissionsMu.Unlock()
	if s.loopPermissionsReady {
		return nil
	}
	if err := ensureLoopDirectories(ctx, paths); err != nil {
		return fmt.Errorf("failed to repair Lightning Loop data permissions: %w", err)
	}
	s.loopPermissionsReady = true
	return nil
}

// loopLNDMaterial keeps macaroon baking in the manager's typed LND client while
// delegating all privileged placement and ownership to the broker. Existing
// dedicated macaroons are preserved and never returned across the boundary.
func (s *Server) loopLNDMaterial(ctx context.Context, hasExistingMacaroon bool) ([]byte, []byte, error) {
	cert, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read LND TLS certificate: %w", err)
	}
	if len(cert) == 0 {
		return nil, nil, errors.New("LND TLS certificate is empty")
	}
	if !hasExistingMacaroon {
		if s.lnd == nil {
			return nil, nil, errors.New("LND client unavailable")
		}
		ids, err := s.lnd.ListMacaroonIDs(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list LND macaroon IDs: %w", err)
		}
		rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
		if err != nil {
			return nil, nil, err
		}
		baked, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
			Permissions:              loopMacaroonPermissions(),
			RootKeyID:                rootKeyID,
			AllowExternalPermissions: true,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to bake dedicated Lightning Loop macaroon: %w", err)
		}
		raw, err := hex.DecodeString(baked.MacaroonHex)
		if err != nil || len(raw) == 0 {
			return nil, nil, errors.New("invalid LND macaroon response")
		}
		return cert, raw, nil
	}
	return cert, nil, nil
}

func loopMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "read"}, {Entity: "address", Action: "write"},
		{Entity: "info", Action: "read"}, {Entity: "info", Action: "write"},
		{Entity: "invoices", Action: "read"}, {Entity: "invoices", Action: "write"},
		{Entity: "message", Action: "read"}, {Entity: "message", Action: "write"},
		{Entity: "offchain", Action: "read"}, {Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"}, {Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"}, {Entity: "peers", Action: "write"},
		{Entity: "signer", Action: "generate"}, {Entity: "signer", Action: "read"},
	}
}

func loopConfigContents(paths loopPaths) string {
	return appmanifest.LoopConfig(paths)
}

func loopServiceContents(paths loopPaths) string {
	return appmanifest.LoopServiceUnit(paths)
}

func loopPersistentSwapStateExists(paths loopPaths) bool {
	for _, path := range []string{
		paths.LoopDBPath,
		paths.LoopDBPath + "-wal",
		paths.LegacyLoopDB,
	} {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func loopUninstallSafetyDecision(status string, hasPersistentSwapState bool) (verify, block bool) {
	if !hasPersistentSwapState {
		return false, false
	}
	if status != "running" {
		return false, true
	}
	return true, false
}

func loopServiceStartError(ctx context.Context, paths loopPaths, cause error) error {
	details := readLoopFailureDetails(ctx, paths)
	if details == "" {
		return fmt.Errorf("failed to start Lightning Loop: %w", cause)
	}
	return fmt.Errorf("failed to start Lightning Loop: %w; recent loopd log: %s", cause, details)
}

func waitForLoopServiceStable(ctx context.Context) error {
	const (
		maxChecks     = 10
		stableChecks  = 3
		checkInterval = 200 * time.Millisecond
	)
	consecutiveRunning := 0
	lastState := "unknown"
	var lastErr error
	for check := 0; check < maxChecks; check++ {
		state, err := loopDisplayServiceState(ctx)
		if err != nil {
			lastErr = err
			consecutiveRunning = 0
		} else {
			lastState = state
			if state == "running" {
				consecutiveRunning++
				if consecutiveRunning >= stableChecks {
					return nil
				}
			} else {
				consecutiveRunning = 0
			}
		}
		if check == maxChecks-1 {
			break
		}
		timer := time.NewTimer(checkInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return fmt.Errorf("Lightning Loop service status could not be confirmed: %w", lastErr)
	}
	return fmt.Errorf("Lightning Loop service did not remain active (last state: %s)", lastState)
}

func readLoopFailureDetails(_ context.Context, paths loopPaths) string {
	if raw, err := os.ReadFile(paths.LoopLogPath); err == nil {
		if details := compactLoopFailureLog(string(raw), 12, 3500); details != "" {
			return details
		}
	}
	return ""
}

func compactLoopFailureLog(raw string, maxLines, maxChars int) string {
	raw = strings.ToValidUTF8(strings.ReplaceAll(raw, "\x00", ""), "?")
	lines := strings.Split(raw, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	if maxLines > 0 && len(filtered) > maxLines {
		filtered = filtered[len(filtered)-maxLines:]
	}
	result := strings.Join(filtered, " | ")
	runes := []rune(result)
	if maxChars > 0 && len(runes) > maxChars {
		result = "..." + string(runes[len(runes)-maxChars:])
	}
	return result
}

func ensureLoopClientMaterial(ctx context.Context, paths loopPaths) error {
	cert, certErr := os.ReadFile(paths.ClientTLSCert)
	macaroon, macaroonErr := os.ReadFile(paths.ClientMacaroon)
	if certErr == nil && len(cert) > 0 && macaroonErr == nil && len(macaroon) > 0 {
		return nil
	}
	if handled, err := system.EnsureLoopClientMaterialWithBroker(ctx); !handled {
		return errors.New("Lightning Loop API material requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("failed to prepare manager access to the Lightning Loop API: %w", err)
	}
	cert, certErr = os.ReadFile(paths.ClientTLSCert)
	macaroon, macaroonErr = os.ReadFile(paths.ClientMacaroon)
	if certErr != nil {
		return fmt.Errorf("Lightning Loop API certificate remains unreadable after repair: %w", certErr)
	}
	if len(cert) == 0 {
		return errors.New("Lightning Loop API certificate remains empty after repair")
	}
	if macaroonErr != nil {
		return fmt.Errorf("Lightning Loop API macaroon remains unreadable after repair: %w", macaroonErr)
	}
	if len(macaroon) == 0 {
		return errors.New("Lightning Loop API macaroon remains empty after repair")
	}
	return nil
}

func validateLoopDaemonMaterial(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("Lightning Loop daemon %s is unavailable at %s: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Lightning Loop daemon %s must not be a symbolic link: %s", label, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Lightning Loop daemon %s is not a regular file: %s", label, path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("Lightning Loop daemon %s is empty: %s", label, path)
	}
	return nil
}

func isPendingLoopState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "SUCCESS", "FAILED":
		return false
	default:
		return true
	}
}
