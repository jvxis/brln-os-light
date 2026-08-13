//go:build linux

package privileged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"

	"golang.org/x/sys/unix"
)

func (manager *BitcoinCoreConfigManager) Ensure(ctx context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (BitcoinCoreConfigState, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if generateRPCAuth {
		if _, err := bitcoinCoreConfigWithRPCAuth(content, "validation:00000000000000000000000000000000$0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
			return BitcoinCoreConfigState{}, err
		}
	}
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)

	existing, exists, original, legacyOwner, err := readBitcoinCoreConfigForEnsureAt(directoryFD)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if exists {
		if err := validateBitcoinCoreConfigContent(existing); err != nil {
			return BitcoinCoreConfigState{}, errors.New("existing bitcoin config is invalid")
		}
		if legacyOwner {
			if dryRun {
				return BitcoinCoreConfigState{Status: "validated"}, nil
			}
			if err := writeBitcoinCoreConfigAt(ctx, directoryFD, existing, &original); err != nil {
				return BitcoinCoreConfigState{}, err
			}
		}
		return BitcoinCoreConfigState{Status: "ready"}, nil
	}
	if dryRun {
		return BitcoinCoreConfigState{Status: "validated"}, nil
	}
	if generateRPCAuth {
		credentials, err := manager.ensureCredentials()
		if err != nil {
			return BitcoinCoreConfigState{}, err
		}
		content, err = bitcoinCoreConfigWithRPCAuth(content, credentials.RPCAuth)
		if err != nil {
			return BitcoinCoreConfigState{}, err
		}
	}
	if err := writeBitcoinCoreConfigAt(ctx, directoryFD, content, nil); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	return BitcoinCoreConfigState{Status: "ready"}, nil
}

func (manager *BitcoinCoreConfigManager) Credentials(_ context.Context, dataDir string) (BitcoinCoreCredentialsState, error) {
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreCredentialsState{}, err
	}
	defer unix.Close(directoryFD)

	credentials, err := manager.readCredentials()
	if err != nil {
		return BitcoinCoreCredentialsState{}, err
	}
	content, exists, err := readBitcoinCoreConfigAt(directoryFD)
	if err != nil || !exists || !bitcoinCoreConfigContainsRPCAuth(content, credentials.RPCAuth) {
		return BitcoinCoreCredentialsState{}, errors.New("bitcoin RPC credentials do not match the active config")
	}
	return BitcoinCoreCredentialsState{
		Status:   "ready",
		User:     credentials.User,
		Password: credentials.Password,
	}, nil
}

func (manager *BitcoinCoreConfigManager) EnsureCredentials(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreCredentialsEnsureState, error) {
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreCredentialsEnsureState{}, err
	}
	defer unix.Close(directoryFD)

	content, exists, original, legacyOwner, err := readBitcoinCoreConfigForEnsureAt(directoryFD)
	if err != nil || !exists {
		return BitcoinCoreCredentialsEnsureState{}, errors.New("bitcoin config does not exist")
	}
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreCredentialsEnsureState{}, errors.New("existing bitcoin config is invalid")
	}

	credentials, credentialErr := manager.readCredentials()
	if credentialErr != nil && !os.IsNotExist(credentialErr) {
		return BitcoinCoreCredentialsEnsureState{}, errors.New("bitcoin RPC credential state is invalid")
	}
	if os.IsNotExist(credentialErr) {
		placeholder := appmanifest.BitcoinCoreRPCUser + ":00000000000000000000000000000000$0000000000000000000000000000000000000000000000000000000000000000"
		if _, _, err := bitcoinCoreConfigWithManagedRPCAuth(content, placeholder, appmanifest.BitcoinCoreRPCUser, true); err != nil {
			return BitcoinCoreCredentialsEnsureState{}, err
		}
		if dryRun {
			return BitcoinCoreCredentialsEnsureState{Status: "validated"}, nil
		}
		credentials, err = generateBitcoinCoreCredentials()
		if err != nil {
			return BitcoinCoreCredentialsEnsureState{}, errors.New("generate bitcoin RPC credentials failed")
		}
		if err := manager.writeCredentialsFile(bitcoinCoreCredentialsFile, credentials, appmanifest.BitcoinCoreRPCUser); err != nil {
			return BitcoinCoreCredentialsEnsureState{}, err
		}
	} else if dryRun {
		if _, _, err := bitcoinCoreConfigWithManagedRPCAuth(content, credentials.RPCAuth, appmanifest.BitcoinCoreRPCUser, true); err != nil {
			return BitcoinCoreCredentialsEnsureState{}, err
		}
		return BitcoinCoreCredentialsEnsureState{Status: "validated"}, nil
	}

	updated, changed, err := bitcoinCoreConfigWithManagedRPCAuth(content, credentials.RPCAuth, appmanifest.BitcoinCoreRPCUser, true)
	if err != nil {
		return BitcoinCoreCredentialsEnsureState{}, err
	}
	if changed || legacyOwner {
		if err := writeBitcoinCoreConfigAt(ctx, directoryFD, updated, &original); err != nil {
			return BitcoinCoreCredentialsEnsureState{}, err
		}
	}
	if changed || legacyOwner {
		return BitcoinCoreCredentialsEnsureState{
			Status: "restart_required", User: credentials.User, Password: credentials.Password, ConfigChanged: changed,
		}, nil
	}
	if err := probeBitcoinCoreElectrsCredential(ctx, credentials.User, credentials.Password); err != nil {
		if errors.Is(err, errElectrsBitcoinRPCAuthentication) {
			return BitcoinCoreCredentialsEnsureState{
				Status: "restart_required", User: credentials.User, Password: credentials.Password,
			}, nil
		}
		return BitcoinCoreCredentialsEnsureState{}, errors.New("bitcoin RPC credential activation check failed")
	}
	return BitcoinCoreCredentialsEnsureState{
		Status: "ready", User: credentials.User, Password: credentials.Password, ConfigChanged: changed,
	}, nil
}

func (manager *BitcoinCoreConfigManager) EnsureElectrsCredentials(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreElectrsCredentialsState, error) {
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreElectrsCredentialsState{}, err
	}
	defer unix.Close(directoryFD)

	content, exists, original, _, err := readBitcoinCoreConfigForEnsureAt(directoryFD)
	if err != nil || !exists {
		return BitcoinCoreElectrsCredentialsState{}, errors.New("bitcoin config does not exist")
	}
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreElectrsCredentialsState{}, errors.New("existing bitcoin config is invalid")
	}

	credentials, credentialErr := manager.readCredentialsFile(bitcoinCoreElectrsCredentialsFile, appmanifest.ElectrsBitcoinRPCUser)
	if credentialErr != nil && !os.IsNotExist(credentialErr) {
		return BitcoinCoreElectrsCredentialsState{}, errors.New("Electrs RPC credential state is invalid")
	}
	if os.IsNotExist(credentialErr) {
		placeholder := appmanifest.ElectrsBitcoinRPCUser + ":00000000000000000000000000000000$0000000000000000000000000000000000000000000000000000000000000000"
		if _, _, err := bitcoinCoreConfigWithElectrsRPCAuth(content, placeholder); err != nil {
			return BitcoinCoreElectrsCredentialsState{}, err
		}
		if dryRun {
			return BitcoinCoreElectrsCredentialsState{Status: "validated"}, nil
		}
		credentials, err = generateBitcoinCoreCredentialsForUser(appmanifest.ElectrsBitcoinRPCUser)
		if err != nil {
			return BitcoinCoreElectrsCredentialsState{}, errors.New("generate Electrs RPC credentials failed")
		}
		if err := manager.writeCredentialsFile(bitcoinCoreElectrsCredentialsFile, credentials, appmanifest.ElectrsBitcoinRPCUser); err != nil {
			return BitcoinCoreElectrsCredentialsState{}, err
		}
	} else if dryRun {
		if _, _, err := bitcoinCoreConfigWithElectrsRPCAuth(content, credentials.RPCAuth); err != nil {
			return BitcoinCoreElectrsCredentialsState{}, err
		}
		return BitcoinCoreElectrsCredentialsState{Status: "validated"}, nil
	}

	updated, changed, err := bitcoinCoreConfigWithElectrsRPCAuth(content, credentials.RPCAuth)
	if err != nil {
		return BitcoinCoreElectrsCredentialsState{}, err
	}
	if changed {
		if err := writeBitcoinCoreConfigAt(ctx, directoryFD, updated, &original); err != nil {
			return BitcoinCoreElectrsCredentialsState{}, err
		}
		return BitcoinCoreElectrsCredentialsState{
			Status: "restart_required", User: credentials.User, Password: credentials.Password, ConfigChanged: true,
		}, nil
	}
	probe := manager.ElectrsCredentialProbe
	if probe == nil {
		probe = probeBitcoinCoreElectrsCredential
	}
	if err := probe(ctx, credentials.User, credentials.Password); err != nil {
		if errors.Is(err, errElectrsBitcoinRPCAuthentication) {
			return BitcoinCoreElectrsCredentialsState{
				Status: "restart_required", User: credentials.User, Password: credentials.Password,
			}, nil
		}
		return BitcoinCoreElectrsCredentialsState{}, errors.New("Electrs RPC credential activation check failed")
	}
	return BitcoinCoreElectrsCredentialsState{
		Status: "ready", User: credentials.User, Password: credentials.Password, ConfigChanged: changed,
	}, nil
}

func probeBitcoinCoreElectrsCredential(ctx context.Context, user string, password string) error {
	var result struct{}
	return callElectrsBitcoinRPC(ctx, &http.Client{Timeout: 3 * time.Second}, "http://127.0.0.1:8332/", user, password, "getblockchaininfo", nil, &result)
}

func (manager *BitcoinCoreConfigManager) ensureCredentials() (bitcoinCoreStoredCredentials, error) {
	credentials, err := manager.readCredentials()
	if err == nil {
		return credentials, nil
	}
	if !os.IsNotExist(err) {
		return bitcoinCoreStoredCredentials{}, err
	}
	credentials, err = generateBitcoinCoreCredentials()
	if err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("generate bitcoin RPC credentials failed")
	}
	raw, err := marshalBitcoinCoreCredentials(credentials)
	if err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("encode bitcoin RPC credentials failed")
	}
	root := manager.storageRoot()
	if err := validateRootOwnedDirectory(root, 0o700); err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("bitcoin credential root is unsafe")
	}
	path := filepath.Join(root, bitcoinCoreCredentialsFile)
	if err := writeAtomicRegularFile(path, raw, 0o600); err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("persist bitcoin RPC credentials failed")
	}
	if err := validateRootOwnedRegularFile(path, 0o600); err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("bitcoin RPC credentials are unsafe")
	}
	return credentials, nil
}

func (manager *BitcoinCoreConfigManager) writeCredentialsFile(name string, credentials bitcoinCoreStoredCredentials, expectedUser string) error {
	raw, err := marshalBitcoinCoreCredentialsForUser(credentials, expectedUser)
	if err != nil {
		return errors.New("encode bitcoin RPC credentials failed")
	}
	root := manager.storageRoot()
	if err := validateRootOwnedDirectory(root, 0o700); err != nil {
		return errors.New("bitcoin credential root is unsafe")
	}
	path := filepath.Join(root, name)
	if err := writeAtomicRegularFile(path, raw, 0o600); err != nil {
		return errors.New("persist bitcoin RPC credentials failed")
	}
	if err := validateRootOwnedRegularFile(path, 0o600); err != nil {
		return errors.New("bitcoin RPC credentials are unsafe")
	}
	return nil
}

func (manager *BitcoinCoreConfigManager) readCredentials() (bitcoinCoreStoredCredentials, error) {
	return manager.readCredentialsFile(bitcoinCoreCredentialsFile, appmanifest.BitcoinCoreRPCUser)
}

func (manager *BitcoinCoreConfigManager) readCredentialsFile(name string, expectedUser string) (bitcoinCoreStoredCredentials, error) {
	root := manager.storageRoot()
	if err := validateRootOwnedDirectory(root, 0o700); err != nil {
		return bitcoinCoreStoredCredentials{}, errors.New("bitcoin credential root is unsafe")
	}
	path := filepath.Join(root, name)
	if err := validateRootOwnedRegularFile(path, 0o600); err != nil {
		return bitcoinCoreStoredCredentials{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || len(raw) > maxBitcoinCoreCredentialsBytes {
		return bitcoinCoreStoredCredentials{}, errors.New("read bitcoin RPC credentials failed")
	}
	var credentials bitcoinCoreStoredCredentials
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&credentials); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return bitcoinCoreStoredCredentials{}, errors.New("bitcoin RPC credentials are invalid")
	}
	if err := validateBitcoinCoreCredentialsForUser(credentials, expectedUser); err != nil {
		return bitcoinCoreStoredCredentials{}, err
	}
	return credentials, nil
}

func bitcoinCoreConfigContainsRPCAuth(content string, rpcAuth string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if trimmed == "rpcauth="+rpcAuth {
			return true
		}
	}
	return false
}

func (manager *BitcoinCoreConfigManager) Read(_ context.Context, dataDir string) (BitcoinCoreConfigState, error) {
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)
	content, exists, err := readBitcoinCoreConfigAt(directoryFD)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if !exists {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config does not exist")
	}
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config is invalid")
	}
	return BitcoinCoreConfigState{Status: "ready", Content: content}, nil
}

func (manager *BitcoinCoreConfigManager) Write(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	directoryFD, err := manager.openEnrolledDataDir(dataDir)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	defer unix.Close(directoryFD)

	var original unix.Stat_t
	exists, err := statBitcoinCoreConfigAt(directoryFD, &original)
	if err != nil {
		return BitcoinCoreConfigState{}, err
	}
	if !exists {
		return BitcoinCoreConfigState{}, errors.New("bitcoin config does not exist")
	}
	if dryRun {
		return BitcoinCoreConfigState{Status: "validated"}, nil
	}
	if err := writeBitcoinCoreConfigAt(ctx, directoryFD, content, &original); err != nil {
		return BitcoinCoreConfigState{}, err
	}
	return BitcoinCoreConfigState{Status: "ready"}, nil
}

func (manager *BitcoinCoreConfigManager) openEnrolledDataDir(dataDir string) (int, error) {
	if err := validateBitcoinCoreConfigDataDir(dataDir); err != nil {
		return -1, err
	}
	root := manager.storageRoot()
	storedDataDir, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageDataDirFile))
	if err != nil || storedDataDir != dataDir {
		return -1, errors.New("bitcoin config storage enrollment is invalid")
	}
	storageID, err := readStorageMetadata(filepath.Join(root, bitcoinCoreStorageIDFile))
	if err != nil || !validStorageID(storageID) {
		return -1, errors.New("bitcoin config storage identity is invalid")
	}

	directoryFD, err := unix.Openat2(unix.AT_FDCWD, dataDir, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC),
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return -1, errors.New("open bitcoin config directory failed")
	}
	var directory unix.Stat_t
	if err := unix.Fstat(directoryFD, &directory); err != nil ||
		directory.Mode&unix.S_IFMT != unix.S_IFDIR || directory.Uid != 101 || directory.Gid != 101 ||
		os.FileMode(directory.Mode).Perm()&0o027 != 0 || os.FileMode(directory.Mode).Perm()&0o700 != 0o700 {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config directory is unsafe")
	}
	markerFD, err := unix.Openat(directoryFD, bitcoinCoreStorageMarkerFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker is unavailable")
	}
	marker := os.NewFile(uintptr(markerFD), bitcoinCoreStorageMarkerFile)
	if marker == nil {
		_ = unix.Close(markerFD)
		_ = unix.Close(directoryFD)
		return -1, errors.New("open bitcoin config storage marker failed")
	}
	defer marker.Close()
	var markerStat unix.Stat_t
	if err := unix.Fstat(markerFD, &markerStat); err != nil || markerStat.Mode&unix.S_IFMT != unix.S_IFREG ||
		markerStat.Uid != 0 || markerStat.Gid != 101 || os.FileMode(markerStat.Mode).Perm() != 0o640 || markerStat.Size > 128 {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker is unsafe")
	}
	raw, err := io.ReadAll(io.LimitReader(marker, 129))
	if err != nil || string(raw) != storageID+"\n" {
		_ = unix.Close(directoryFD)
		return -1, errors.New("bitcoin config storage marker does not match")
	}
	return directoryFD, nil
}

func readBitcoinCoreConfigForEnsureAt(directoryFD int) (string, bool, unix.Stat_t, bool, error) {
	var stat unix.Stat_t
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", false, stat, false, nil
	}
	if err != nil {
		return "", false, stat, false, errors.New("open bitcoin config failed")
	}
	file := os.NewFile(uintptr(configFD), bitcoinCoreConfigFile)
	if file == nil {
		_ = unix.Close(configFD)
		return "", false, stat, false, errors.New("open bitcoin config failed")
	}
	defer file.Close()
	if err := unix.Fstat(configFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		(stat.Uid != 0 && stat.Uid != 101) || stat.Gid != 101 || os.FileMode(stat.Mode).Perm() != 0o640 ||
		stat.Size <= 0 || stat.Size > maxBitcoinCoreConfigBytes {
		return "", false, stat, false, errors.New("bitcoin config file is unsafe")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBitcoinCoreConfigBytes+1))
	if err != nil || len(raw) > maxBitcoinCoreConfigBytes {
		return "", false, stat, false, errors.New("read bitcoin config failed")
	}
	return string(raw), true, stat, stat.Uid == 101, nil
}

func readBitcoinCoreConfigAt(directoryFD int) (string, bool, error) {
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("open bitcoin config failed")
	}
	file := os.NewFile(uintptr(configFD), bitcoinCoreConfigFile)
	if file == nil {
		_ = unix.Close(configFD)
		return "", false, errors.New("open bitcoin config failed")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := validateBitcoinCoreConfigStat(configFD, &stat); err != nil {
		return "", false, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBitcoinCoreConfigBytes+1))
	if err != nil || len(raw) > maxBitcoinCoreConfigBytes {
		return "", false, errors.New("read bitcoin config failed")
	}
	return string(raw), true, nil
}

func statBitcoinCoreConfigAt(directoryFD int, stat *unix.Stat_t) (bool, error) {
	return statBitcoinCoreConfigWithOwnerAt(directoryFD, stat, 0)
}

func statBitcoinCoreConfigWithOwnerAt(directoryFD int, stat *unix.Stat_t, expectedUID uint32) (bool, error) {
	configFD, err := unix.Openat(directoryFD, bitcoinCoreConfigFile, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("open bitcoin config failed")
	}
	defer unix.Close(configFD)
	if err := validateBitcoinCoreConfigStatWithOwner(configFD, stat, expectedUID); err != nil {
		return false, err
	}
	return true, nil
}

func validateBitcoinCoreConfigStat(fd int, stat *unix.Stat_t) error {
	return validateBitcoinCoreConfigStatWithOwner(fd, stat, 0)
}

func validateBitcoinCoreConfigStatWithOwner(fd int, stat *unix.Stat_t, expectedUID uint32) error {
	if err := unix.Fstat(fd, stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Uid != expectedUID || stat.Gid != 101 || os.FileMode(stat.Mode).Perm() != 0o640 ||
		stat.Size <= 0 || stat.Size > maxBitcoinCoreConfigBytes {
		return errors.New("bitcoin config file is unsafe")
	}
	return nil
}

func writeBitcoinCoreConfigAt(ctx context.Context, directoryFD int, content string, original *unix.Stat_t) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporaryName, err := newBitcoinCoreConfigTemporaryName()
	if err != nil {
		return errors.New("bitcoin config temporary name generation failed")
	}
	temporaryFD, err := unix.Openat(directoryFD, temporaryName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return errors.New("create bitcoin config temporary file failed")
	}
	committed := false
	defer func() {
		if !committed {
			_ = unix.Unlinkat(directoryFD, temporaryName, 0)
		}
	}()
	temporary := os.NewFile(uintptr(temporaryFD), temporaryName)
	if temporary == nil {
		_ = unix.Close(temporaryFD)
		return errors.New("create bitcoin config temporary file failed")
	}
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
	}()
	if err := temporary.Chown(0, 101); err != nil || temporary.Chmod(0o640) != nil {
		return errors.New("secure bitcoin config temporary file failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write bitcoin config temporary file failed")
	}
	if err := temporary.Sync(); err != nil || temporary.Close() != nil {
		return errors.New("sync bitcoin config temporary file failed")
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return err
	}

	var current unix.Stat_t
	expectedUID := uint32(0)
	if original != nil {
		expectedUID = original.Uid
	}
	exists, err := statBitcoinCoreConfigWithOwnerAt(directoryFD, &current, expectedUID)
	if err != nil {
		return err
	}
	if original == nil {
		if exists {
			return errors.New("bitcoin config appeared during creation")
		}
	} else if !exists || !sameBitcoinCoreConfigStat(*original, current) {
		return errors.New("bitcoin config changed during update")
	}
	if err := unix.Renameat(directoryFD, temporaryName, directoryFD, bitcoinCoreConfigFile); err != nil {
		return errors.New("commit bitcoin config failed")
	}
	committed = true
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync bitcoin config directory failed")
	}
	return nil
}

func sameBitcoinCoreConfigStat(left unix.Stat_t, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode &&
		left.Uid == right.Uid && left.Gid == right.Gid && left.Size == right.Size &&
		left.Mtim == right.Mtim && left.Ctim == right.Ctim
}
