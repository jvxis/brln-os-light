package privileged

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	maxLNDgEnvBytes        = 8192
	maxLNDgEntrypointBytes = 16 * 1024
	maxLNDgCredentialBytes = 64 * 1024
	defaultLNDConfigPath   = "/data/lnd/lnd.conf"
)

type lndgValidatedFiles struct {
	envRaw         []byte
	entrypointRaw  []byte
	certificateRaw []byte
	macaroonRaw    []byte
	runtime        appmanifest.LNDgRuntime
	channelDBPath  string
	placeholderDB  bool
}

func (manager *ComposeAppManager) validatedLNDgFiles() (lndgValidatedFiles, error) {
	var files lndgValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.LNDgID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.LNDgID)
	dataDir := filepath.Join(dataRoot, "data")
	pgDir := filepath.Join(dataRoot, "pgdata")
	lndDir := filepath.Join(dataRoot, appmanifest.LNDgLNDDir)
	logPath := filepath.Join(dataDir, "lndg-controller.log")
	entrypointPath := filepath.Join(appRoot, appmanifest.LNDgEntrypointFile)

	if err := validateSnapshotDirectoryEntries(appRoot, map[string]bool{
		appmanifest.LNDgComposeFile:    true,
		appmanifest.LNDgEnvFile:        true,
		appmanifest.LNDgEntrypointFile: true,
	}); err != nil {
		return files, errors.New("LNDg app declaration contains unexpected assets")
	}
	for _, directory := range []string{dataRoot, dataDir, pgDir, lndDir} {
		if err := validateRegularDirectory(directory); err != nil {
			return files, errors.New("LNDg data directory is invalid")
		}
	}
	if err := validateSnapshotDirectoryEntries(lndDir, map[string]bool{
		appmanifest.LNDgTLSCertFile:   true,
		appmanifest.LNDgMacaroonFile:  true,
		appmanifest.LNDgChannelDBFile: true,
	}); err != nil {
		return files, errors.New("LNDg LND declaration contains unexpected assets")
	}

	envPath := filepath.Join(appRoot, appmanifest.LNDgEnvFile)
	if err := validateSecretFileMode(envPath); err != nil {
		return files, errors.New("LNDg environment is not private")
	}
	envRaw, err := readRegularFile(envPath, maxLNDgEnvBytes)
	if err != nil {
		return files, errors.New("LNDg environment is unavailable")
	}
	runtime, err := appmanifest.ParseLNDgRuntimeEnv(envRaw)
	if err != nil {
		return files, errors.New("LNDg environment does not match the catalog")
	}
	for _, secret := range []struct {
		path string
		want string
	}{
		{filepath.Join(dataDir, "lndg-admin.txt"), runtime.AdminPassword},
		{filepath.Join(dataDir, "lndg-db-password.txt"), runtime.DBPassword},
	} {
		if err := validateSecretFileMode(secret.path); err != nil {
			return files, errors.New("LNDg persisted credential is not private")
		}
		raw, err := readRegularFile(secret.path, 1024)
		if err != nil || strings.TrimSpace(string(raw)) != secret.want {
			return files, errors.New("LNDg persisted credential does not match the environment")
		}
	}

	entrypointRaw, err := readRegularFile(entrypointPath, maxLNDgEntrypointBytes)
	if err != nil {
		return files, errors.New("LNDg entrypoint is unavailable")
	}
	if !bytes.Equal(entrypointRaw, []byte(appmanifest.LNDgEntrypoint)) {
		return files, errors.New("LNDg entrypoint does not match the catalog")
	}

	composePath := filepath.Join(appRoot, appmanifest.LNDgComposeFile)
	composeRaw, err := readRegularFile(composePath, 64*1024)
	if err != nil {
		return files, errors.New("LNDg compose manifest is unavailable")
	}
	channelDBPath := filepath.Join(lndDataRoot, "data", "graph", "mainnet", appmanifest.LNDgChannelDBFile)
	placeholderDB := false
	if info, statErr := os.Lstat(channelDBPath); statErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		channelDBPath = filepath.Join(lndDir, appmanifest.LNDgChannelDBFile)
		placeholderDB = true
		info, placeholderErr := os.Lstat(channelDBPath)
		if placeholderErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 0 {
			return files, errors.New("LNDg channel database placeholder is invalid")
		}
	}
	expectedCompose := appmanifest.LNDgCompose(appmanifest.LNDgComposePaths{
		DataDir:        dataDir,
		PgDir:          pgDir,
		LogPath:        logPath,
		LndDir:         lndDir,
		ChannelDBPath:  channelDBPath,
		EntrypointPath: entrypointPath,
	})
	if !bytes.Equal(composeRaw, []byte(expectedCompose)) {
		return files, errors.New("LNDg compose manifest does not match the catalog")
	}

	certificatePath := filepath.Join(lndDir, appmanifest.LNDgTLSCertFile)
	certificateRaw, err := readRegularFile(certificatePath, maxLNDgCredentialBytes)
	if err != nil || validateTLSCertificate(certificateRaw) != nil {
		return files, errors.New("LNDg LND certificate is invalid")
	}
	macaroonPath := filepath.Join(lndDir, appmanifest.LNDgMacaroonFile)
	if err := validateSecretFileMode(macaroonPath); err != nil {
		return files, errors.New("LNDg LND credential is not private")
	}
	macaroonRaw, err := readRegularFile(macaroonPath, maxLNDgCredentialBytes)
	if err != nil || len(macaroonRaw) == 0 {
		return files, errors.New("LNDg LND credential is unavailable")
	}
	adminPath := filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon")
	adminRaw, err := readRegularFile(adminPath, maxLNDgCredentialBytes)
	if err != nil {
		return files, errors.New("native LND admin credential is unavailable")
	}
	if bytes.Equal(macaroonRaw, adminRaw) {
		return files, errors.New("LNDg LND credential must not be the admin macaroon")
	}
	if info, err := os.Lstat(logPath); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return files, errors.New("LNDg log file is invalid")
	}

	files = lndgValidatedFiles{
		envRaw:         envRaw,
		entrypointRaw:  entrypointRaw,
		certificateRaw: certificateRaw,
		macaroonRaw:    macaroonRaw,
		runtime:        runtime,
		channelDBPath:  channelDBPath,
		placeholderDB:  placeholderDB,
	}
	return files, nil
}

func (manager *ComposeAppManager) createLNDgSnapshot(files lndgValidatedFiles) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedAppsRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedAppsRoot, appmanifest.LNDgID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure LNDg execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, map[string]bool{
		appmanifest.LNDgComposeFile:    true,
		appmanifest.LNDgEnvFile:        true,
		appmanifest.LNDgEntrypointFile: true,
		appmanifest.LNDgLNDDir:         false,
		lndgImageAttestationFile:       true,
	}); err != nil {
		return snapshot, func() {}, errors.New("LNDg execution snapshot contains unexpected assets")
	}
	lndDir := filepath.Join(snapshotRoot, appmanifest.LNDgLNDDir)
	if err := ensureDirectoryTreeNoSymlink(lndDir, 0750); err != nil {
		return snapshot, func() {}, errors.New("failed to secure LNDg LND snapshot")
	}
	if err := setPrivilegedPathGroup(lndDir, appmanifest.LNDgContainerGID); err != nil {
		return snapshot, func() {}, errors.New("failed to assign LNDg snapshot group")
	}
	if err := validateSnapshotDirectoryEntries(lndDir, map[string]bool{
		appmanifest.LNDgTLSCertFile:   true,
		appmanifest.LNDgMacaroonFile:  true,
		appmanifest.LNDgChannelDBFile: true,
	}); err != nil {
		return snapshot, func() {}, errors.New("LNDg LND snapshot contains unexpected assets")
	}

	dataRoot := filepath.Join(appsDataRoot, appmanifest.LNDgID)
	composePath := filepath.Join(snapshotRoot, appmanifest.LNDgComposeFile)
	envPath := filepath.Join(snapshotRoot, appmanifest.LNDgEnvFile)
	entrypointPath := filepath.Join(snapshotRoot, appmanifest.LNDgEntrypointFile)
	channelDBPath := files.channelDBPath
	if files.placeholderDB {
		channelDBPath = filepath.Join(lndDir, appmanifest.LNDgChannelDBFile)
	}
	compose := appmanifest.LNDgCompose(appmanifest.LNDgComposePaths{
		DataDir:        filepath.Join(dataRoot, "data"),
		PgDir:          filepath.Join(dataRoot, "pgdata"),
		LogPath:        filepath.Join(dataRoot, "data", "lndg-controller.log"),
		LndDir:         lndDir,
		ChannelDBPath:  channelDBPath,
		EntrypointPath: entrypointPath,
	})
	for _, file := range []struct {
		path string
		raw  []byte
		mode os.FileMode
		gid  int
	}{
		{composePath, []byte(compose), 0600, 0},
		{envPath, files.envRaw, 0600, 0},
		{entrypointPath, files.entrypointRaw, 0750, appmanifest.LNDgContainerGID},
		{filepath.Join(lndDir, appmanifest.LNDgTLSCertFile), files.certificateRaw, 0640, appmanifest.LNDgContainerGID},
		{filepath.Join(lndDir, appmanifest.LNDgMacaroonFile), files.macaroonRaw, 0640, appmanifest.LNDgContainerGID},
	} {
		if err := writeAtomicRegularFile(file.path, file.raw, file.mode); err != nil {
			return snapshot, func() {}, errors.New("failed to write LNDg execution snapshot")
		}
		if file.gid != 0 {
			if err := setPrivilegedPathGroup(file.path, file.gid); err != nil {
				return snapshot, func() {}, errors.New("failed to assign LNDg snapshot group")
			}
		}
	}
	if files.placeholderDB {
		placeholderPath := filepath.Join(lndDir, appmanifest.LNDgChannelDBFile)
		if err := writeAtomicRegularFile(placeholderPath, nil, 0640); err != nil {
			return snapshot, func() {}, errors.New("failed to write LNDg channel database placeholder")
		}
		if err := setPrivilegedPathGroup(placeholderPath, appmanifest.LNDgContainerGID); err != nil {
			return snapshot, func() {}, errors.New("failed to assign LNDg placeholder group")
		}
	}
	return composeAppSnapshot{root: snapshotRoot, composePath: composePath, envPath: envPath}, func() {}, nil
}

func (manager *ComposeAppManager) removeLNDgExecutionSnapshot(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.LNDgID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid app execution snapshot")
	}
	if err := validateRegularDirectory(expectedRoot); err != nil {
		return errors.New("invalid app execution snapshot")
	}
	return os.RemoveAll(expectedRoot)
}

func (manager *ComposeAppManager) refreshLNDgSnapshotCertificate(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.LNDgID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid LNDg execution snapshot")
	}
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	var certificateRaw []byte
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		certificateRaw, err = readRegularFile(filepath.Join(lndDataRoot, "tls.cert"), maxLNDgCredentialBytes)
		if err == nil && validateTLSCertificate(certificateRaw) == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || validateTLSCertificate(certificateRaw) != nil {
		return errors.New("refreshed LND certificate is unavailable")
	}
	target := filepath.Join(expectedRoot, appmanifest.LNDgLNDDir, appmanifest.LNDgTLSCertFile)
	if err := writeAtomicRegularFile(target, certificateRaw, 0640); err != nil {
		return errors.New("failed to refresh LNDg certificate snapshot")
	}
	if err := setPrivilegedPathGroup(target, appmanifest.LNDgContainerGID); err != nil {
		return errors.New("failed to assign LNDg certificate group")
	}
	return nil
}

func (manager *ComposeAppManager) ensureLNDgHostAccess(ctx context.Context) error {
	// Docker's host-gateway token resolves to the daemon's default bridge
	// gateway, not to the gateway of the Compose network carrying the packet.
	// The catalog still uses the LNDg bridge name for the UFW ingress rule.
	gatewayRaw, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return errors.New("LNDg network gateway lookup failed")
	}
	gateway := strings.TrimSpace(gatewayRaw)
	if !isPrivateDockerGateway(gateway) {
		return errors.New("LNDg network gateway is invalid")
	}
	configPath := manager.LNDConfigPath
	if configPath == "" {
		configPath = defaultLNDConfigPath
	}
	configRaw, err := readRegularFile(configPath, 1024*1024)
	if err != nil {
		return errors.New("LND configuration is unavailable")
	}
	lines := strings.Split(strings.TrimRight(string(configRaw), "\n"), "\n")
	updated, changed := updateLNDgRPCOptions(lines, gateway)
	certificateNeedsRefresh := manager.lndCertificateNeedsDockerHostAccess()
	if changed {
		content := []byte(strings.Join(updated, "\n") + "\n")
		if err := replaceRegularFilePreservingMetadata(configPath, content); err != nil {
			return errors.New("LND configuration update failed")
		}
	}
	if changed || certificateNeedsRefresh {
		if err := manager.removeLNDServerCertificate(); err != nil {
			return err
		}
		if _, err := manager.Runner.Run(ctx, systemctlPath, "restart", "lnd"); err != nil {
			return errors.New("LND restart failed")
		}
	}
	return manager.ensureLNDgInternalFirewall(ctx)
}

func updateLNDgRPCOptions(lines []string, gateway string) ([]string, bool) {
	desired := []string{
		"tlsextraip=" + gateway,
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"rpclisten=" + gateway + ":10009",
	}
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(lines)+len(desired))
	insert := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[Application Options]" && insert == -1 {
			cleaned = append(cleaned, line)
			insert = len(cleaned)
			continue
		}
		for _, wanted := range desired {
			if trimmed == wanted {
				seen[wanted] = true
				break
			}
		}
		cleaned = append(cleaned, line)
	}
	if insert == -1 {
		insert = 0
	}
	block := make([]string, 0, len(desired))
	for _, wanted := range desired {
		if !seen[wanted] {
			block = append(block, wanted)
		}
	}
	updated := append([]string{}, cleaned[:insert]...)
	updated = append(updated, block...)
	updated = append(updated, cleaned[insert:]...)
	if len(updated) != len(lines) {
		return updated, true
	}
	for index := range updated {
		if updated[index] != lines[index] {
			return updated, true
		}
	}
	return updated, false
}

func isPrivateDockerGateway(value string) bool {
	ip := net.ParseIP(value)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		return false
	}
	return ip.To4()[3] == 1
}

func (manager *ComposeAppManager) lndCertificateNeedsDockerHostAccess() bool {
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	certificateRaw, err := readRegularFile(filepath.Join(lndDataRoot, "tls.cert"), maxLNDgCredentialBytes)
	if err != nil {
		return true
	}
	certificate, err := parseTLSCertificate(certificateRaw)
	if err != nil {
		return true
	}
	return certificate.VerifyHostname("host.docker.internal") != nil
}

func (manager *ComposeAppManager) removeLNDServerCertificate() error {
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	for _, name := range []string{"tls.cert", "tls.key"} {
		path := filepath.Join(lndDataRoot, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LND TLS material is unsafe")
		}
		if err := os.Remove(path); err != nil {
			return errors.New("LND TLS material removal failed")
		}
	}
	return nil
}

func (manager *ComposeAppManager) ensureLNDgInternalFirewall(ctx context.Context) error {
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", appmanifest.LNDgProject+"_default", "--format", "{{.Id}}")
	if err != nil {
		return errors.New("LNDg network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return errors.New("LNDg network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return errors.New("LNDg network ID is invalid")
		}
	}
	bridge := "br-" + id[:12]
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", "in", "on", bridge, "to", "any", "port", "10009", "proto", "tcp"); err != nil {
		return errors.New("LNDg internal firewall rule failed")
	}
	return nil
}

func (manager *ComposeAppManager) waitAndSyncLNDgDatabase(ctx context.Context, commandPath string, composeArgs []string) error {
	base := append([]string(nil), composeArgs...)
	for attempt := 0; attempt < 30; attempt++ {
		containerRaw, err := manager.Runner.Run(ctx, commandPath, append(append([]string(nil), base...), "ps", "-q", appmanifest.LNDgDatabaseService)...)
		if err == nil {
			containerID := parseDockerContainerID(containerRaw)
			if containerID != "" {
				script := `export PGPASSWORD="$POSTGRES_PASSWORD"
pg_isready -h 127.0.0.1 -U "${POSTGRES_USER:-lndg}" -d postgres >/dev/null 2>&1 || exit 1
psql -h 127.0.0.1 -U "${POSTGRES_USER:-lndg}" -d postgres -v ON_ERROR_STOP=1 -v password="$POSTGRES_PASSWORD" >/dev/null <<'SQL'
ALTER USER lndg WITH PASSWORD :'password';
SQL`
				if _, execErr := manager.Runner.Run(ctx, dockerPath, "exec", "-i", containerID, "sh", "-c", script); execErr == nil {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return errors.New("LNDg database readiness timed out")
		case <-time.After(time.Second):
		}
	}
	return errors.New("LNDg database did not become ready")
}

func (manager *ComposeAppManager) resetLNDgAdmin(ctx context.Context, snapshot composeAppSnapshot) error {
	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--env-file", snapshot.envPath, "--project-name", appmanifest.LNDgProject,
		"--project-directory", snapshot.root, "-f", snapshot.composePath, "ps", "-q", appmanifest.LNDgPrimaryService)
	containerRaw, err := manager.Runner.Run(ctx, commandPath, args...)
	if err != nil {
		return errors.New("LNDg container lookup failed")
	}
	containerID := parseDockerContainerID(containerRaw)
	if containerID == "" {
		return errors.New("LNDg container is not running")
	}
	script := `python - <<'PY'
import os
import sys
sys.path.insert(0, "/app")
os.environ.setdefault("DJANGO_SETTINGS_MODULE", "lndg.settings")
import django
django.setup()
from django.contrib.auth import get_user_model
username = os.environ.get("LNDG_ADMIN_USER", "lndg-admin")
password = os.environ.get("LNDG_ADMIN_PASSWORD", "")
if not password:
    raise SystemExit("LNDG_ADMIN_PASSWORD is required")
User = get_user_model()
user, _ = User.objects.get_or_create(username=username, defaults={"email": "admin@lndg.local"})
user.set_password(password)
user.is_staff = True
user.is_superuser = True
user.save()
print("ok")
PY`
	if _, err := manager.Runner.Run(ctx, dockerPath, "exec", "-i", containerID, "sh", "-c", script); err != nil {
		return errors.New("LNDg admin reset failed")
	}
	return nil
}

func (manager *ComposeAppManager) ResetAdmin(ctx context.Context, appID string, dryRun bool) error {
	if manager == nil || manager.Runner == nil {
		return errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.LNDgID {
		return errors.New("app admin reset manifest is not allowed")
	}
	files, err := manager.validatedLNDgFiles()
	if err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	snapshot, cleanup, err := manager.createLNDgSnapshot(files)
	if err != nil {
		return err
	}
	defer cleanup()
	return manager.resetLNDgAdmin(ctx, snapshot)
}

func parseTLSCertificate(raw []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certificate PEM is invalid")
	}
	return x509.ParseCertificate(block.Bytes)
}
