//go:build linux

package privileged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func (manager *NativeLNDManagerCredentialManager) ensure(ctx context.Context, managerUID, managerGID, lndUID, lndGID int, dryRun bool) (LNDManagerCredentialState, error) {
	if err := ctx.Err(); err != nil {
		return LNDManagerCredentialState{}, err
	}
	configuredPath, err := manager.config.LNDMacaroonPath(ctx)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	adminMetadata, adminPresent, err := inspectLNDManagerFile(manager.adminPath, lndUID, lndGID, 0o600, 0o640)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	if !adminPresent {
		return LNDManagerCredentialState{Status: "pending", ConfiguredPath: configuredPath}, nil
	}

	record, recordPresent, err := readLNDManagerMigrationRecord(manager.statePath)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	_, credentialPresent, err := inspectLNDManagerFile(manager.credentialPath, 0, managerGID, 0o640)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	if recordPresent != credentialPresent {
		return LNDManagerCredentialState{}, errors.New("LND manager credential transaction is incomplete")
	}
	if configuredPath == manager.credentialPath && !recordPresent {
		return LNDManagerCredentialState{}, errors.New("configured LND manager credential has no trusted state")
	}
	if configuredPath != manager.adminPath && configuredPath != manager.credentialPath {
		return LNDManagerCredentialState{}, errors.New("configured LND macaroon path is unsupported")
	}

	changed := configuredPath != manager.credentialPath || os.FileMode(adminMetadata.Mode).Perm() != 0o600 || !recordPresent || (recordPresent && record.Phase != "committed")
	if dryRun {
		return LNDManagerCredentialState{
			Status:         "validated",
			Changed:        changed,
			ConfiguredPath: configuredPath,
			AdminProtected: os.FileMode(adminMetadata.Mode).Perm() == 0o600,
		}, nil
	}

	if !recordPresent {
		credential, rootKeyID, bakeErr := manager.rpc.Bake(ctx)
		if bakeErr != nil {
			if rootKeyID != 0 {
				manager.cleanupRootKey(rootKeyID)
			}
			if errors.Is(bakeErr, errInvalidLNDManagerCredential) {
				return LNDManagerCredentialState{}, bakeErr
			}
			return LNDManagerCredentialState{Status: "pending", ConfiguredPath: configuredPath}, nil
		}
		if len(credential) == 0 || len(credential) > maxLNDManagerCredentialBytes {
			manager.cleanupRootKey(rootKeyID)
			return LNDManagerCredentialState{}, errors.New("baked LND manager credential is invalid")
		}
		if err := ensureLNDManagerCredentialDirectories(manager, managerGID); err != nil {
			manager.cleanupRootKey(rootKeyID)
			return LNDManagerCredentialState{}, err
		}
		adminRaw, readErr := readRegularFile(manager.adminPath, maxLNDManagerCredentialBytes)
		if readErr != nil || bytes.Equal(adminRaw, credential) {
			manager.cleanupRootKey(rootKeyID)
			return LNDManagerCredentialState{}, errors.New("baked LND manager credential is unsafe")
		}
		if err := writeLNDManagerOwnedFile(manager.credentialPath, credential, 0, managerGID, 0o640); err != nil {
			manager.cleanupRootKey(rootKeyID)
			return LNDManagerCredentialState{}, errors.New("store LND manager credential failed")
		}
		record = lndManagerMigrationRecord{
			Version:      1,
			Phase:        "prepared",
			RootKeyID:    rootKeyID,
			PreviousPath: manager.adminPath,
			AdminUID:     adminMetadata.Uid,
			AdminGID:     adminMetadata.Gid,
			AdminMode:    uint32(os.FileMode(adminMetadata.Mode).Perm()),
		}
		if err := writeLNDManagerMigrationRecord(manager.statePath, record); err != nil {
			_ = os.Remove(manager.credentialPath)
			manager.cleanupRootKey(rootKeyID)
			return LNDManagerCredentialState{}, err
		}
		recordPresent = true
		credentialPresent = true
		changed = true
	}
	if err := validateLNDManagerMigrationRecord(record, manager, lndUID, lndGID); err != nil {
		return LNDManagerCredentialState{}, err
	}
	if !credentialPresent {
		return LNDManagerCredentialState{}, errors.New("LND manager credential disappeared")
	}
	if err := manager.rpc.Verify(ctx, manager.credentialPath); err != nil {
		return LNDManagerCredentialState{}, errors.New("LND manager credential verification failed")
	}
	if _, err := manager.config.SetLNDMacaroonPath(ctx, manager.credentialPath, false); err != nil {
		return LNDManagerCredentialState{}, err
	}
	if err := setLNDManagerFileMetadata(manager.adminPath, lndUID, lndGID, 0o600); err != nil {
		_, _ = manager.config.SetLNDMacaroonPath(context.Background(), manager.adminPath, false)
		return LNDManagerCredentialState{}, err
	}
	record.Phase = "committed"
	if err := writeLNDManagerMigrationRecord(manager.statePath, record); err != nil {
		return LNDManagerCredentialState{}, err
	}
	return LNDManagerCredentialState{
		Status:         "ready",
		Changed:        changed,
		ConfiguredPath: manager.credentialPath,
		AdminProtected: true,
	}, nil
}

func (manager *NativeLNDManagerCredentialManager) cleanupRootKey(rootKeyID uint64) {
	if rootKeyID == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = manager.rpc.DeleteRootKey(cleanupCtx, rootKeyID)
}

func (manager *NativeLNDManagerCredentialManager) rollback(ctx context.Context, _ int, managerGID, lndUID, lndGID int, dryRun bool) (LNDManagerCredentialState, error) {
	configuredPath, err := manager.config.LNDMacaroonPath(ctx)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	record, recordPresent, err := readLNDManagerMigrationRecord(manager.statePath)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	_, credentialPresent, err := inspectLNDManagerFile(manager.credentialPath, 0, managerGID, 0o640)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	if !recordPresent && !credentialPresent {
		if configuredPath != manager.adminPath {
			return LNDManagerCredentialState{}, errors.New("configured LND manager credential has no rollback state")
		}
		return LNDManagerCredentialState{Status: "absent", ConfiguredPath: configuredPath}, nil
	}
	if !recordPresent || !credentialPresent {
		return LNDManagerCredentialState{}, errors.New("LND manager credential rollback state is incomplete")
	}
	if err := validateLNDManagerMigrationRecord(record, manager, lndUID, lndGID); err != nil {
		return LNDManagerCredentialState{}, err
	}
	if configuredPath != manager.adminPath && configuredPath != manager.credentialPath {
		return LNDManagerCredentialState{}, errors.New("configured LND macaroon path is unsupported")
	}
	if dryRun {
		return LNDManagerCredentialState{Status: "validated", Changed: true, ConfiguredPath: configuredPath}, nil
	}
	if _, err := manager.config.SetLNDMacaroonPath(ctx, manager.adminPath, false); err != nil {
		return LNDManagerCredentialState{}, err
	}
	if err := setLNDManagerFileMetadata(manager.adminPath, int(record.AdminUID), int(record.AdminGID), record.AdminMode); err != nil {
		return LNDManagerCredentialState{}, err
	}
	if err := manager.rpc.DeleteRootKey(ctx, record.RootKeyID); err != nil {
		return LNDManagerCredentialState{}, errors.New("revoke LND manager credential failed")
	}
	if err := os.Remove(manager.credentialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LNDManagerCredentialState{}, errors.New("remove LND manager credential failed")
	}
	if err := os.Remove(manager.statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return LNDManagerCredentialState{}, errors.New("remove LND manager credential state failed")
	}
	return LNDManagerCredentialState{Status: "rolled_back", Changed: true, ConfiguredPath: manager.adminPath}, nil
}

func ensureLNDManagerCredentialDirectories(manager *NativeLNDManagerCredentialManager, managerGID int) error {
	parent := filepath.Dir(manager.credentialRoot)
	if err := validateRuntimeParent(parent); err != nil {
		return errors.New("LND manager credential parent is unsafe")
	}
	for _, path := range []string{manager.credentialRoot, manager.credentialDir} {
		if err := os.Mkdir(path, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return errors.New("create LND manager credential directory failed")
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LND manager credential directory is unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || (stat.Gid != 0 && stat.Gid != uint32(managerGID)) || info.Mode().Perm()&0o027 != 0 {
			return errors.New("LND manager credential directory metadata is unsafe")
		}
		if err := os.Chown(path, 0, managerGID); err != nil || os.Chmod(path, 0o750) != nil {
			return errors.New("set LND manager credential directory metadata failed")
		}
	}
	return nil
}

func inspectLNDManagerFile(path string, uid, gid int, modes ...os.FileMode) (unix.Stat_t, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return unix.Stat_t{}, false, nil
	}
	if err != nil {
		return unix.Stat_t{}, false, errors.New("open LND manager credential file failed")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return unix.Stat_t{}, false, errors.New("LND manager credential file is unsafe")
	}
	if stat.Uid != uint32(uid) || stat.Gid != uint32(gid) || stat.Size < 1 || stat.Size > maxLNDManagerCredentialBytes {
		return unix.Stat_t{}, false, errors.New("LND manager credential metadata is unsafe")
	}
	modeOK := false
	for _, mode := range modes {
		if os.FileMode(stat.Mode).Perm() == mode {
			modeOK = true
			break
		}
	}
	if !modeOK {
		return unix.Stat_t{}, false, errors.New("LND manager credential mode is unsafe")
	}
	return stat, true, nil
}

func setLNDManagerFileMetadata(path string, uid, gid int, mode uint32) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("open LND credential metadata target failed")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return errors.New("LND credential metadata target is unsafe")
	}
	if err := unix.Fchown(fd, uid, gid); err != nil || unix.Fchmod(fd, mode) != nil || unix.Fsync(fd) != nil {
		return errors.New("set LND credential metadata failed")
	}
	return nil
}

func writeLNDManagerOwnedFile(path string, data []byte, uid, gid int, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".manager-credential-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chown(uid, gid); err != nil || temporary.Chmod(mode) != nil {
		return errors.New("set temporary LND credential metadata failed")
	}
	if written, err := temporary.Write(data); err != nil || written != len(data) {
		return errors.New("write temporary LND credential failed")
	}
	if err := temporary.Sync(); err != nil || temporary.Close() != nil {
		return errors.New("sync temporary LND credential failed")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("commit LND credential failed")
	}
	committed = true
	return syncDirectory(filepath.Dir(path))
}

func writeLNDManagerMigrationRecord(path string, record lndManagerMigrationRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return errors.New("encode LND manager credential state failed")
	}
	data = append(data, '\n')
	if err := writeLNDManagerOwnedFile(path, data, 0, 0, 0o600); err != nil {
		return errors.New("store LND manager credential state failed")
	}
	return nil
}

func readLNDManagerMigrationRecord(path string) (lndManagerMigrationRecord, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return lndManagerMigrationRecord{}, false, nil
	}
	if err != nil {
		return lndManagerMigrationRecord{}, false, errors.New("open LND manager credential state failed")
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return lndManagerMigrationRecord{}, false, errors.New("open LND manager credential state failed")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != 0 || stat.Gid != 0 || os.FileMode(stat.Mode).Perm() != 0o600 || stat.Size < 1 || stat.Size > 4096 {
		return lndManagerMigrationRecord{}, false, errors.New("LND manager credential state is unsafe")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var record lndManagerMigrationRecord
	if err := decoder.Decode(&record); err != nil {
		return lndManagerMigrationRecord{}, false, errors.New("LND manager credential state is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return lndManagerMigrationRecord{}, false, errors.New("LND manager credential state has trailing data")
	}
	return record, true, nil
}

func validateLNDManagerMigrationRecord(record lndManagerMigrationRecord, manager *NativeLNDManagerCredentialManager, lndUID, lndGID int) error {
	if record.Version != 1 || (record.Phase != "prepared" && record.Phase != "committed") || record.RootKeyID == 0 || record.PreviousPath != manager.adminPath ||
		record.AdminUID != uint32(lndUID) || record.AdminGID != uint32(lndGID) || (record.AdminMode != 0o600 && record.AdminMode != 0o640) {
		return errors.New("LND manager credential state is invalid")
	}
	return nil
}

func syncDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer unix.Close(fd)
	return unix.Fsync(fd)
}
