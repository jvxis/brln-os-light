package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	catalogStorageDataDirFile   = "storage-data-dir"
	catalogStorageIDFile        = "storage-id"
	catalogStorageMarkerFile    = ".lightningos-storage-id"
	catalogStorageMigrationFile = "storage-migration.json"
	electrsStorageMinFreeKiB    = uint64(100 * 1024 * 1024)
	mempoolStorageMinFreeKiB    = uint64(20 * 1024 * 1024)
	electrsStorageReserveKiB    = uint64(5 * 1024 * 1024)
	mempoolStorageReserveKiB    = uint64(2 * 1024 * 1024)
)

type CatalogStorage struct {
	PrivilegedAppsRoot string
}

func NewCatalogStorageManager() *CatalogStorage { return &CatalogStorage{} }

func (manager *CatalogStorage) Ensure(ctx context.Context, appID string, dataDir string, finalize bool, dryRun bool) (CatalogStorageState, error) {
	normalized, err := manager.validateTarget(appID, dataDir)
	if err != nil {
		return CatalogStorageState{}, err
	}
	if dryRun {
		return CatalogStorageState{Status: "validated"}, nil
	}
	metadataRoot := manager.metadataRoot(appID)
	if err := ensureDirectoryTreeNoSymlink(metadataRoot, 0700); err != nil || validateRootOwnedDirectory(metadataRoot, 0700) != nil {
		return CatalogStorageState{}, errors.New("catalog storage metadata root is unsafe")
	}
	dataPath := filepath.Join(metadataRoot, catalogStorageDataDirFile)
	idPath := filepath.Join(metadataRoot, catalogStorageIDFile)
	storedDir, dataErr := readStorageMetadata(dataPath)
	storageID, idErr := readStorageMetadata(idPath)
	existing := dataErr == nil || idErr == nil
	if existing {
		if dataErr != nil || idErr != nil || storedDir != normalized || !validStorageID(storageID) {
			return CatalogStorageState{}, errors.New("catalog storage metadata does not match the request")
		}
	} else if !os.IsNotExist(dataErr) || !os.IsNotExist(idErr) {
		return CatalogStorageState{}, errors.New("catalog storage metadata is unavailable")
	}
	if err := validateCatalogFreeSpace(appID, normalized, !existing); err != nil {
		return CatalogStorageState{}, err
	}

	if err := os.MkdirAll(normalized, 0750); err != nil || ensureDirectoryTreeNoSymlink(normalized, 0750) != nil {
		return CatalogStorageState{}, errors.New("catalog storage directory is unsafe")
	}
	if err := os.Chown(normalized, 0, 0); err != nil || os.Chmod(normalized, 0750) != nil {
		return CatalogStorageState{}, errors.New("catalog storage root ownership update failed")
	}
	for _, child := range catalogStorageChildren(appID) {
		childPath := filepath.Join(normalized, child.name)
		if err := os.MkdirAll(childPath, 0750); err != nil || ensureDirectoryTreeNoSymlink(childPath, 0750) != nil {
			return CatalogStorageState{}, errors.New("catalog storage data directory is unsafe")
		}
		if err := os.Chown(childPath, child.uid, child.gid); err != nil || os.Chmod(childPath, 0750) != nil {
			return CatalogStorageState{}, errors.New("catalog storage data ownership update failed")
		}
	}
	if !existing {
		storageID, err = newBitcoinCoreStorageID()
		if err != nil {
			return CatalogStorageState{}, errors.New("catalog storage identity generation failed")
		}
	}
	if err := writeCatalogStorageMarker(normalized, storageID); err != nil {
		return CatalogStorageState{}, err
	}
	if !existing {
		if err := writeAtomicRegularFile(idPath, []byte(storageID+"\n"), 0600); err != nil {
			return CatalogStorageState{}, errors.New("catalog storage identity persistence failed")
		}
		if err := writeAtomicRegularFile(dataPath, []byte(normalized+"\n"), 0600); err != nil {
			return CatalogStorageState{}, errors.New("catalog storage target persistence failed")
		}
	}
	if finalize {
		if err := manager.finalizeLegacyMigration(ctx, appID, normalized); err != nil {
			return CatalogStorageState{}, err
		}
		return CatalogStorageState{Status: "ready"}, nil
	}
	if err := manager.prepareLegacyMigration(ctx, appID, normalized); err != nil {
		return CatalogStorageState{}, err
	}
	return CatalogStorageState{Status: "ready"}, nil
}

type catalogTreeSummary struct {
	Files       int64 `json:"files"`
	Directories int64 `json:"directories"`
	Bytes       int64 `json:"bytes"`
}

type catalogMigrationEntry struct {
	Source  string             `json:"source"`
	Target  string             `json:"target"`
	Summary catalogTreeSummary `json:"summary"`
}

type catalogMigrationRecord struct {
	AppID   string                  `json:"app_id"`
	DataDir string                  `json:"data_dir"`
	Entries []catalogMigrationEntry `json:"entries"`
}

func legacyCatalogSources(appID string) map[string]string {
	if appID == appmanifest.ElectrsID {
		return map[string]string{"db": "/var/lib/docker/volumes/electrs_data/_data"}
	}
	return map[string]string{
		"db":    "/var/lib/docker/volumes/mempool_dbdata/_data",
		"cache": "/var/lib/docker/volumes/mempool_cache/_data",
	}
}

func (manager *CatalogStorage) prepareLegacyMigration(ctx context.Context, appID, dataDir string) error {
	recordPath := filepath.Join(manager.metadataRoot(appID), catalogStorageMigrationFile)
	if _, err := os.Lstat(recordPath); err == nil {
		return manager.validateMigrationRecord(appID, dataDir)
	} else if !os.IsNotExist(err) {
		return errors.New("catalog storage migration state is unavailable")
	}
	record := catalogMigrationRecord{AppID: appID, DataDir: dataDir}
	for child, source := range legacyCatalogSources(appID) {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourceSummary, exists, err := catalogDirectorySummary(source)
		if err != nil {
			return errors.New("legacy catalog storage is unsafe")
		}
		if !exists || sourceSummary.Files == 0 {
			continue
		}
		target := filepath.Join(dataDir, child)
		targetSummary, _, err := catalogDirectorySummary(target)
		if err != nil {
			return errors.New("catalog storage migration target is unsafe")
		}
		if targetSummary.Files == 0 && targetSummary.Directories <= 1 {
			ownership := catalogStorageChildren(appID)
			uid, gid := 0, 0
			for _, candidate := range ownership {
				if candidate.name == child {
					uid, gid = candidate.uid, candidate.gid
				}
			}
			if err := copyCatalogTree(ctx, source, target, uid, gid); err != nil {
				return errors.New("legacy catalog storage migration failed")
			}
			targetSummary, _, err = catalogDirectorySummary(target)
			if err != nil {
				return errors.New("catalog storage migration verification failed")
			}
		}
		if targetSummary != sourceSummary {
			return errors.New("catalog storage migration verification failed")
		}
		record.Entries = append(record.Entries, catalogMigrationEntry{Source: source, Target: target, Summary: sourceSummary})
	}
	if len(record.Entries) == 0 {
		return nil
	}
	raw, err := json.Marshal(record)
	if err != nil || writeAtomicRegularFile(recordPath, append(raw, '\n'), 0600) != nil {
		return errors.New("catalog storage migration state persistence failed")
	}
	return nil
}

func (manager *CatalogStorage) validateMigrationRecord(appID, dataDir string) error {
	record, err := manager.readMigrationRecord(appID)
	if err != nil || record.AppID != appID || record.DataDir != dataDir || len(record.Entries) == 0 {
		return errors.New("catalog storage migration state is invalid")
	}
	expected := legacyCatalogSources(appID)
	for _, entry := range record.Entries {
		child := filepath.Base(entry.Target)
		if expected[child] != entry.Source || entry.Target != filepath.Join(dataDir, child) {
			return errors.New("catalog storage migration state is invalid")
		}
		targetSummary, exists, err := catalogDirectorySummary(entry.Target)
		if err != nil || !exists || targetSummary != entry.Summary {
			return errors.New("catalog storage migration target changed")
		}
	}
	return nil
}

func (manager *CatalogStorage) finalizeLegacyMigration(ctx context.Context, appID, dataDir string) error {
	recordPath := filepath.Join(manager.metadataRoot(appID), catalogStorageMigrationFile)
	if _, err := os.Lstat(recordPath); os.IsNotExist(err) {
		return nil
	}
	if err := manager.validateMigrationRecord(appID, dataDir); err != nil {
		return err
	}
	record, _ := manager.readMigrationRecord(appID)
	for _, entry := range record.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourceSummary, exists, err := catalogDirectorySummary(entry.Source)
		if err != nil || !exists || sourceSummary != entry.Summary {
			return errors.New("legacy catalog storage changed before cleanup")
		}
		children, err := os.ReadDir(entry.Source)
		if err != nil {
			return errors.New("legacy catalog storage cleanup failed")
		}
		for _, child := range children {
			target := filepath.Join(entry.Source, child.Name())
			if filepath.Dir(target) != entry.Source {
				return errors.New("legacy catalog storage cleanup target is invalid")
			}
			if err := os.RemoveAll(target); err != nil {
				return errors.New("legacy catalog storage cleanup failed")
			}
		}
	}
	if err := os.Remove(recordPath); err != nil {
		return errors.New("catalog storage migration finalization failed")
	}
	return nil
}

func (manager *CatalogStorage) readMigrationRecord(appID string) (catalogMigrationRecord, error) {
	var record catalogMigrationRecord
	path := filepath.Join(manager.metadataRoot(appID), catalogStorageMigrationFile)
	if err := validateRootOwnedRegularFile(path, 0600); err != nil {
		return record, err
	}
	raw, err := readRegularFile(path, 16*1024)
	if err != nil || json.Unmarshal(raw, &record) != nil {
		return record, errors.New("catalog storage migration state is invalid")
	}
	return record, nil
}

func catalogDirectorySummary(root string) (catalogTreeSummary, bool, error) {
	var summary catalogTreeSummary
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return summary, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return summary, false, errors.New("catalog directory is unsafe")
	}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("catalog directory contains an unsafe entry")
		}
		if entry.IsDir() {
			summary.Directories++
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("catalog directory contains a non-regular entry")
		}
		summary.Files++
		summary.Bytes += info.Size()
		return nil
	})
	return summary, true, err
}

func copyCatalogTree(ctx context.Context, source, target string, uid, gid int) error {
	return filepath.WalkDir(source, func(sourcePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, sourcePath)
		if err != nil || rel == ".." || filepath.IsAbs(rel) {
			return errors.New("invalid migration source")
		}
		targetPath := filepath.Join(target, rel)
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe migration entry")
		}
		if entry.IsDir() {
			if err := os.MkdirAll(targetPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := os.Chown(targetPath, uid, gid); err != nil {
				return err
			}
			return os.Chmod(targetPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsupported migration entry")
		}
		return copyCatalogFile(sourcePath, targetPath, info, uid, gid)
	})
}

func copyCatalogFile(source, target string, info os.FileInfo, uid, gid int) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = output.Close()
		if !ok {
			_ = os.Remove(target)
		}
	}()
	if _, err := io.Copy(output, input); err != nil || output.Sync() != nil || output.Close() != nil {
		return errors.New("catalog file copy failed")
	}
	if err := os.Chown(target, uid, gid); err != nil || os.Chmod(target, info.Mode().Perm()) != nil || os.Chtimes(target, time.Now(), info.ModTime()) != nil {
		return errors.New("catalog file metadata copy failed")
	}
	ok = true
	return nil
}

type catalogStorageChild struct {
	name     string
	uid, gid int
}

func catalogStorageChildren(appID string) []catalogStorageChild {
	if appID == appmanifest.ElectrsID {
		return []catalogStorageChild{{name: "db", uid: appmanifest.ElectrsContainerUID, gid: appmanifest.ElectrsContainerGID}}
	}
	return []catalogStorageChild{
		{name: "db", uid: appmanifest.MempoolDatabaseUID, gid: appmanifest.MempoolDatabaseGID},
		{name: "cache", uid: appmanifest.MempoolContainerUID, gid: appmanifest.MempoolContainerGID},
	}
}

func (manager *CatalogStorage) validateTarget(appID, dataDir string) (string, error) {
	normalized, err := appmanifest.NormalizeCatalogDataDir(appID, dataDir)
	if err != nil || normalized != dataDir {
		return "", errors.New("catalog storage target is not canonical")
	}
	nearest, err := nearestExistingDirectory(normalized)
	if err != nil || validateExistingDirectoryTree(nearest) != nil {
		return "", errors.New("catalog storage path contains an unsafe component")
	}
	defaultPath := appmanifest.ElectrsDefaultDataDir
	if appID == appmanifest.MempoolID {
		defaultPath = appmanifest.MempoolDefaultDataDir
	}
	if normalized != defaultPath {
		rootDevice, rootErr := storageDeviceID("/")
		targetDevice, targetErr := storageDeviceID(nearest)
		if rootErr != nil || targetErr != nil || rootDevice == targetDevice {
			return "", errors.New("custom catalog storage must be on a mounted non-root filesystem")
		}
	}
	return normalized, nil
}

func validateCatalogFreeSpace(appID, dataDir string, initial bool) error {
	nearest, err := nearestExistingDirectory(dataDir)
	if err != nil {
		return errors.New("catalog storage parent is unavailable")
	}
	minimum := electrsStorageReserveKiB
	if initial {
		minimum = electrsStorageMinFreeKiB
	}
	if appID == appmanifest.MempoolID {
		minimum = mempoolStorageReserveKiB
		if initial {
			minimum = mempoolStorageMinFreeKiB
		}
	}
	available, err := storageAvailableKiB(nearest)
	if err != nil || available < minimum {
		return errors.New("catalog storage has insufficient free space")
	}
	return nil
}

func (manager *CatalogStorage) metadataRoot(appID string) string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appID)
}

func writeCatalogStorageMarker(dataDir, storageID string) error {
	if !validStorageID(storageID) {
		return errors.New("catalog storage identity is invalid")
	}
	marker := filepath.Join(dataDir, catalogStorageMarkerFile)
	if err := writeAtomicRegularFile(marker, []byte(storageID+"\n"), 0600); err != nil || os.Chown(marker, 0, 0) != nil || os.Chmod(marker, 0600) != nil {
		return errors.New("catalog storage marker update failed")
	}
	return nil
}

func (manager *ComposeAppManager) validateCatalogStorageEnrollment(appID, dataDir string) error {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	metadataRoot := filepath.Join(root, appID)
	storedDir, err := readStorageMetadata(filepath.Join(metadataRoot, catalogStorageDataDirFile))
	if err != nil || storedDir != dataDir {
		return errors.New("catalog storage enrollment is unavailable")
	}
	storageID, err := readStorageMetadata(filepath.Join(metadataRoot, catalogStorageIDFile))
	if err != nil || !validStorageID(storageID) {
		return errors.New("catalog storage identity is invalid")
	}
	marker := filepath.Join(dataDir, catalogStorageMarkerFile)
	if err := validateRootOwnedRegularFile(marker, 0600); err != nil {
		return errors.New("catalog storage marker is unsafe")
	}
	raw, err := readRegularFile(marker, 128)
	if err != nil || string(raw) != storageID+"\n" {
		return errors.New("catalog storage marker does not match")
	}
	return nil
}
