//go:build linux

package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"

	"lightningos-light/internal/appmanifest"
)

func (manager *ComposeAppManager) createBitcoinCoreExecutionSnapshot(ctx context.Context, dryRun bool, requireConfig bool) (composeAppSnapshot, func(), error) {
	root, composeRaw, err := manager.bitcoinCoreExecutionDefinition(ctx, requireConfig)
	if err != nil {
		return composeAppSnapshot{}, func() {}, err
	}
	snapshot := composeAppSnapshot{
		root:        root,
		composePath: filepath.Join(root, appmanifest.BitcoinCoreComposeFile),
	}
	if dryRun {
		return snapshot, func() {}, nil
	}
	guardPath := filepath.Join(root, appmanifest.BitcoinCoreStorageGuardFile)
	if err := writeAtomicRegularFile(guardPath, []byte(appmanifest.BitcoinCoreStorageGuard()), 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to persist bitcoin storage guard")
	}
	if err := writeAtomicRegularFile(snapshot.composePath, composeRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to persist bitcoin execution manifest")
	}
	if err := validateRootOwnedRegularFile(guardPath, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("bitcoin storage guard is unsafe")
	}
	if err := validateRootOwnedRegularFile(snapshot.composePath, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("bitcoin execution manifest is unsafe")
	}
	return snapshot, func() {}, nil
}

func (manager *ComposeAppManager) createBitcoinCoreInspectionSnapshot(ctx context.Context) (composeAppSnapshot, func(), error) {
	_, composeRaw, err := manager.bitcoinCoreExecutionDefinition(ctx, false)
	if err != nil {
		return composeAppSnapshot{}, func() {}, err
	}
	manifest, _ := appmanifest.ComposeManifestForApp(appmanifest.BitcoinCoreID)
	return manager.createSnapshot(manifest, composeRaw, nil)
}

func (manager *ComposeAppManager) bitcoinCoreExecutionDefinition(ctx context.Context, requireConfig bool) (string, []byte, error) {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	root = filepath.Join(filepath.Clean(root), appmanifest.BitcoinCoreID)
	if err := validateRootOwnedDirectory(root, 0700); err != nil {
		return "", nil, errors.New("bitcoin execution root is not root-only")
	}
	dataDir, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageDataDirFile))
	if err != nil {
		return "", nil, errors.New("bitcoin storage enrollment is unavailable")
	}
	normalized, err := appmanifest.NormalizeBitcoinCoreDataDir(dataDir)
	if err != nil || normalized != dataDir {
		return "", nil, errors.New("bitcoin storage enrollment is invalid")
	}
	storageID, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageIDFile))
	if err != nil || !validStorageID(storageID) {
		return "", nil, errors.New("bitcoin storage identity is invalid")
	}
	if requireConfig {
		configManager := &BitcoinCoreConfigManager{PrivilegedAppsRoot: manager.PrivilegedAppsRoot}
		if _, err := configManager.Read(ctx, dataDir); err != nil {
			return "", nil, errors.New("bitcoin config is not ready")
		}
	}
	composeRaw, err := appmanifest.BitcoinCoreCompose(dataDir, filepath.ToSlash(root))
	if err != nil {
		return "", nil, errors.New("bitcoin execution manifest is invalid")
	}
	return root, []byte(composeRaw), nil
}

func (manager *ComposeAppManager) validateBitcoinCoreLifecycleAttestation() error {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		return err
	}
	path := manager.bitcoinCoreImageAttestationPath()
	if err := validateRootOwnedRegularFile(path, 0600); err != nil {
		return errors.New("bitcoin core image attestation is unsafe")
	}
	attestation, err := readBitcoinCoreImageAttestation(path)
	if err != nil || attestation.Release != appmanifest.BitcoinCoreRelease ||
		attestation.ArchiveSHA256 != artifact.ArchiveSHA256 || attestation.BaseImage != artifact.BaseImage ||
		attestation.Signatures < appmanifest.BitcoinCoreSignatureThreshold {
		return errors.New("bitcoin core image attestation is invalid")
	}
	return nil
}

func (manager *ComposeAppManager) removeBitcoinCoreExecutionSnapshot(snapshotRoot string) error {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(root), appmanifest.BitcoinCoreID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid bitcoin execution snapshot")
	}
	if err := validateRootOwnedDirectory(expectedRoot, 0700); err != nil {
		return errors.New("invalid bitcoin execution snapshot")
	}
	for _, name := range []string{appmanifest.BitcoinCoreComposeFile, appmanifest.BitcoinCoreStorageGuardFile} {
		path := filepath.Join(expectedRoot, name)
		if err := validateRootOwnedRegularFile(path, 0600); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return errors.New("invalid bitcoin execution asset")
		}
		if err := os.Remove(path); err != nil {
			return errors.New("failed to remove bitcoin execution asset")
		}
	}
	return nil
}
