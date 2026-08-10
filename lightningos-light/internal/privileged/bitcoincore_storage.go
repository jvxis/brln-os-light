package privileged

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	bitcoinCoreStorageDataDirFile = "storage-data-dir"
	bitcoinCoreStorageIDFile      = "storage-id"
	bitcoinCoreStorageMinFreeKiB  = uint64(10 * 1024 * 1024)
)

type BitcoinCoreStorageManager struct {
	PrivilegedAppsRoot string
	MinFreeKiB         uint64
}

func NewBitcoinCoreStorageManager() *BitcoinCoreStorageManager {
	return &BitcoinCoreStorageManager{}
}

func (manager *BitcoinCoreStorageManager) Ensure(_ context.Context, dataDir string, dryRun bool) (BitcoinCoreStorageState, error) {
	normalized, _, err := manager.validateTarget(dataDir)
	if err != nil {
		return BitcoinCoreStorageState{}, err
	}
	if dryRun {
		return BitcoinCoreStorageState{Status: "validated"}, nil
	}
	root := manager.storageRoot()
	if err := ensureDirectoryTreeNoSymlink(root, 0700); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage metadata root is unsafe")
	}
	if err := validateRootOwnedDirectory(root, 0700); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage metadata root is not root-only")
	}

	dataDirPath := filepath.Join(root, bitcoinCoreStorageDataDirFile)
	storageIDPath := filepath.Join(root, bitcoinCoreStorageIDFile)
	storedDataDir, dataDirErr := readStorageMetadata(dataDirPath)
	storageID, storageIDErr := readStorageMetadata(storageIDPath)
	existing := dataDirErr == nil || storageIDErr == nil
	if existing {
		if dataDirErr != nil || storageIDErr != nil {
			return BitcoinCoreStorageState{}, errors.New("bitcoin storage metadata is incomplete")
		}
		if storedDataDir != normalized || !validStorageID(storageID) {
			return BitcoinCoreStorageState{}, errors.New("bitcoin storage metadata does not match the request")
		}
	} else if !os.IsNotExist(dataDirErr) || !os.IsNotExist(storageIDErr) {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage metadata is unavailable")
	}

	if err := os.MkdirAll(normalized, 0750); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage directory creation failed")
	}
	if err := ensureDirectoryTreeNoSymlink(normalized, 0750); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage path became unsafe")
	}
	if err := os.Chown(normalized, 101, 101); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage ownership update failed")
	}
	if err := manager.validateFreeSpace(normalized); err != nil {
		return BitcoinCoreStorageState{}, err
	}

	if existing {
		if err := writeBitcoinCoreStorageMarker(normalized, storageID); err != nil {
			return BitcoinCoreStorageState{}, err
		}
		return BitcoinCoreStorageState{Status: "ready"}, nil
	}

	storageID, err = newBitcoinCoreStorageID()
	if err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage identity generation failed")
	}
	if err := writeBitcoinCoreStorageMarker(normalized, storageID); err != nil {
		return BitcoinCoreStorageState{}, err
	}
	if err := writeAtomicRegularFile(storageIDPath, []byte(storageID+"\n"), 0600); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage identity persistence failed")
	}
	if err := writeAtomicRegularFile(dataDirPath, []byte(normalized+"\n"), 0600); err != nil {
		return BitcoinCoreStorageState{}, errors.New("bitcoin storage target persistence failed")
	}
	return BitcoinCoreStorageState{Status: "ready"}, nil
}

func (manager *BitcoinCoreStorageManager) validateTarget(dataDir string) (string, string, error) {
	normalized, err := appmanifest.NormalizeBitcoinCoreDataDir(dataDir)
	if err != nil || normalized != dataDir {
		return "", "", errors.New("bitcoin storage target is not canonical")
	}
	nearest, err := nearestExistingDirectory(normalized)
	if err != nil {
		return "", "", errors.New("bitcoin storage parent is unavailable")
	}
	if err := validateExistingDirectoryTree(nearest); err != nil {
		return "", "", errors.New("bitcoin storage path contains an unsafe component")
	}
	if normalized != appmanifest.BitcoinCoreDefaultDataDir {
		rootDevice, err := storageDeviceID("/")
		if err != nil {
			return "", "", errors.New("root storage identity is unavailable")
		}
		targetDevice, err := storageDeviceID(nearest)
		if err != nil || targetDevice == rootDevice {
			return "", "", errors.New("custom bitcoin storage must be on a mounted non-root filesystem")
		}
	}
	if err := manager.validateFreeSpace(nearest); err != nil {
		return "", "", err
	}
	return normalized, nearest, nil
}

func (manager *BitcoinCoreStorageManager) validateFreeSpace(path string) error {
	minimum := manager.MinFreeKiB
	if minimum == 0 {
		minimum = bitcoinCoreStorageMinFreeKiB
	}
	available, err := storageAvailableKiB(path)
	if err != nil || available < minimum {
		return errors.New("bitcoin storage has insufficient free space")
	}
	return nil
}

func (manager *BitcoinCoreStorageManager) storageRoot() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.BitcoinCoreID)
}

func nearestExistingDirectory(path string) (string, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return "", errors.New("nearest storage parent is not a regular directory")
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("no existing storage parent")
		}
		current = parent
	}
}

func validateExistingDirectoryTree(path string) error {
	clean := filepath.Clean(path)
	for current := clean; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid storage directory tree")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func readStorageMetadata(path string) (string, error) {
	if err := validateRootOwnedRegularFile(path, 0600); err != nil {
		return "", err
	}
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid bitcoin storage metadata")
	}
	return value, nil
}

func newBitcoinCoreStorageID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func validStorageID(value string) bool {
	if len(value) != 48 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeBitcoinCoreStorageMarker(dataDir string, storageID string) error {
	if !validStorageID(storageID) {
		return errors.New("bitcoin storage identity is invalid")
	}
	marker := filepath.Join(dataDir, appmanifest.BitcoinCoreStorageMarker)
	if err := writeAtomicRegularFile(marker, []byte(storageID+"\n"), 0640); err != nil {
		return errors.New("bitcoin storage marker write failed")
	}
	if err := os.Chown(marker, 0, 101); err != nil {
		return errors.New("bitcoin storage marker ownership update failed")
	}
	if err := os.Chmod(marker, 0640); err != nil {
		return errors.New("bitcoin storage marker mode update failed")
	}
	return nil
}
