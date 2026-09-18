package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

type BRLNCommunityPaths struct {
	SnapshotRoot         string
	SignerDir            string
	AuthDir              string
	KeyPasswordPath      string
	AccessPasswordPath   string
	ComposePath          string
	CaddyfilePath        string
	TLSDir               string
	TLSCertificate       string
	TLSPrivateKey        string
	ManagerCACertificate string
}

// NativeBRLNCommunityManager runs the BR⚡LN Community test app. The broker only
// prepares directories, permissions and local secrets; the member's Nostr key is
// created or imported inside the signer container and stays NIP-49 encrypted in
// the signer data directory, which uninstall preserves.
type NativeBRLNCommunityManager struct {
	Runner       CommandRunner
	Paths        BRLNCommunityPaths
	requireFixed bool
}

func NewNativeBRLNCommunityManager(runner CommandRunner) *NativeBRLNCommunityManager {
	snapshotRoot := filepath.Join(defaultPrivilegedAppsRoot, appmanifest.BRLNCommunityID)
	dataRoot := filepath.Join(defaultAppsDataRoot, appmanifest.BRLNCommunityID)
	tlsDir := filepath.Join(snapshotRoot, appmanifest.BRLNCommunityTLSDir)
	return &NativeBRLNCommunityManager{Runner: runner, requireFixed: true, Paths: BRLNCommunityPaths{
		SnapshotRoot:         snapshotRoot,
		SignerDir:            filepath.Join(dataRoot, "signer"),
		AuthDir:              filepath.Join(dataRoot, "auth"),
		KeyPasswordPath:      filepath.Join(dataRoot, "auth", "key_password"),
		AccessPasswordPath:   filepath.Join(dataRoot, "auth", "access_password"),
		ComposePath:          filepath.Join(snapshotRoot, appmanifest.BRLNCommunityComposeFile),
		CaddyfilePath:        filepath.Join(snapshotRoot, appmanifest.BRLNCommunityCaddyfile),
		TLSDir:               tlsDir,
		TLSCertificate:       filepath.Join(tlsDir, "server.crt"),
		TLSPrivateKey:        filepath.Join(tlsDir, "server.key"),
		ManagerCACertificate: selectBarkManagerCACertificatePath(defaultManagerCACertificatePath, legacyManagerCACertificatePath),
	}}
}

func (manager *NativeBRLNCommunityManager) composePaths() appmanifest.BRLNCommunityComposePaths {
	return appmanifest.BRLNCommunityComposePaths{
		SignerDir: manager.Paths.SignerDir, AuthDir: manager.Paths.AuthDir, CaddyfilePath: manager.Paths.CaddyfilePath,
		TLSCertificate: manager.Paths.TLSCertificate, TLSPrivateKey: manager.Paths.TLSPrivateKey,
		ManagerCACertificate: manager.Paths.ManagerCACertificate,
	}
}

func (manager *NativeBRLNCommunityManager) Status(ctx context.Context) (BRLNCommunityState, error) {
	if manager == nil || manager.Runner == nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community command runner is unavailable")
	}
	state := BRLNCommunityState{Installed: manager.snapshotReady(), Status: "stopped"}
	if !state.Installed {
		return state, nil
	}
	if _, err := readBarkWalletSecret(manager.Paths.AccessPasswordPath); err == nil {
		state.PasswordAvailable = true
	}
	output, err := manager.Runner.Run(ctx, dockerPath, "ps",
		"--filter", "label=com.docker.compose.project="+appmanifest.BRLNCommunityProject,
		"--filter", "label=com.docker.compose.service="+appmanifest.BRLNCommunityPrimaryService,
		"--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		state.Status = "unknown"
		return state, errors.New("BR⚡LN Community container status failed")
	}
	if strings.TrimSpace(output) != "" {
		if parseDockerContainerID(output) == "" {
			state.Status = "unknown"
			return state, errors.New("BR⚡LN Community container identity is ambiguous")
		}
		state.Status = "running"
	}
	return state, nil
}

func (manager *NativeBRLNCommunityManager) Ensure(_ context.Context, dryRun bool) (BRLNCommunityState, error) {
	if err := validateManagerCACertificate(manager.Paths.ManagerCACertificate, manager.requireFixed); err != nil {
		return BRLNCommunityState{}, err
	}
	composeRaw, err := appmanifest.BRLNCommunityCompose(manager.composePaths())
	if err != nil {
		return BRLNCommunityState{}, err
	}
	if dryRun {
		return BRLNCommunityState{Status: "validated"}, nil
	}
	for _, directory := range []struct {
		path string
		mode os.FileMode
	}{
		{manager.Paths.SignerDir, 0700}, {manager.Paths.AuthDir, 0750},
		{manager.Paths.SnapshotRoot, 0700}, {manager.Paths.TLSDir, 0700},
	} {
		if err := ensureDirectoryTreeNoSymlink(directory.path, directory.mode); err != nil {
			return BRLNCommunityState{}, errors.New("BR⚡LN Community directory preparation failed")
		}
	}
	if err := manager.ensureSecrets(); err != nil {
		return BRLNCommunityState{}, err
	}
	if err := manager.ensureTLS(); err != nil {
		return BRLNCommunityState{}, err
	}
	if err := prepareBRLNCommunityWritableData(manager.Paths.SignerDir, manager.Paths.AuthDir,
		manager.Paths.KeyPasswordPath, manager.Paths.AccessPasswordPath); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community data permission preparation failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.CaddyfilePath, []byte(appmanifest.BRLNCommunityCaddyConfig()), 0640); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community proxy configuration write failed")
	}
	if err := setPrivilegedPathGroup(manager.Paths.CaddyfilePath, appmanifest.BRLNCommunityProxyGID); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community proxy configuration ownership failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.ComposePath, []byte(composeRaw), 0600); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community compose write failed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return BRLNCommunityState{}, err
	}
	return BRLNCommunityState{Installed: true, Status: "stopped", PasswordAvailable: true}, nil
}

func (manager *NativeBRLNCommunityManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (BRLNCommunityState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community lifecycle action is not allowed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return BRLNCommunityState{}, err
	}
	if dryRun {
		return BRLNCommunityState{Installed: true, Status: "validated", PasswordAvailable: true}, nil
	}
	if action == AppLifecycleStart {
		for _, image := range appmanifest.BRLNCommunityImages() {
			if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
				return BRLNCommunityState{}, errors.New("verified BR⚡LN Community image is not ready")
			}
		}
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return BRLNCommunityState{}, err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.BRLNCommunityProject,
		"--project-directory", manager.Paths.SnapshotRoot, "-f", manager.Paths.ComposePath)
	if action == AppLifecycleStart {
		args = append(args, "up", "-d")
	} else {
		args = append(args, "stop", "--timeout", strconv.Itoa(appmanifest.BRLNCommunityStopTimeout))
	}
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community lifecycle command failed")
	}
	return manager.Status(ctx)
}

// Remove deletes the containers and the execution snapshot. The signer data and
// local secrets under apps-data are preserved, so a reinstall keeps the member's
// identity and paired devices.
func (manager *NativeBRLNCommunityManager) Remove(ctx context.Context, dryRun bool) error {
	if !manager.snapshotReady() {
		return nil
	}
	if err := manager.validateSnapshot(); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.BRLNCommunityProject,
		"--project-directory", manager.Paths.SnapshotRoot, "-f", manager.Paths.ComposePath,
		"down", "--remove-orphans", "--timeout", strconv.Itoa(appmanifest.BRLNCommunityStopTimeout))
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return errors.New("BR⚡LN Community remove command failed")
	}
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}

func (manager *NativeBRLNCommunityManager) EnsureFirewall(ctx context.Context, dryRun bool) (BRLNCommunityState, error) {
	if dryRun {
		return BRLNCommunityState{Status: "validated"}, nil
	}
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return BRLNCommunityState{Status: "inactive"}, nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", appmanifest.BRLNCommunityProject+"_default", "--format", "{{.Id}}")
	if err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return BRLNCommunityState{}, errors.New("BR⚡LN Community network ID is invalid")
		}
	}
	bridge := "br-" + id[:12]
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", "in", "on", bridge, "to", "any", "port", "8443", "proto", "tcp"); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community manager authorization firewall preparation failed")
	}
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", strconv.Itoa(appmanifest.BRLNCommunityPort)+"/tcp"); err != nil {
		return BRLNCommunityState{}, errors.New("BR⚡LN Community firewall preparation failed")
	}
	return BRLNCommunityState{Status: "active", UFWActive: true}, nil
}

// ReadPassword returns the signer page access password. It never exposes the key
// password, which only the signer container reads.
func (manager *NativeBRLNCommunityManager) ReadPassword() (string, error) {
	if !manager.snapshotReady() {
		return "", errors.New("BR⚡LN Community is not installed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return "", err
	}
	return readBarkWalletSecret(manager.Paths.AccessPasswordPath)
}

func (manager *NativeBRLNCommunityManager) ensureSecrets() error {
	for _, secret := range []struct {
		path  string
		bytes int
		name  string
	}{
		{manager.Paths.KeyPasswordPath, 32, "key password"},
		{manager.Paths.AccessPasswordPath, 24, "access password"},
	} {
		if _, err := readBarkWalletSecret(secret.path); err == nil {
			continue
		} else if !os.IsNotExist(rootCause(err)) {
			return errors.New("BR⚡LN Community " + secret.name + " is invalid")
		}
		value, err := newBarkWalletToken(secret.bytes)
		if err != nil {
			return errors.New("BR⚡LN Community " + secret.name + " generation failed")
		}
		if err := writeAtomicRegularFile(secret.path, []byte(value+"\n"), 0640); err != nil {
			return errors.New("BR⚡LN Community " + secret.name + " write failed")
		}
		if err := setPrivilegedPathGroup(secret.path, appmanifest.BRLNCommunitySignerGID); err != nil {
			return errors.New("BR⚡LN Community " + secret.name + " ownership failed")
		}
	}
	return nil
}

func (manager *NativeBRLNCommunityManager) ensureTLS() error {
	certificateExists, err := barkWalletPathEntryExists(manager.Paths.TLSCertificate)
	if err != nil {
		return errors.New("BR⚡LN Community TLS certificate state is invalid")
	}
	privateKeyExists, err := barkWalletPathEntryExists(manager.Paths.TLSPrivateKey)
	if err != nil {
		return errors.New("BR⚡LN Community TLS private key state is invalid")
	}
	if certificateExists || privateKeyExists {
		certificateRaw, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
		privateKeyRaw, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
		if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificateRaw, privateKeyRaw) != nil {
			return errors.New("BR⚡LN Community TLS key pair is invalid")
		}
		return manager.secureTLSFiles()
	}
	certificateRaw, privateKeyRaw, err := generateLocalAppTLS("lightningos-brln-community")
	if err != nil {
		return errors.New("BR⚡LN Community TLS generation failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.TLSCertificate, certificateRaw, 0640); err != nil {
		return errors.New("BR⚡LN Community TLS certificate write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.TLSPrivateKey, privateKeyRaw, 0640); err != nil {
		return errors.New("BR⚡LN Community TLS private key write failed")
	}
	return manager.secureTLSFiles()
}

func (manager *NativeBRLNCommunityManager) secureTLSFiles() error {
	for _, path := range []string{manager.Paths.TLSCertificate, manager.Paths.TLSPrivateKey} {
		if err := os.Chmod(path, 0640); err != nil {
			return errors.New("BR⚡LN Community TLS permission preparation failed")
		}
		if err := setPrivilegedPathGroup(path, appmanifest.BRLNCommunityProxyGID); err != nil {
			return errors.New("BR⚡LN Community TLS ownership failed")
		}
	}
	return nil
}

func (manager *NativeBRLNCommunityManager) validateSnapshot() error {
	if err := validateManagerCACertificate(manager.Paths.ManagerCACertificate, manager.requireFixed); err != nil {
		return err
	}
	if err := validateBRLNCommunitySnapshotPermissions(manager.Paths); err != nil {
		return err
	}
	if err := validateExecutionSnapshotDirectoryEntries(manager.Paths.SnapshotRoot, map[string]bool{
		appmanifest.BRLNCommunityComposeFile: true, appmanifest.BRLNCommunityCaddyfile: true,
		appmanifest.BRLNCommunityTLSDir: false,
	}); err != nil {
		return errors.New("BR⚡LN Community snapshot contains an unexpected entry")
	}
	if err := validateExecutionSnapshotDirectoryEntries(manager.Paths.TLSDir, map[string]bool{"server.crt": true, "server.key": true}); err != nil {
		return errors.New("BR⚡LN Community TLS snapshot contains an unexpected entry")
	}
	composeRaw, err := readRegularFile(manager.Paths.ComposePath, 64*1024)
	expectedCompose, expectedErr := appmanifest.BRLNCommunityCompose(manager.composePaths())
	if err != nil || expectedErr != nil || string(composeRaw) != expectedCompose {
		return errors.New("BR⚡LN Community compose does not match the catalog")
	}
	caddyRaw, err := readRegularFile(manager.Paths.CaddyfilePath, 16*1024)
	if err != nil || string(caddyRaw) != appmanifest.BRLNCommunityCaddyConfig() {
		return errors.New("BR⚡LN Community proxy configuration does not match the catalog")
	}
	certificateRaw, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
	privateKeyRaw, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
	if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificateRaw, privateKeyRaw) != nil {
		return errors.New("BR⚡LN Community TLS key pair is invalid")
	}
	if _, err := readBarkWalletSecret(manager.Paths.KeyPasswordPath); err != nil {
		return errors.New("BR⚡LN Community key password is invalid")
	}
	if _, err := readBarkWalletSecret(manager.Paths.AccessPasswordPath); err != nil {
		return errors.New("BR⚡LN Community access password is invalid")
	}
	return nil
}

func (manager *NativeBRLNCommunityManager) snapshotReady() bool {
	return safeNonEmptyRegularFile(manager.Paths.ComposePath) && safeNonEmptyRegularFile(manager.Paths.CaddyfilePath) &&
		safeNonEmptyRegularFile(manager.Paths.TLSCertificate) && safeNonEmptyRegularFile(manager.Paths.TLSPrivateKey)
}
