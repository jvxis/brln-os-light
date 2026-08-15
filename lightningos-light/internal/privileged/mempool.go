package privileged

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
)

type mempoolValidatedFiles struct {
	composeRaw []byte
	envRaw     []byte
	runtime    appmanifest.MempoolRuntime
}

func (manager *ComposeAppManager) validatedMempoolFiles() (mempoolValidatedFiles, error) {
	var files mempoolValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.MempoolID)
	if err := validateSnapshotDirectoryEntries(appRoot, map[string]bool{
		appmanifest.MempoolComposeFile: true,
		appmanifest.MempoolEnvFile:     true,
	}); err != nil {
		return files, errors.New("Mempool app declaration contains unexpected assets")
	}
	envPath := filepath.Join(appRoot, appmanifest.MempoolEnvFile)
	if err := validateSecretFileMode(envPath); err != nil {
		return files, errors.New("Mempool environment is not private")
	}
	envRaw, err := readRegularFile(envPath, 2048)
	if err != nil {
		return files, errors.New("Mempool environment is unavailable")
	}
	runtime, err := appmanifest.ParseMempoolRuntimeEnv(envRaw)
	if err != nil {
		return files, errors.New("Mempool environment does not match the catalog")
	}
	composeRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.MempoolComposeFile), 64*1024)
	if err != nil {
		return files, errors.New("Mempool compose manifest is unavailable")
	}
	expected, err := appmanifest.MempoolCompose(runtime)
	if err != nil || !bytes.Equal(composeRaw, []byte(expected)) {
		return files, errors.New("Mempool compose manifest does not match the catalog")
	}
	files = mempoolValidatedFiles{composeRaw: composeRaw, envRaw: envRaw, runtime: runtime}
	return files, nil
}

func (manager *ComposeAppManager) createMempoolSnapshot(files mempoolValidatedFiles) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	privilegedRoot := manager.PrivilegedAppsRoot
	if privilegedRoot == "" {
		privilegedRoot = defaultPrivilegedAppsRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedRoot, appmanifest.MempoolID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure Mempool execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, map[string]bool{
		appmanifest.MempoolComposeFile: true,
		appmanifest.MempoolEnvFile:     true,
		catalogStorageDataDirFile:      true,
		catalogStorageIDFile:           true,
		catalogStorageMigrationFile:    true,
	}); err != nil {
		return snapshot, func() {}, errors.New("Mempool execution snapshot contains unexpected assets")
	}
	snapshot = composeAppSnapshot{
		root: snapshotRoot, composePath: filepath.Join(snapshotRoot, appmanifest.MempoolComposeFile),
		envPath: filepath.Join(snapshotRoot, appmanifest.MempoolEnvFile),
	}
	if files.runtime.DataDir != "" {
		if err := manager.validateCatalogStorageEnrollment(appmanifest.MempoolID, files.runtime.DataDir); err != nil {
			return composeAppSnapshot{}, func() {}, err
		}
	}
	if err := writeAtomicRegularFile(snapshot.composePath, files.composeRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Mempool compose manifest")
	}
	if err := writeAtomicRegularFile(snapshot.envPath, files.envRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Mempool environment")
	}
	return snapshot, func() {}, nil
}

func (manager *ComposeAppManager) removeMempoolExecutionSnapshot(snapshotRoot string) error {
	privilegedRoot := manager.PrivilegedAppsRoot
	if privilegedRoot == "" {
		privilegedRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedRoot), appmanifest.MempoolID)
	if filepath.Clean(snapshotRoot) != expectedRoot || validateRegularDirectory(expectedRoot) != nil {
		return errors.New("invalid Mempool execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(expectedRoot, map[string]bool{
		appmanifest.MempoolComposeFile: true,
		appmanifest.MempoolEnvFile:     true,
		catalogStorageDataDirFile:      true,
		catalogStorageIDFile:           true,
		catalogStorageMigrationFile:    true,
	}); err != nil {
		return errors.New("Mempool execution snapshot contains unexpected assets")
	}
	return os.RemoveAll(expectedRoot)
}

func (manager *ComposeAppManager) validateMempoolDependencies(ctx context.Context, runtime appmanifest.MempoolRuntime) error {
	electrsMode := appmanifest.ElectrsBitcoinModeApp
	if runtime.BitcoinMode == appmanifest.MempoolBitcoinModeNative {
		electrsMode = appmanifest.ElectrsBitcoinModeNative
	}
	cookie := []byte(runtime.BitcoinRPCUser + ":" + runtime.BitcoinRPCPass)
	if err := manager.validateElectrsBitcoin(ctx, appmanifest.ElectrsRuntime{
		BitcoinMode: electrsMode, Network: runtime.Network,
	}, cookie); err != nil {
		return err
	}
	output, err := manager.Runner.Run(ctx, dockerPath, "inspect", "--format={{.State.Running}}", "electrs")
	if err != nil || strings.TrimSpace(output) != "true" {
		return errors.New("Mempool requires Electrs to be running")
	}
	output, err = manager.Runner.Run(ctx, dockerPath, "inspect", "--format={{if index .NetworkSettings.Networks \"electrs_default\"}}connected{{end}}", "electrs")
	if err != nil || strings.TrimSpace(output) != "connected" {
		return errors.New("Mempool requires the catalog Electrs network")
	}
	return nil
}
