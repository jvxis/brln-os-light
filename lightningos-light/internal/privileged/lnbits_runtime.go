package privileged

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const maxLNbitsCredentialBytes = 64 * 1024

const lnbitsSettingsMigrationScript = `import json
import os
import sqlite3

path = "/app/data/database.sqlite3"
if not os.path.isfile(path):
    raise SystemExit(0)

db = sqlite3.connect(path, timeout=10)
try:
    exists = db.execute(
        "select 1 from sqlite_master where type='table' and name='system_settings'"
    ).fetchone()
    if not exists:
        raise SystemExit(0)
    desired = {
        "lnbits_backend_wallet_class": "LndRestWallet",
        "lnd_rest_endpoint": "https://host.docker.internal:8080/",
        "lnd_rest_cert": "/etc/lnd/tls.cert",
        "lnd_rest_macaroon": "/etc/lnd/lnbits.macaroon",
    }
    scrubbed = (
        "lnd_admin_macaroon",
        "lnd_invoice_macaroon",
        "lnd_rest_admin_macaroon",
        "lnd_rest_invoice_macaroon",
        "lnd_rest_macaroon_encrypted",
        "lnd_grpc_admin_macaroon",
        "lnd_grpc_invoice_macaroon",
        "lnd_grpc_macaroon",
        "lnd_grpc_macaroon_encrypted",
    )
    with db:
        for key, value in desired.items():
            db.execute(
                "update system_settings set value=? where id=?",
                (json.dumps(value), key),
            )
        for key in scrubbed:
            db.execute(
                "update system_settings set value=? where id=?",
                (json.dumps(""), key),
            )
finally:
    db.close()
`

type lnbitsValidatedFiles struct {
	envRaw         []byte
	certificateRaw []byte
	macaroonRaw    []byte
}

func (manager *ComposeAppManager) validatedLNbitsFiles() (lnbitsValidatedFiles, error) {
	var files lnbitsValidatedFiles
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
	appRoot := filepath.Join(appsRoot, appmanifest.LNbitsID)
	dataRoot := filepath.Join(appsDataRoot, appmanifest.LNbitsID)
	dataDir := filepath.Join(dataRoot, "data")
	lndDir := filepath.Join(dataRoot, appmanifest.LNbitsLNDDir)

	if err := validateSnapshotDirectoryEntries(appRoot, map[string]bool{
		appmanifest.LNbitsComposeFile: true,
		appmanifest.LNbitsEnvFile:     true,
	}); err != nil {
		return files, errors.New("LNbits app declaration contains unexpected assets")
	}
	for _, directory := range []string{dataRoot, dataDir, filepath.Join(dataDir, "extensions"), lndDir} {
		if err := validateRegularDirectory(directory); err != nil {
			return files, errors.New("LNbits data directory is invalid")
		}
	}
	if err := validateSnapshotDirectoryEntries(lndDir, map[string]bool{
		appmanifest.LNbitsTLSCertFile:  true,
		appmanifest.LNbitsMacaroonFile: true,
	}); err != nil {
		return files, errors.New("LNbits LND declaration contains unexpected assets")
	}

	envPath := filepath.Join(appRoot, appmanifest.LNbitsEnvFile)
	if err := validateSecretFileMode(envPath); err != nil {
		return files, errors.New("LNbits environment is not private")
	}
	envRaw, err := readRegularFile(envPath, 64*1024)
	if err != nil || appmanifest.ValidateLNbitsEnv(envRaw) != nil {
		return files, errors.New("LNbits environment does not match the catalog")
	}

	certificatePath := filepath.Join(lndDir, appmanifest.LNbitsTLSCertFile)
	certificateRaw, err := readRegularFile(certificatePath, maxLNbitsCredentialBytes)
	if err != nil || validateTLSCertificate(certificateRaw) != nil {
		return files, errors.New("LNbits LND certificate is invalid")
	}
	macaroonPath := filepath.Join(lndDir, appmanifest.LNbitsMacaroonFile)
	if err := validateSecretFileMode(macaroonPath); err != nil {
		return files, errors.New("LNbits LND credential is not private")
	}
	macaroonRaw, err := readRegularFile(macaroonPath, maxLNbitsCredentialBytes)
	if err != nil || len(macaroonRaw) == 0 {
		return files, errors.New("LNbits LND credential is unavailable")
	}
	adminRaw, err := readRegularFile(filepath.Join(lndDataRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"), maxLNbitsCredentialBytes)
	if err != nil {
		return files, errors.New("native LND admin credential is unavailable")
	}
	if bytes.Equal(macaroonRaw, adminRaw) {
		return files, errors.New("LNbits LND credential must not be the admin macaroon")
	}

	composeRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.LNbitsComposeFile), 64*1024)
	if err != nil {
		return files, errors.New("LNbits compose manifest is unavailable")
	}
	expectedCompose := appmanifest.LNbitsCompose(appmanifest.LNbitsComposePaths{
		DataDir:      dataDir,
		TLSCertPath:  certificatePath,
		MacaroonPath: macaroonPath,
	})
	if !bytes.Equal(composeRaw, []byte(expectedCompose)) {
		return files, errors.New("LNbits compose manifest does not match the catalog")
	}

	return lnbitsValidatedFiles{envRaw: envRaw, certificateRaw: certificateRaw, macaroonRaw: macaroonRaw}, nil
}

func (manager *ComposeAppManager) createLNbitsSnapshot(files lnbitsValidatedFiles) (composeAppSnapshot, func(), error) {
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
	snapshotRoot := filepath.Join(privilegedAppsRoot, appmanifest.LNbitsID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure LNbits execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, map[string]bool{
		appmanifest.LNbitsComposeFile: true,
		appmanifest.LNbitsEnvFile:     true,
		appmanifest.LNbitsLNDDir:      false,
	}); err != nil {
		return snapshot, func() {}, errors.New("LNbits execution snapshot contains unexpected assets")
	}
	lndDir := filepath.Join(snapshotRoot, appmanifest.LNbitsLNDDir)
	if err := ensureDirectoryTreeNoSymlink(lndDir, 0750); err != nil {
		return snapshot, func() {}, errors.New("failed to secure LNbits LND snapshot")
	}
	if err := setPrivilegedPathGroup(lndDir, appmanifest.LNbitsContainerGID); err != nil {
		return snapshot, func() {}, errors.New("failed to assign LNbits snapshot group")
	}
	if err := validateSnapshotDirectoryEntries(lndDir, map[string]bool{
		appmanifest.LNbitsTLSCertFile:  true,
		appmanifest.LNbitsMacaroonFile: true,
	}); err != nil {
		return snapshot, func() {}, errors.New("LNbits LND snapshot contains unexpected assets")
	}

	composePath := filepath.Join(snapshotRoot, appmanifest.LNbitsComposeFile)
	envPath := filepath.Join(snapshotRoot, appmanifest.LNbitsEnvFile)
	certificatePath := filepath.Join(lndDir, appmanifest.LNbitsTLSCertFile)
	macaroonPath := filepath.Join(lndDir, appmanifest.LNbitsMacaroonFile)
	compose := appmanifest.LNbitsCompose(appmanifest.LNbitsComposePaths{
		DataDir:      filepath.Join(appsDataRoot, appmanifest.LNbitsID, "data"),
		TLSCertPath:  certificatePath,
		MacaroonPath: macaroonPath,
	})
	for _, file := range []struct {
		path string
		raw  []byte
		mode os.FileMode
		gid  int
	}{
		{composePath, []byte(compose), 0600, 0},
		{envPath, files.envRaw, 0600, 0},
		{certificatePath, files.certificateRaw, 0640, appmanifest.LNbitsContainerGID},
		{macaroonPath, files.macaroonRaw, 0640, appmanifest.LNbitsContainerGID},
	} {
		if err := writeAtomicRegularFile(file.path, file.raw, file.mode); err != nil {
			return snapshot, func() {}, errors.New("failed to write LNbits execution snapshot")
		}
		if file.gid != 0 {
			if err := setPrivilegedPathGroup(file.path, file.gid); err != nil {
				return snapshot, func() {}, errors.New("failed to assign LNbits snapshot group")
			}
		}
	}
	return composeAppSnapshot{root: snapshotRoot, composePath: composePath, envPath: envPath}, func() {}, nil
}

func (manager *ComposeAppManager) removeLNbitsExecutionSnapshot(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.LNbitsID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid app execution snapshot")
	}
	if err := validateRegularDirectory(expectedRoot); err != nil {
		return errors.New("invalid app execution snapshot")
	}
	return os.RemoveAll(expectedRoot)
}

func (manager *ComposeAppManager) refreshLNbitsSnapshotCertificate(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.LNbitsID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid LNbits execution snapshot")
	}
	lndDataRoot := manager.LNDDataRoot
	if lndDataRoot == "" {
		lndDataRoot = defaultLNDDataRoot
	}
	var certificateRaw []byte
	var err error
	for attempt := 0; attempt < 30; attempt++ {
		certificateRaw, err = readRegularFile(filepath.Join(lndDataRoot, "tls.cert"), maxLNbitsCredentialBytes)
		if err == nil && validateTLSCertificate(certificateRaw) == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || validateTLSCertificate(certificateRaw) != nil {
		return errors.New("refreshed LND certificate is unavailable")
	}
	target := filepath.Join(expectedRoot, appmanifest.LNbitsLNDDir, appmanifest.LNbitsTLSCertFile)
	if err := writeAtomicRegularFile(target, certificateRaw, 0640); err != nil {
		return errors.New("failed to refresh LNbits certificate snapshot")
	}
	if err := setPrivilegedPathGroup(target, appmanifest.LNbitsContainerGID); err != nil {
		return errors.New("failed to assign LNbits certificate group")
	}
	return nil
}

func (manager *ComposeAppManager) ensureLNbitsHostAccess(ctx context.Context) error {
	gatewayRaw, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return errors.New("LNbits network gateway lookup failed")
	}
	gateway := strings.TrimSpace(gatewayRaw)
	if !isPrivateDockerGateway(gateway) {
		return errors.New("LNbits network gateway is invalid")
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
	updated, changed := updateLNbitsRESTOptions(lines, gateway)
	certificateNeedsRefresh := manager.lndCertificateNeedsDockerHostAccess()
	if changed {
		if err := replaceRegularFilePreservingMetadata(configPath, []byte(strings.Join(updated, "\n")+"\n")); err != nil {
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
	return nil
}

func (manager *ComposeAppManager) EnsureLNDHostAccess(ctx context.Context, appID string, dryRun bool) error {
	if manager == nil || manager.Runner == nil {
		return errors.New("compose app manager is unavailable")
	}
	if appID != appmanifest.BTCPayID {
		return errors.New("app LND host access manifest is not allowed")
	}
	if dryRun {
		return nil
	}
	return manager.ensureLNbitsHostAccess(ctx)
}

func (manager *ComposeAppManager) migrateLNbitsLegacySettings(ctx context.Context) error {
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	dataDir := filepath.Join(appsDataRoot, appmanifest.LNbitsID, "data")
	containerUser := strconv.Itoa(appmanifest.LNbitsContainerUID) + ":" + strconv.Itoa(appmanifest.LNbitsContainerGID)
	if _, err := manager.Runner.Run(ctx, dockerPath,
		"run", "--rm", "--network", "none",
		"--user", containerUser, "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777",
		"-e", "HOME=/app/data", "-e", "PYTHONDONTWRITEBYTECODE=1",
		"-v", dataDir+":/app/data:rw",
		appmanifest.LNbitsImage,
		"/app/.venv/bin/python", "-c", lnbitsSettingsMigrationScript,
	); err != nil {
		return errors.New("LNbits legacy settings migration failed")
	}
	return nil
}

func updateLNbitsRESTOptions(lines []string, gateway string) ([]string, bool) {
	seen := make(map[string]bool)
	hasWildcard := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		seen[trimmed] = true
		switch trimmed {
		case "restlisten=0.0.0.0:8080", "restlisten=[::]:8080", "restlisten=:8080", "restlisten=*:8080":
			hasWildcard = true
		}
	}
	desired := []string{"tlsextraip=" + gateway, "tlsextradomain=host.docker.internal"}
	if !hasWildcard {
		desired = append(desired, "restlisten="+gateway+":8080")
	}
	insert := -1
	for index, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line), "[Application Options]") {
			insert = index + 1
			break
		}
	}
	if insert == -1 {
		lines = append([]string{"[Application Options]"}, lines...)
		insert = 1
	}
	block := make([]string, 0, len(desired))
	for _, item := range desired {
		if !seen[item] {
			block = append(block, item)
		}
	}
	if len(block) == 0 {
		return append([]string(nil), lines...), false
	}
	updated := append([]string{}, lines[:insert]...)
	updated = append(updated, block...)
	updated = append(updated, lines[insert:]...)
	return updated, true
}

func (manager *ComposeAppManager) ensureLNbitsInternalFirewall(ctx context.Context) error {
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", appmanifest.LNbitsProject+"_default", "--format", "{{.Id}}")
	if err != nil {
		return errors.New("LNbits network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return errors.New("LNbits network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return errors.New("LNbits network ID is invalid")
		}
	}
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", "in", "on", "br-"+id[:12], "to", "any", "port", "8080", "proto", "tcp"); err != nil {
		return errors.New("LNbits internal firewall rule failed")
	}
	return nil
}
