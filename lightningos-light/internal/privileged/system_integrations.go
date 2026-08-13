package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultTerminalHelperPath         = "/usr/local/sbin/lightningos-terminal"
	defaultTerminalPasswordHelperPath = "/usr/local/sbin/lightningos-terminal-password"
	defaultManagerFirewallHelperPath  = "/usr/local/sbin/lightningos-manager-firewall"
	defaultManagerTLSMDNSHelperPath   = "/usr/local/sbin/lightningos-setup-manager-tls-mdns"
	defaultLNDIntegrationDropInPath   = "/etc/systemd/system/lnd.service.d/20-lightningos-restart.conf"
	defaultManagerTLSCertificatePath  = "/etc/lightningos/tls/server.crt"
	defaultSystemIntegrationsMarker   = "/var/lib/lightningos-privileged/system-integrations-20260813-v6"
	envExecutablePath                 = "/usr/bin/env"
	idExecutablePath                  = "/usr/bin/id"

	terminalHelperSHA256         = "6958cc1aaee60b009774014a95cdee080e011944aa6ef23457ef5788a7ccd859"
	terminalPasswordHelperSHA256 = "b647d8acfeaf604f7419e175c17c24e41ad18361530b685cea29f1aa084d5b68"
	managerFirewallHelperSHA256  = "d1defb2adf65db0c2738c3c58033155f2b9c8ac87694b1127fff615b62ab7123"
	managerTLSMDNSHelperSHA256   = "7ff9f17061eec00aec8eedc389044b0cf283e639f8e8f0cfda4ca47c6074b8f9"

	lndIntegrationDropIn            = "[Service]\nRestart=always\nRestartSec=60\n"
	systemIntegrationsMarkerContent = "ready\n"
)

var systemIntegrationIdentityPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type SystemIntegrationsManager interface {
	InstallAsset(ctx context.Context, params SystemIntegrationAssetInstallParams, dryRun bool) (SystemIntegrationAssetState, error)
	Status(ctx context.Context) (SystemIntegrationsState, error)
	Apply(ctx context.Context, dryRun bool) (SystemIntegrationsState, error)
	Finalize(ctx context.Context, dryRun bool) (SystemIntegrationsState, error)
}

func (manager *NativeSystemIntegrationsManager) Status(_ context.Context) (SystemIntegrationsState, error) {
	if manager == nil {
		return SystemIntegrationsState{}, errors.New("system integrations manager is unavailable")
	}
	current, err := integrationFileMatches(manager.Paths.Marker, []byte(systemIntegrationsMarkerContent), 0644)
	if err != nil {
		return SystemIntegrationsState{}, err
	}
	if !current {
		return SystemIntegrationsState{Status: "absent"}, nil
	}
	if err := manager.verifyInstalledAssets(); err != nil {
		return SystemIntegrationsState{Status: "absent"}, nil
	}
	return SystemIntegrationsState{Status: "ready"}, nil
}

type SystemIntegrationPaths struct {
	TerminalHelper         string
	TerminalPasswordHelper string
	ManagerFirewallHelper  string
	ManagerTLSMDNSHelper   string
	LNDDropIn              string
	ManagerTLSCertificate  string
	Marker                 string
}

type systemIntegrationAssetSpec struct {
	Path   string
	SHA256 string
}

type NativeSystemIntegrationsManager struct {
	Runner  CommandRunner
	Paths   SystemIntegrationPaths
	Digests map[SystemIntegrationAsset]string
}

func NewNativeSystemIntegrationsManager(runner CommandRunner) *NativeSystemIntegrationsManager {
	return &NativeSystemIntegrationsManager{
		Runner: runner,
		Paths: SystemIntegrationPaths{
			TerminalHelper:         defaultTerminalHelperPath,
			TerminalPasswordHelper: defaultTerminalPasswordHelperPath,
			ManagerFirewallHelper:  defaultManagerFirewallHelperPath,
			ManagerTLSMDNSHelper:   defaultManagerTLSMDNSHelperPath,
			LNDDropIn:              defaultLNDIntegrationDropInPath,
			ManagerTLSCertificate:  defaultManagerTLSCertificatePath,
			Marker:                 defaultSystemIntegrationsMarker,
		},
	}
}

func (manager *NativeSystemIntegrationsManager) InstallAsset(_ context.Context, params SystemIntegrationAssetInstallParams, dryRun bool) (SystemIntegrationAssetState, error) {
	spec, err := manager.assetSpec(params.Asset)
	if err != nil {
		return SystemIntegrationAssetState{}, err
	}
	digest := sha256.Sum256([]byte(params.Content))
	if !strings.EqualFold(hex.EncodeToString(digest[:]), spec.SHA256) {
		return SystemIntegrationAssetState{}, errors.New("system integration asset digest is not allowed")
	}
	if dryRun {
		return SystemIntegrationAssetState{Status: "validated"}, nil
	}
	current, err := integrationFileMatches(spec.Path, []byte(params.Content), 0755)
	if err != nil {
		return SystemIntegrationAssetState{}, err
	}
	if current {
		return SystemIntegrationAssetState{Status: "ready"}, nil
	}
	if err := ensureDirectoryTreeNoSymlink(filepath.Dir(spec.Path), 0755); err != nil {
		return SystemIntegrationAssetState{}, errors.New("system integration asset directory is unsafe")
	}
	if err := refuseNonRegularDestination(spec.Path); err != nil {
		return SystemIntegrationAssetState{}, err
	}
	if err := writeAtomicRegularFile(spec.Path, []byte(params.Content), 0755); err != nil {
		return SystemIntegrationAssetState{}, errors.New("system integration asset installation failed")
	}
	if err := validateSystemIntegrationFile(spec.Path, 0755); err != nil {
		return SystemIntegrationAssetState{}, errors.New("system integration asset installation is unsafe")
	}
	return SystemIntegrationAssetState{Status: "ready", Changed: true}, nil
}

func (manager *NativeSystemIntegrationsManager) Apply(ctx context.Context, dryRun bool) (SystemIntegrationsState, error) {
	if manager == nil || manager.Runner == nil {
		return SystemIntegrationsState{}, errors.New("system integrations manager is unavailable")
	}
	if dryRun {
		return SystemIntegrationsState{Status: "validated"}, nil
	}
	if err := manager.verifyInstalledAssets(); err != nil {
		return SystemIntegrationsState{}, err
	}

	state := SystemIntegrationsState{Status: "ready"}
	loaded, err := manager.lndServiceLoaded(ctx)
	if err != nil {
		return SystemIntegrationsState{}, err
	}
	if loaded {
		current, err := integrationFileMatches(manager.Paths.LNDDropIn, []byte(lndIntegrationDropIn), 0644)
		if err != nil {
			return SystemIntegrationsState{}, err
		}
		if !current {
			if err := ensureDirectoryTreeNoSymlink(filepath.Dir(manager.Paths.LNDDropIn), 0755); err != nil {
				return SystemIntegrationsState{}, errors.New("LND integration directory is unsafe")
			}
			if err := refuseNonRegularDestination(manager.Paths.LNDDropIn); err != nil {
				return SystemIntegrationsState{}, err
			}
			if err := writeAtomicRegularFile(manager.Paths.LNDDropIn, []byte(lndIntegrationDropIn), 0644); err != nil {
				return SystemIntegrationsState{}, errors.New("LND integration policy update failed")
			}
			if err := validateSystemIntegrationFile(manager.Paths.LNDDropIn, 0644); err != nil {
				return SystemIntegrationsState{}, errors.New("LND integration policy is unsafe")
			}
			if _, err := manager.Runner.Run(ctx, systemctlPath, "daemon-reload"); err != nil {
				return SystemIntegrationsState{}, errors.New("systemd integration reload failed")
			}
			state.LNDPolicyChanged = true
		}
	}

	certificateBefore, err := optionalRegularFileSHA256(manager.Paths.ManagerTLSCertificate, 1024*1024)
	if err != nil {
		return SystemIntegrationsState{}, errors.New("manager certificate is unsafe")
	}
	managerGroup := manager.managerGroup(ctx)
	if _, err := manager.Runner.Run(ctx, envExecutablePath,
		"LIGHTNINGOS_MANAGER_GROUP="+managerGroup,
		"LIGHTNINGOS_MANAGER_PORT=8443",
		manager.Paths.ManagerTLSMDNSHelper,
	); err != nil {
		return SystemIntegrationsState{}, errors.New("manager TLS and mDNS integration failed")
	}
	certificateAfter, err := optionalRegularFileSHA256(manager.Paths.ManagerTLSCertificate, 1024*1024)
	if err != nil {
		return SystemIntegrationsState{}, errors.New("manager certificate is unsafe after reconciliation")
	}
	state.CertificateChanged = certificateAfter != "" && certificateAfter != certificateBefore

	if _, err := manager.Runner.Run(ctx, manager.Paths.ManagerFirewallHelper); err != nil {
		return SystemIntegrationsState{}, errors.New("manager firewall integration failed")
	}
	return state, nil
}

func (manager *NativeSystemIntegrationsManager) Finalize(_ context.Context, dryRun bool) (SystemIntegrationsState, error) {
	if manager == nil {
		return SystemIntegrationsState{}, errors.New("system integrations manager is unavailable")
	}
	if dryRun {
		return SystemIntegrationsState{Status: "validated"}, nil
	}
	if err := manager.verifyInstalledAssets(); err != nil {
		return SystemIntegrationsState{}, err
	}
	current, err := integrationFileMatches(manager.Paths.Marker, []byte(systemIntegrationsMarkerContent), 0644)
	if err != nil {
		return SystemIntegrationsState{}, err
	}
	if current {
		return SystemIntegrationsState{Status: "ready"}, nil
	}
	if err := ensureDirectoryTreeNoSymlink(filepath.Dir(manager.Paths.Marker), 0750); err != nil {
		return SystemIntegrationsState{}, errors.New("system integrations marker directory is unsafe")
	}
	if err := refuseNonRegularDestination(manager.Paths.Marker); err != nil {
		return SystemIntegrationsState{}, err
	}
	if err := writeAtomicRegularFile(manager.Paths.Marker, []byte(systemIntegrationsMarkerContent), 0644); err != nil {
		return SystemIntegrationsState{}, errors.New("system integrations marker update failed")
	}
	if err := validateSystemIntegrationFile(manager.Paths.Marker, 0644); err != nil {
		return SystemIntegrationsState{}, errors.New("system integrations marker is unsafe")
	}
	return SystemIntegrationsState{Status: "ready"}, nil
}

func (manager *NativeSystemIntegrationsManager) assetSpec(asset SystemIntegrationAsset) (systemIntegrationAssetSpec, error) {
	if manager == nil {
		return systemIntegrationAssetSpec{}, errors.New("system integrations manager is unavailable")
	}
	digests := map[SystemIntegrationAsset]string{
		SystemIntegrationAssetTerminal:         terminalHelperSHA256,
		SystemIntegrationAssetTerminalPassword: terminalPasswordHelperSHA256,
		SystemIntegrationAssetManagerFirewall:  managerFirewallHelperSHA256,
		SystemIntegrationAssetManagerTLSMDNS:   managerTLSMDNSHelperSHA256,
	}
	for key, value := range manager.Digests {
		digests[key] = value
	}
	paths := map[SystemIntegrationAsset]string{
		SystemIntegrationAssetTerminal:         manager.Paths.TerminalHelper,
		SystemIntegrationAssetTerminalPassword: manager.Paths.TerminalPasswordHelper,
		SystemIntegrationAssetManagerFirewall:  manager.Paths.ManagerFirewallHelper,
		SystemIntegrationAssetManagerTLSMDNS:   manager.Paths.ManagerTLSMDNSHelper,
	}
	path, ok := paths[asset]
	digest, digestOK := digests[asset]
	if !ok || !digestOK || path == "" || len(digest) != 64 {
		return systemIntegrationAssetSpec{}, errors.New("system integration asset is not allowed")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return systemIntegrationAssetSpec{}, errors.New("system integration asset catalog is invalid")
	}
	return systemIntegrationAssetSpec{Path: filepath.Clean(path), SHA256: strings.ToLower(digest)}, nil
}

func (manager *NativeSystemIntegrationsManager) verifyInstalledAssets() error {
	for _, asset := range []SystemIntegrationAsset{
		SystemIntegrationAssetTerminal,
		SystemIntegrationAssetTerminalPassword,
		SystemIntegrationAssetManagerFirewall,
		SystemIntegrationAssetManagerTLSMDNS,
	} {
		spec, err := manager.assetSpec(asset)
		if err != nil {
			return err
		}
		if err := validateSystemIntegrationFile(spec.Path, 0755); err != nil {
			return errors.New("installed system integration asset is unsafe")
		}
		raw, err := os.ReadFile(spec.Path)
		if err != nil || len(raw) == 0 || len(raw) > 16*1024 {
			return errors.New("installed system integration asset is unavailable")
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:]) != spec.SHA256 {
			return errors.New("installed system integration asset digest is invalid")
		}
	}
	return nil
}

func (manager *NativeSystemIntegrationsManager) lndServiceLoaded(ctx context.Context) (bool, error) {
	output, err := manager.Runner.Run(ctx, systemctlPath, "show", "--property=LoadState", "--value", "lnd.service")
	state := strings.TrimSpace(output)
	if state == "not-found" || (err != nil && state == "") {
		return false, nil
	}
	if err != nil || state != "loaded" {
		return false, errors.New("LND service state is invalid")
	}
	return true, nil
}

func (manager *NativeSystemIntegrationsManager) managerGroup(ctx context.Context) string {
	group, err := manager.Runner.Run(ctx, systemctlPath, "show", "--property=Group", "--value", "lightningos-manager.service")
	group = strings.TrimSpace(group)
	if err == nil && systemIntegrationIdentityPattern.MatchString(group) {
		return group
	}
	user, err := manager.Runner.Run(ctx, systemctlPath, "show", "--property=User", "--value", "lightningos-manager.service")
	user = strings.TrimSpace(user)
	if err == nil && systemIntegrationIdentityPattern.MatchString(user) {
		group, err = manager.Runner.Run(ctx, idExecutablePath, "-gn", user)
		group = strings.TrimSpace(group)
		if err == nil && systemIntegrationIdentityPattern.MatchString(group) {
			return group
		}
	}
	return "lightningos"
}

func integrationFileMatches(path string, expected []byte, mode os.FileMode) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("system integration file is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("system integration destination is unsafe")
	}
	if info.Size() > 16*1024 {
		return false, errors.New("system integration file is oversized")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, errors.New("system integration file is unavailable")
	}
	if string(raw) != string(expected) || info.Mode().Perm() != mode {
		return false, nil
	}
	if err := validateSystemIntegrationFile(path, mode); err != nil {
		return false, nil
	}
	return true, nil
}

func refuseNonRegularDestination(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("system integration destination is unsafe")
	}
	return nil
}

func optionalRegularFileSHA256(path string, maximum int64) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximum {
		return "", errors.New("file is unsafe")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
