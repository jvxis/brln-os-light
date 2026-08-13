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

const fedimintSnapshotEnvFile = ".env"

type fedimintValidatedFiles struct {
	composeRaw  []byte
	runtimeRaw  []byte
	certificate []byte
	macaroon    []byte
	guardian    *appmanifest.FedimintGuardianRuntime
	gateway     *appmanifest.FedimintGatewayRuntime
}

func (manager *ComposeAppManager) validatedFedimintFiles(appID string) (fedimintValidatedFiles, error) {
	var files fedimintValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appID)
	allowed := map[string]bool{}
	var composeName, runtimeName string
	switch appID {
	case appmanifest.FedimintGuardianID:
		composeName, runtimeName = appmanifest.FedimintGuardianComposeFile, appmanifest.FedimintGuardianRuntimeFile
		allowed[composeName], allowed[runtimeName] = true, true
	case appmanifest.FedimintGatewayID:
		composeName, runtimeName = appmanifest.FedimintGatewayComposeFile, appmanifest.FedimintGatewayRuntimeFile
		allowed[composeName], allowed[runtimeName] = true, true
		allowed[appmanifest.FedimintGatewayTLSFile], allowed[appmanifest.FedimintGatewayMacaroonFile] = true, true
	default:
		return files, errors.New("Fedimint manifest is not allowed")
	}
	if err := validateSnapshotDirectoryEntries(appRoot, allowed); err != nil {
		return files, errors.New("Fedimint declaration contains unexpected assets")
	}
	composePath := filepath.Join(appRoot, composeName)
	runtimePath := filepath.Join(appRoot, runtimeName)
	for _, path := range []string{composePath, runtimePath} {
		if err := validateSecretFileMode(path); err != nil {
			return files, errors.New("Fedimint declaration is not private")
		}
	}
	runtimeRaw, err := readRegularFile(runtimePath, 4096)
	if err != nil {
		return files, errors.New("Fedimint runtime is unavailable")
	}
	composeRaw, err := readRegularFile(composePath, 64*1024)
	if err != nil {
		return files, errors.New("Fedimint compose manifest is unavailable")
	}
	files.composeRaw, files.runtimeRaw = composeRaw, runtimeRaw
	switch appID {
	case appmanifest.FedimintGuardianID:
		runtime, err := appmanifest.ParseFedimintGuardianRuntimeJSON(runtimeRaw)
		if err != nil {
			return fedimintValidatedFiles{}, errors.New("Fedimint Guardian runtime does not match the catalog")
		}
		expected, err := appmanifest.FedimintGuardianCompose(runtime)
		if err != nil || !bytes.Equal(composeRaw, []byte(expected)) {
			return fedimintValidatedFiles{}, errors.New("Fedimint Guardian compose manifest does not match the catalog")
		}
		files.guardian = &runtime
	case appmanifest.FedimintGatewayID:
		runtime, err := appmanifest.ParseFedimintGatewayRuntimeJSON(runtimeRaw)
		if err != nil {
			return fedimintValidatedFiles{}, errors.New("Fedimint Gateway runtime does not match the catalog")
		}
		expected, err := appmanifest.FedimintGatewayCompose(runtime)
		if err != nil || !bytes.Equal(composeRaw, []byte(expected)) {
			return fedimintValidatedFiles{}, errors.New("Fedimint Gateway compose manifest does not match the catalog")
		}
		certificatePath := filepath.Join(appRoot, appmanifest.FedimintGatewayTLSFile)
		macaroonPath := filepath.Join(appRoot, appmanifest.FedimintGatewayMacaroonFile)
		for _, path := range []string{certificatePath, macaroonPath} {
			if err := validateSecretFileMode(path); err != nil {
				return fedimintValidatedFiles{}, errors.New("Fedimint Gateway LND material is not private")
			}
		}
		certificate, err := readRegularFile(certificatePath, 64*1024)
		if err != nil || validateTLSCertificate(certificate) != nil {
			return fedimintValidatedFiles{}, errors.New("Fedimint Gateway LND certificate is invalid")
		}
		macaroon, err := readRegularFile(macaroonPath, 64*1024)
		if err != nil || len(macaroon) == 0 {
			return fedimintValidatedFiles{}, errors.New("Fedimint Gateway LND credential is unavailable")
		}
		lndRoot := manager.LNDDataRoot
		if lndRoot == "" {
			lndRoot = defaultLNDDataRoot
		}
		admin, err := readRegularFile(filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet", "admin.macaroon"), 64*1024)
		if err != nil || bytes.Equal(admin, macaroon) {
			return fedimintValidatedFiles{}, errors.New("Fedimint Gateway must not use the LND admin macaroon")
		}
		files.gateway, files.certificate, files.macaroon = &runtime, certificate, macaroon
	}
	return files, nil
}

func (manager *ComposeAppManager) createFedimintSnapshot(appID string, files fedimintValidatedFiles) (composeAppSnapshot, func(), error) {
	privilegedRoot := manager.PrivilegedAppsRoot
	if privilegedRoot == "" {
		privilegedRoot = defaultPrivilegedAppsRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedRoot, 0700); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedRoot, appID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to secure Fedimint execution snapshot")
	}
	composeName := appmanifest.FedimintGuardianComposeFile
	allowed := map[string]bool{composeName: true, fedimintSnapshotEnvFile: true}
	if appID == appmanifest.FedimintGatewayID {
		composeName = appmanifest.FedimintGatewayComposeFile
		allowed = map[string]bool{composeName: true, fedimintSnapshotEnvFile: true, "lnd": false}
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, allowed); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("Fedimint execution snapshot contains unexpected assets")
	}
	snapshot := composeAppSnapshot{root: snapshotRoot, composePath: filepath.Join(snapshotRoot, composeName)}
	if err := writeAtomicRegularFile(snapshot.composePath, files.composeRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Fedimint compose manifest")
	}
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	service := "fedimintd"
	if appID == appmanifest.FedimintGatewayID {
		service = "gatewayd"
	}
	snapshot.envPath = filepath.Join(snapshotRoot, fedimintSnapshotEnvFile)
	env := appmanifest.FedimintDataDirEnv + "=" + filepath.Join(appsDataRoot, appID, service) + "\n"
	if appID == appmanifest.FedimintGatewayID {
		credentialRoot := filepath.Join(snapshotRoot, "lnd")
		if err := ensureDirectoryTreeNoSymlink(credentialRoot, 0750); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New("failed to secure Fedimint Gateway credential root")
		}
		certificatePath := filepath.Join(credentialRoot, appmanifest.FedimintGatewayTLSFile)
		macaroonPath := filepath.Join(credentialRoot, appmanifest.FedimintGatewayMacaroonFile)
		if err := writeAtomicRegularFile(certificatePath, files.certificate, 0440); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Fedimint Gateway certificate")
		}
		if err := writeAtomicRegularFile(macaroonPath, files.macaroon, 0440); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Fedimint Gateway credential")
		}
		if err := setFedimintCredentialOwnership(credentialRoot, certificatePath, macaroonPath); err != nil {
			return composeAppSnapshot{}, func() {}, errors.New("failed to secure Fedimint Gateway credential ownership")
		}
		env += appmanifest.FedimintGatewayCredentialRoot + "=" + credentialRoot + "\n"
	}
	if err := writeAtomicRegularFile(snapshot.envPath, []byte(env), 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Fedimint environment")
	}
	return snapshot, func() {}, nil
}

func (manager *ComposeAppManager) removeFedimintExecutionSnapshot(appID string, root string) error {
	privilegedRoot := manager.PrivilegedAppsRoot
	if privilegedRoot == "" {
		privilegedRoot = defaultPrivilegedAppsRoot
	}
	expected := filepath.Join(filepath.Clean(privilegedRoot), appID)
	if filepath.Clean(root) != expected || validateRegularDirectory(expected) != nil {
		return errors.New("invalid Fedimint execution snapshot")
	}
	return os.RemoveAll(expected)
}

func (manager *ComposeAppManager) prepareFedimintData(appID string) error {
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	service := "fedimintd"
	if appID == appmanifest.FedimintGatewayID {
		service = "gatewayd"
	}
	dataDir := filepath.Join(appsDataRoot, appID, service)
	if err := ensureDirectoryTreeNoSymlink(dataDir, 0700); err != nil {
		return errors.New("failed to create Fedimint writable data")
	}
	if err := prepareFedimintWritableData(dataDir); err != nil {
		return errors.New("Fedimint writable data preparation failed")
	}
	return nil
}

func (manager *ComposeAppManager) migrateLegacyFedimint(ctx context.Context, appID string) error {
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	service := "fedimintd"
	if appID == appmanifest.FedimintGatewayID {
		service = "gatewayd"
	}
	legacyRoot := filepath.Join(appsDataRoot, "fedimint")
	source := filepath.Join(legacyRoot, service)
	targetRoot := filepath.Join(appsDataRoot, appID)
	target := filepath.Join(targetRoot, service)
	if validateRegularDirectory(source) != nil || validateRegularDirectory(target) == nil {
		return nil
	}
	for _, name := range []string{"fedimint-fedimintd-1", "fedimint_fedimintd_1", "fedimint-gatewayd-1", "fedimint_gatewayd_1"} {
		output, inspectErr := manager.Runner.Run(ctx, dockerPath, "container", "inspect", "--format={{.Id}}", name)
		if inspectErr != nil {
			continue
		}
		containerID := parseDockerContainerID(output)
		if containerID == "" {
			return errors.New("legacy Fedimint container identity is invalid")
		}
		if _, err := manager.Runner.Run(ctx, dockerPath, "stop", "--time", strconv.Itoa(appmanifest.FedimintStopTimeout), containerID); err != nil {
			return errors.New("legacy Fedimint container stop failed")
		}
		if _, err := manager.Runner.Run(ctx, dockerPath, "rm", containerID); err != nil {
			return errors.New("legacy Fedimint container removal failed")
		}
	}
	if err := ensureDirectoryTreeNoSymlink(targetRoot, 0750); err != nil {
		return errors.New("legacy Fedimint target is unsafe")
	}
	if err := os.Rename(source, target); err != nil {
		return errors.New("legacy Fedimint data migration failed")
	}
	return nil
}

func (manager *ComposeAppManager) stopFedimintForOwnershipMigration(ctx context.Context, appID string) error {
	service := appmanifest.FedimintGuardianPrimaryService
	if appID == appmanifest.FedimintGatewayID {
		service = appmanifest.FedimintGatewayPrimaryService
	}
	for _, name := range []string{appID + "-" + service + "-1", appID + "_" + service + "_1"} {
		output, err := manager.Runner.Run(ctx, dockerPath, "container", "inspect", "--format={{.Id}}", name)
		if err != nil {
			continue
		}
		containerID := parseDockerContainerID(output)
		if containerID == "" {
			return errors.New("Fedimint container identity is invalid")
		}
		if _, err := manager.Runner.Run(ctx, dockerPath, "stop", "--time", strconv.Itoa(appmanifest.FedimintStopTimeout), containerID); err != nil {
			return errors.New("Fedimint ownership migration stop failed")
		}
	}
	return nil
}

func (manager *ComposeAppManager) prepareFedimintDataForStart(ctx context.Context, appID string) error {
	appsDataRoot := manager.AppsDataRoot
	if appsDataRoot == "" {
		appsDataRoot = defaultAppsDataRoot
	}
	service := "fedimintd"
	if appID == appmanifest.FedimintGatewayID {
		service = "gatewayd"
	}
	dataDir := filepath.Join(appsDataRoot, appID, service)
	if validateRegularDirectory(dataDir) == nil && !fedimintWritableDataReady(dataDir) {
		if err := manager.stopFedimintForOwnershipMigration(ctx, appID); err != nil {
			return err
		}
	}
	return manager.prepareFedimintData(appID)
}

func (manager *ComposeAppManager) ensureFedimintGatewayHostAccess(ctx context.Context) error {
	gatewayRaw, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}")
	if err != nil {
		return errors.New("Fedimint Gateway LND gateway lookup failed")
	}
	gateway := strings.TrimSpace(gatewayRaw)
	if !isPrivateDockerGateway(gateway) {
		return errors.New("Fedimint Gateway LND gateway is invalid")
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

func (manager *ComposeAppManager) refreshFedimintGatewaySnapshotCertificate(root string) error {
	lndRoot := manager.LNDDataRoot
	if lndRoot == "" {
		lndRoot = defaultLNDDataRoot
	}
	var certificate []byte
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		certificate, err = readRegularFile(filepath.Join(lndRoot, "tls.cert"), 64*1024)
		if err == nil && validateTLSCertificate(certificate) == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil || validateTLSCertificate(certificate) != nil {
		return errors.New("LND certificate refresh failed")
	}
	target := filepath.Join(root, "lnd", appmanifest.FedimintGatewayTLSFile)
	if err := writeAtomicRegularFile(target, certificate, 0440); err != nil {
		return errors.New("Fedimint Gateway certificate refresh failed")
	}
	if err := setFedimintCredentialFileOwnership(target); err != nil {
		return errors.New("Fedimint Gateway certificate ownership is unsafe")
	}
	return nil
}

func (manager *ComposeAppManager) ensureFedimintGatewayInternalFirewall(ctx context.Context) error {
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", appmanifest.FedimintGatewayNetwork, "--format", "{{.Id}}")
	if err != nil {
		return errors.New("Fedimint Gateway network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return errors.New("Fedimint Gateway network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return errors.New("Fedimint Gateway network ID is invalid")
		}
	}
	bridge := "br-" + id[:12]
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", "in", "on", bridge, "to", "any", "port", "10009", "proto", "tcp"); err != nil {
		return errors.New("Fedimint Gateway internal firewall rule failed")
	}
	return nil
}

func (manager *ComposeAppManager) Logs(ctx context.Context, appID string, lines int, since string) (AppLogsState, error) {
	var state AppLogsState
	if lines < 1 || lines > 500 || !validComposeLogSince(since) {
		return state, errors.New("app log query is invalid")
	}
	var snapshot composeAppSnapshot
	var cleanup func()
	var err error
	switch appID {
	case appmanifest.BitcoinCoreID:
		snapshot, cleanup, err = manager.createBitcoinCoreInspectionSnapshot(ctx)
	case appmanifest.FedimintGuardianID, appmanifest.FedimintGatewayID:
		var files fedimintValidatedFiles
		files, err = manager.validatedFedimintFiles(appID)
		if err == nil {
			snapshot, cleanup, err = manager.createFedimintSnapshot(appID, files)
		}
	default:
		return state, errors.New("app log manifest is not allowed")
	}
	if err != nil {
		return state, err
	}
	defer cleanup()
	manifest, _ := appmanifest.ComposeManifestForApp(appID)
	commandPath, prefix, err := manager.resolveCompose(ctx)
	if err != nil {
		return state, err
	}
	args := append([]string(nil), prefix...)
	if snapshot.envPath != "" {
		args = append(args, "--env-file", snapshot.envPath)
	}
	args = append(args,
		"--project-name", manifest.Project,
		"--project-directory", snapshot.root,
		"-f", snapshot.composePath,
		"logs", "--no-color", "--tail", strconv.Itoa(lines),
	)
	if since != "" {
		args = append(args, "--since", since)
	}
	args = append(args, manifest.PrimaryService)
	output, err := manager.Runner.Run(ctx, commandPath, args...)
	if err != nil {
		return state, errors.New("app log command failed")
	}
	state.Source = "docker:" + manifest.PrimaryService
	state.Lines = boundedAppLogLines(output)
	return state, nil
}

func boundedAppLogLines(output string) []string {
	const maxTotal = 48 * 1024
	const maxLine = 4096
	result := make([]string, 0)
	total := 0
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) > maxLine {
			line = line[:maxLine]
		}
		if total+len(line)+1 > maxTotal {
			break
		}
		result = append(result, line)
		total += len(line) + 1
	}
	return result
}
