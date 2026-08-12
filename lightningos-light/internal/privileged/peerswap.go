package privileged

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

type NativePeerSwapManager struct {
	Runner           CommandRunner
	Paths            appmanifest.PeerSwapPaths
	StateRoot        string
	AppsRoot         string
	AppsDataRoot     string
	StagedAssetsRoot string
}

func NewNativePeerSwapManager(runner CommandRunner) *NativePeerSwapManager {
	return &NativePeerSwapManager{
		Runner:           runner,
		Paths:            appmanifest.DefaultPeerSwapPaths(),
		StateRoot:        appmanifest.PeerSwapStateRoot,
		AppsRoot:         appmanifest.PeerSwapAppsRoot,
		AppsDataRoot:     appmanifest.PeerSwapAppsDataRoot,
		StagedAssetsRoot: appmanifest.PeerSwapStagedAssetsRoot,
	}
}

func (manager *NativePeerSwapManager) Status(ctx context.Context) (PeerSwapState, error) {
	if manager == nil || manager.Runner == nil {
		return PeerSwapState{}, errors.New("PeerSwap command runner is unavailable")
	}
	state := PeerSwapState{
		Installed:      safeNonEmptyRegularFile(manager.Paths.PeerswapdPath) && safeNonEmptyRegularFile(manager.Paths.PSCLIPath) && safeNonEmptyRegularFile(manager.Paths.PSWebPath),
		Status:         "stopped",
		HasLNDMacaroon: safeNonEmptyRegularFile(manager.Paths.LNDMacaroonPath),
	}
	if source, err := manager.Source(ctx); err == nil && source.Configured {
		state.ElementsMode = source.Source.Mode
	}
	if !state.Installed {
		return state, nil
	}
	daemon, daemonErr := manager.serviceStatus(ctx, appmanifest.PeerSwapService)
	web, webErr := manager.serviceStatus(ctx, appmanifest.PeerSwapWebService)
	if daemonErr != nil || webErr != nil {
		state.Status = "unknown"
		return state, errors.New("PeerSwap service status failed")
	}
	if daemon == "running" && web == "running" {
		state.Status = "running"
	}
	return state, nil
}

func (manager *NativePeerSwapManager) Source(_ context.Context) (PeerSwapSourceState, error) {
	if _, err := os.Lstat(manager.Paths.DataRoot); os.IsNotExist(err) {
		return PeerSwapSourceState{}, nil
	} else if err != nil || validateFixedParent(manager.stateRoot(), manager.Paths.DataRoot) != nil {
		return PeerSwapSourceState{}, errors.New("PeerSwap data root is unsafe")
	}
	raw, err := readRegularFile(manager.Paths.ElementsSourcePath, 16*1024)
	if os.IsNotExist(err) {
		return PeerSwapSourceState{}, nil
	}
	if err != nil {
		return PeerSwapSourceState{}, errors.New("PeerSwap Elements source read failed")
	}
	var source PeerSwapSource
	if err := json.Unmarshal(raw, &source); err != nil {
		return PeerSwapSourceState{}, errors.New("PeerSwap Elements source is invalid")
	}
	source.Mode = strings.ToLower(strings.TrimSpace(source.Mode))
	if source.Mode == "" {
		source.Mode = appmanifest.PeerSwapElementsModeLocal
	}
	if strings.TrimSpace(source.Wallet) == "" {
		source.Wallet = "peerswap"
	}
	if err := appmanifest.ValidatePeerSwapSource(source.Mode, source.URL, source.User, source.Password, source.Wallet); err != nil {
		return PeerSwapSourceState{}, err
	}
	return PeerSwapSourceState{Configured: true, Source: source}, nil
}

func (manager *NativePeerSwapManager) WriteSource(_ context.Context, source PeerSwapSource, dryRun bool) error {
	if err := appmanifest.ValidatePeerSwapSource(source.Mode, source.URL, source.User, source.Password, source.Wallet); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	if err := manager.ensureDataRoot(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return errors.New("PeerSwap Elements source encoding failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.ElementsSourcePath, append(raw, '\n'), 0600); err != nil {
		return errors.New("PeerSwap Elements source write failed")
	}
	if err := setLoopFileOwnership(manager.Paths.ElementsSourcePath, "root", "root"); err != nil {
		return errors.New("PeerSwap Elements source ownership failed")
	}
	return os.Chmod(manager.Paths.ElementsSourcePath, 0600)
}

func (manager *NativePeerSwapManager) Ensure(ctx context.Context, params PeerSwapEnsureParams, dryRun bool) (PeerSwapState, error) {
	if err := appmanifest.ValidatePeerSwapMaterial(params.LNDTLSCertificate, params.LNDMacaroon); err != nil {
		return PeerSwapState{}, err
	}
	if err := appmanifest.ValidatePeerSwapConfig(params.Config, params.ElementsMode, manager.Paths); err != nil {
		return PeerSwapState{}, err
	}
	if err := appmanifest.ValidatePeerSwapWebConfig(params.WebConfig, manager.Paths); err != nil {
		return PeerSwapState{}, err
	}
	source, err := manager.Source(ctx)
	if err != nil || !source.Configured || source.Source.Mode != params.ElementsMode {
		return PeerSwapState{}, errors.New("PeerSwap Elements source is not configured")
	}
	if err := manager.verifyStagedBinaries(); err != nil {
		return PeerSwapState{}, err
	}
	if dryRun {
		return PeerSwapState{Status: "validated", ElementsMode: params.ElementsMode}, nil
	}
	if err := manager.ensureIdentity(ctx); err != nil {
		return PeerSwapState{}, err
	}
	if err := manager.ensureDirectories(ctx); err != nil {
		return PeerSwapState{}, err
	}
	// Existing installations stored the remote Elements credential as the
	// manager identity. Rewriting the already validated, normalized policy
	// through the typed owner keeps the choice intact while making it root-only.
	if err := manager.WriteSource(ctx, source.Source, false); err != nil {
		return PeerSwapState{}, err
	}
	if err := manager.migrateLegacyData(); err != nil {
		return PeerSwapState{}, err
	}
	if err := ensureFixedDirectory(manager.stateRoot(), manager.Paths.LNDDir, 0750); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap LND directory creation failed")
	}
	if err := setLoopDirectoryOwnership(manager.Paths.LNDDir, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap LND directory ownership failed")
	}
	if err := os.Chmod(manager.Paths.LNDDir, 0750|os.ModeSetgid); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap LND directory permissions failed")
	}
	if err := manager.installBinaries(); err != nil {
		return PeerSwapState{}, err
	}
	if err := manager.installLNDMaterial(params); err != nil {
		return PeerSwapState{}, err
	}
	existingConfig := ""
	if raw, readErr := readRegularFile(manager.Paths.ConfigPath, 64*1024); readErr == nil {
		existingConfig = string(raw)
	} else if !os.IsNotExist(readErr) {
		return PeerSwapState{}, errors.New("PeerSwap configuration read failed")
	}
	config, err := appmanifest.MergePeerSwapConfig(existingConfig, params.Config, params.ElementsMode, manager.Paths)
	if err != nil {
		return PeerSwapState{}, errors.New("PeerSwap configuration merge failed")
	}
	if err := manager.writeOwnedFile(manager.Paths.ConfigPath, []byte(config), 0600, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap configuration write failed")
	}
	existingWeb := ""
	if raw, readErr := readRegularFile(manager.Paths.PSWebConfigPath, 64*1024); readErr == nil {
		existingWeb = string(raw)
	} else if !os.IsNotExist(readErr) {
		return PeerSwapState{}, errors.New("PeerSwap web configuration read failed")
	}
	web, err := appmanifest.MergePeerSwapWebConfig(existingWeb, params.WebConfig, peerSwapBitcoinSwaps(config))
	if err != nil {
		return PeerSwapState{}, errors.New("PeerSwap web configuration merge failed")
	}
	if err := manager.writeOwnedFile(manager.Paths.PSWebConfigPath, []byte(web), 0600, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap web configuration write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.ServicePath, []byte(appmanifest.PeerSwapServiceUnit(manager.Paths, params.ElementsMode)), 0644); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap service write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.WebServicePath, []byte(appmanifest.PeerSwapWebServiceUnit(manager.Paths)), 0644); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap web service write failed")
	}
	if _, err := manager.Runner.Run(ctx, systemdAnalyzePath, "verify", manager.Paths.ServicePath, manager.Paths.WebServicePath); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap service validation failed")
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "daemon-reload"); err != nil {
		return PeerSwapState{}, errors.New("PeerSwap systemd reload failed")
	}
	return manager.Status(ctx)
}

func (manager *NativePeerSwapManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (PeerSwapState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop && action != AppLifecycleRestart {
		return PeerSwapState{}, errors.New("PeerSwap lifecycle action is not allowed")
	}
	if dryRun {
		return PeerSwapState{Status: "validated"}, nil
	}
	if !safeNonEmptyRegularFile(manager.Paths.PeerswapdPath) || !safeNonEmptyRegularFile(manager.Paths.PSWebPath) || !safeNonEmptyRegularFile(manager.Paths.LNDMacaroonPath) {
		return PeerSwapState{}, errors.New("PeerSwap is not ready")
	}
	if source, err := manager.Source(ctx); err != nil || !source.Configured {
		return PeerSwapState{}, errors.New("PeerSwap Elements source is not configured")
	} else if source.Source.Mode == appmanifest.PeerSwapElementsModeLocal {
		if status, _ := manager.serviceStatus(ctx, appmanifest.ElementsService); status != "running" {
			return PeerSwapState{}, errors.New("local Elements must be running before PeerSwap")
		}
	}
	switch action {
	case AppLifecycleStart:
		if _, err := manager.Runner.Run(ctx, systemctlPath, "enable", "--now", appmanifest.PeerSwapService); err != nil {
			return PeerSwapState{}, errors.New("PeerSwap daemon start failed")
		}
		if _, err := manager.Runner.Run(ctx, systemctlPath, "enable", "--now", appmanifest.PeerSwapWebService); err != nil {
			_, _ = manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.PeerSwapService)
			return PeerSwapState{}, errors.New("PeerSwap web start failed")
		}
	case AppLifecycleStop:
		_, webErr := manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.PeerSwapWebService)
		_, daemonErr := manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.PeerSwapService)
		if webErr != nil || daemonErr != nil {
			return PeerSwapState{}, errors.New("PeerSwap stop failed")
		}
	case AppLifecycleRestart:
		if _, err := manager.Runner.Run(ctx, systemctlPath, "restart", appmanifest.PeerSwapService); err != nil {
			return PeerSwapState{}, errors.New("PeerSwap daemon restart failed")
		}
		if _, err := manager.Runner.Run(ctx, systemctlPath, "restart", appmanifest.PeerSwapWebService); err != nil {
			return PeerSwapState{}, errors.New("PeerSwap web restart failed")
		}
	}
	return manager.Status(ctx)
}

func (manager *NativePeerSwapManager) Remove(ctx context.Context, dryRun bool) error {
	if dryRun {
		return nil
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.PeerSwapWebService)
	_, _ = manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.PeerSwapService)
	for _, unit := range []string{manager.Paths.WebServicePath, manager.Paths.ServicePath} {
		if err := removeFixedRegularFile(unit); err != nil {
			return err
		}
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "daemon-reload")
	if err := removeFixedTree(manager.Paths.Root, manager.appsRoot()); err != nil {
		return err
	}
	// Persistent swap state, policy, dedicated credential and Elements source
	// intentionally survive an App Store uninstall.
	return nil
}

func (manager *NativePeerSwapManager) ensureIdentity(ctx context.Context) error {
	if _, err := manager.Runner.Run(ctx, getentPath, "group", appmanifest.PeerSwapUser); err != nil {
		if _, err := manager.Runner.Run(ctx, groupaddPath, "--system", appmanifest.PeerSwapUser); err != nil {
			return errors.New("failed to create PeerSwap group")
		}
	}
	if _, err := manager.Runner.Run(ctx, idPath, "-u", appmanifest.PeerSwapUser); err != nil {
		if _, err := manager.Runner.Run(ctx, useraddPath, "--system", "--gid", appmanifest.PeerSwapUser, "--home-dir", manager.Paths.RuntimeDir, "--no-create-home", "--shell", "/usr/sbin/nologin", appmanifest.PeerSwapUser); err != nil {
			return errors.New("failed to create PeerSwap user")
		}
	}
	return nil
}

func (manager *NativePeerSwapManager) ensureDataRoot() error {
	if err := validateExistingDirectory(manager.stateRoot()); err != nil {
		return errors.New("PeerSwap state root is unavailable")
	}
	if err := validateExistingDirectory(manager.appsDataRoot()); err != nil {
		return errors.New("PeerSwap apps data root is unavailable")
	}
	if err := ensureFixedDirectory(manager.stateRoot(), manager.Paths.DataRoot, 0751); err != nil {
		return errors.New("PeerSwap data root creation failed")
	}
	if err := setLoopDirectoryOwnership(manager.Paths.DataRoot, "root", "root"); err != nil {
		return errors.New("PeerSwap data root ownership failed")
	}
	return os.Chmod(manager.Paths.DataRoot, 0751)
}

func (manager *NativePeerSwapManager) ensureDirectories(ctx context.Context) error {
	if err := manager.ensureDataRoot(); err != nil {
		return err
	}
	for _, dir := range []string{manager.appsRoot(), manager.Paths.Root, manager.Paths.BinDir, manager.Paths.RuntimeDir} {
		if err := ensureFixedDirectory(manager.stateRoot(), dir, 0750); err != nil {
			return errors.New("PeerSwap directory creation failed")
		}
	}
	for _, parent := range []string{manager.stateRoot(), manager.appsRoot(), manager.appsDataRoot(), manager.Paths.DataRoot} {
		if _, err := manager.Runner.Run(ctx, setfaclPath, "-m", "u:"+appmanifest.PeerSwapUser+":--x", parent); err != nil {
			return errors.New("PeerSwap directory traversal permission failed")
		}
	}
	if err := setLoopTreeOwnership([]string{manager.Paths.Root}, appmanifest.PeerSwapUser, appmanifest.PeerSwapManagerGroup); err != nil {
		return errors.New("PeerSwap application ownership failed")
	}
	if err := setLoopTreeOwnership([]string{manager.Paths.RuntimeDir}, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return errors.New("PeerSwap runtime ownership failed")
	}
	for _, dir := range []string{manager.Paths.Root, manager.Paths.BinDir, manager.Paths.RuntimeDir} {
		if err := os.Chmod(dir, 0750|os.ModeSetgid); err != nil {
			return errors.New("PeerSwap directory permissions failed")
		}
	}
	return nil
}

func (manager *NativePeerSwapManager) verifyStagedBinaries() error {
	for _, asset := range appmanifest.PeerSwapBinaries() {
		path := filepath.Join(manager.stagedAssetsRoot(), asset.Name)
		raw, err := readRegularFile(path, 128*1024*1024)
		if err != nil || len(raw) == 0 {
			return errors.New("PeerSwap staged binary is unavailable")
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != asset.SHA256 {
			return errors.New("PeerSwap staged binary checksum mismatch")
		}
	}
	return nil
}

func (manager *NativePeerSwapManager) installBinaries() error {
	for _, asset := range appmanifest.PeerSwapBinaries() {
		source := filepath.Join(manager.stagedAssetsRoot(), asset.Name)
		target := filepath.Join(manager.Paths.BinDir, asset.Name)
		raw, err := readRegularFile(source, 128*1024*1024)
		if err != nil {
			return errors.New("PeerSwap staged binary read failed")
		}
		if err := manager.writeOwnedFile(target, raw, 0755, appmanifest.PeerSwapUser, appmanifest.PeerSwapManagerGroup); err != nil {
			return errors.New("PeerSwap binary install failed")
		}
	}
	return manager.writeOwnedFile(manager.Paths.VersionPath, []byte(appmanifest.PeerSwapVersionMarker()+"\n"), 0640, appmanifest.PeerSwapUser, appmanifest.PeerSwapManagerGroup)
}

func (manager *NativePeerSwapManager) installLNDMaterial(params PeerSwapEnsureParams) error {
	if err := manager.writeOwnedFile(manager.Paths.LNDTLSCertPath, params.LNDTLSCertificate, 0600, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return errors.New("PeerSwap LND TLS certificate install failed")
	}
	if safeNonEmptyRegularFile(manager.Paths.LNDMacaroonPath) {
		return nil
	}
	if len(params.LNDMacaroon) == 0 {
		return errors.New("PeerSwap dedicated LND credential is required")
	}
	if admin, err := readSafeNonEmptyRegularFile("/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon"); err == nil && bytes.Equal(admin, params.LNDMacaroon) {
		return errors.New("PeerSwap must not use the LND admin macaroon")
	}
	if err := manager.writeOwnedFile(manager.Paths.LNDMacaroonPath, params.LNDMacaroon, 0600, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		return errors.New("PeerSwap LND macaroon install failed")
	}
	return nil
}

func (manager *NativePeerSwapManager) migrateLegacyData() error {
	legacyInfo, err := os.Lstat(manager.Paths.LegacyDataDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !legacyInfo.IsDir() || legacyInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("legacy PeerSwap data directory is unsafe")
	}
	entries, err := os.ReadDir(manager.Paths.RuntimeDir)
	if err != nil || len(entries) != 0 {
		return nil
	}
	if err := copyPeerSwapLegacyTree(manager.Paths.LegacyDataDir, manager.Paths.RuntimeDir); err != nil {
		_ = removeFixedTree(manager.Paths.RuntimeDir, manager.Paths.DataRoot)
		return err
	}
	if err := setLoopTreeOwnership([]string{manager.Paths.RuntimeDir}, appmanifest.PeerSwapUser, appmanifest.PeerSwapUser); err != nil {
		_ = removeFixedTree(manager.Paths.RuntimeDir, manager.Paths.DataRoot)
		return errors.New("legacy PeerSwap ownership migration failed")
	}
	return nil
}

func copyPeerSwapLegacyTree(source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return errors.New("legacy PeerSwap data read failed")
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("legacy PeerSwap data contains an unsafe entry")
		}
		switch {
		case info.IsDir():
			if err := os.Mkdir(destinationPath, 0700); err != nil && !os.IsExist(err) {
				return errors.New("legacy PeerSwap directory copy failed")
			}
			if err := copyPeerSwapLegacyTree(sourcePath, destinationPath); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			in, err := os.Open(sourcePath)
			if err != nil {
				return errors.New("legacy PeerSwap file read failed")
			}
			out, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				_ = in.Close()
				return errors.New("legacy PeerSwap file copy failed")
			}
			_, copyErr := io.Copy(out, in)
			closeOutErr, closeInErr := out.Close(), in.Close()
			if copyErr != nil || closeOutErr != nil || closeInErr != nil {
				return errors.New("legacy PeerSwap file copy failed")
			}
		default:
			// A stopped legacy service can leave a Unix socket behind. It is
			// recreated by PeerSwap and is intentionally not migrated.
			continue
		}
	}
	return nil
}

func (manager *NativePeerSwapManager) writeOwnedFile(path string, raw []byte, mode os.FileMode, owner, group string) error {
	if err := writeAtomicRegularFile(path, raw, mode); err != nil {
		return err
	}
	if err := setLoopFileOwnership(path, owner, group); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (manager *NativePeerSwapManager) serviceStatus(ctx context.Context, service string) (string, error) {
	output, err := manager.Runner.Run(ctx, systemctlPath, "show", service, "--property=ActiveState", "--property=SubState", "--no-pager")
	status := parseLoopServiceState(output)
	if status == "unknown" && err != nil {
		return status, err
	}
	return status, nil
}

func peerSwapBitcoinSwaps(config string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(config, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "bitcoinswaps") {
			parsed, _ := strconv.ParseBool(strings.TrimSpace(value))
			return parsed
		}
	}
	return false
}

func (manager *NativePeerSwapManager) stateRoot() string {
	if manager.StateRoot != "" {
		return manager.StateRoot
	}
	return appmanifest.PeerSwapStateRoot
}

func (manager *NativePeerSwapManager) appsRoot() string {
	if manager.AppsRoot != "" {
		return manager.AppsRoot
	}
	return appmanifest.PeerSwapAppsRoot
}

func (manager *NativePeerSwapManager) appsDataRoot() string {
	if manager.AppsDataRoot != "" {
		return manager.AppsDataRoot
	}
	return appmanifest.PeerSwapAppsDataRoot
}

func (manager *NativePeerSwapManager) stagedAssetsRoot() string {
	if manager.StagedAssetsRoot != "" {
		return manager.StagedAssetsRoot
	}
	return appmanifest.PeerSwapStagedAssetsRoot
}
