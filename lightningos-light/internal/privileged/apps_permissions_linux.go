//go:build linux

package privileged

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"lightningos-light/internal/appmanifest"
)

func prepareLNDgWritableData(dataDir string) error {
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LNDg data entry is unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("LNDg data ownership is unavailable")
		}
		if info.IsDir() {
			if os.Geteuid() == 0 {
				if err := os.Chown(path, appmanifest.LNDgContainerUID, int(stat.Gid)); err != nil {
					return err
				}
			}
			return os.Chmod(path, 0770)
		}
		if !info.Mode().IsRegular() {
			return errors.New("LNDg data entry is not a regular file")
		}
		base := filepath.Base(path)
		if base == "lndg-admin.txt" || base == "lndg-db-password.txt" {
			return os.Chmod(path, 0600)
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, appmanifest.LNDgContainerUID, int(stat.Gid)); err != nil {
				return err
			}
		}
		return os.Chmod(path, 0640)
	})
}

func prepareLNbitsWritableData(dataDir string) error {
	passwdRaw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return errors.New("host account inventory is unavailable")
	}
	groupRaw, err := os.ReadFile("/etc/group")
	if err != nil {
		return errors.New("host group inventory is unavailable")
	}
	if err := validateLNbitsHostIdentityFiles(passwdRaw, groupRaw); err != nil {
		return err
	}
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("LNbits data entry is unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("LNbits data ownership is unavailable")
		}
		if info.IsDir() {
			if os.Geteuid() == 0 {
				if err := os.Chown(path, appmanifest.LNbitsContainerUID, int(stat.Gid)); err != nil {
					return err
				}
			}
			return os.Chmod(path, 0770)
		}
		if !info.Mode().IsRegular() {
			return errors.New("LNbits data entry is not a regular file")
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, appmanifest.LNbitsContainerUID, int(stat.Gid)); err != nil {
				return err
			}
		}
		return os.Chmod(path, 0640)
	})
}

func prepareFedimintWritableData(dataDir string) error {
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Fedimint data entry is unsafe")
		}
		if info.IsDir() {
			if os.Geteuid() == 0 {
				if err := os.Chown(path, appmanifest.FedimintContainerUID, appmanifest.FedimintContainerGID); err != nil {
					return err
				}
			}
			return os.Chmod(path, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("Fedimint data entry is not a regular file")
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, appmanifest.FedimintContainerUID, appmanifest.FedimintContainerGID); err != nil {
				return err
			}
		}
		return os.Chmod(path, 0600)
	})
}

func fedimintWritableDataReady(dataDir string) bool {
	ready := true
	err := filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe Fedimint data")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != uint32(appmanifest.FedimintContainerUID) || stat.Gid != uint32(appmanifest.FedimintContainerGID) {
			ready = false
		}
		if info.IsDir() {
			if info.Mode().Perm() != 0700 {
				ready = false
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsafe Fedimint data")
		}
		if info.Mode().Perm() != 0600 {
			ready = false
		}
		return nil
	})
	return err == nil && ready
}

func setFedimintCredentialOwnership(root string, paths ...string) error {
	if os.Geteuid() == 0 {
		if err := os.Chown(root, 0, appmanifest.FedimintContainerGID); err != nil {
			return err
		}
	}
	if err := os.Chmod(root, 0750); err != nil {
		return err
	}
	for _, path := range paths {
		if err := setFedimintCredentialFileOwnership(path); err != nil {
			return err
		}
	}
	return nil
}

func setFedimintCredentialFileOwnership(path string) error {
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 0, appmanifest.FedimintContainerGID); err != nil {
			return err
		}
	}
	return os.Chmod(path, 0440)
}

func preparePublicPoolWritableData(dataDir string) error {
	passwdRaw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return errors.New("host account inventory is unavailable")
	}
	groupRaw, err := os.ReadFile("/etc/group")
	if err != nil {
		return errors.New("host group inventory is unavailable")
	}
	if identityFileContainsNumericID(passwdRaw, 2, appmanifest.PublicPoolContainerUID) {
		return errors.New("Public Pool container UID collides with a host account")
	}
	if identityFileContainsNumericID(groupRaw, 2, appmanifest.PublicPoolContainerGID) {
		return errors.New("Public Pool container GID collides with a host group")
	}
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Public Pool data entry is unsafe")
		}
		if info.IsDir() {
			if os.Geteuid() == 0 {
				if err := os.Chown(path, appmanifest.PublicPoolContainerUID, appmanifest.PublicPoolContainerGID); err != nil {
					return err
				}
			}
			return os.Chmod(path, 0770)
		}
		if !info.Mode().IsRegular() {
			return errors.New("Public Pool data entry is not a regular file")
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, appmanifest.PublicPoolContainerUID, appmanifest.PublicPoolContainerGID); err != nil {
				return err
			}
		}
		return os.Chmod(path, 0660)
	})
}

func prepareBarkWalletWritableData(walletDir, authDir, passwordPath, sessionPath string) error {
	passwdRaw, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return errors.New("host account inventory is unavailable")
	}
	groupRaw, err := os.ReadFile("/etc/group")
	if err != nil {
		return errors.New("host group inventory is unavailable")
	}
	for _, identity := range []struct {
		uid, gid int
		name     string
	}{
		{appmanifest.BarkWalletAPIUID, appmanifest.BarkWalletAPIGID, "API"},
		{appmanifest.BarkWalletDaemonUID, appmanifest.BarkWalletDaemonGID, "daemon"},
		{appmanifest.BarkWalletProxyUID, appmanifest.BarkWalletProxyGID, "proxy"},
	} {
		if identityFileContainsNumericID(passwdRaw, 2, identity.uid) {
			return errors.New("Bark Wallet " + identity.name + " UID collides with a host account")
		}
		if identityFileContainsNumericID(groupRaw, 2, identity.gid) {
			return errors.New("Bark Wallet " + identity.name + " GID collides with a host group")
		}
	}
	if err := filepath.WalkDir(walletDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Bark Wallet data entry is unsafe")
		}
		if info.IsDir() {
			if os.Geteuid() == 0 {
				if err := os.Chown(path, appmanifest.BarkWalletDaemonUID, appmanifest.BarkWalletDaemonGID); err != nil {
					return err
				}
			}
			return os.Chmod(path, 0700)
		}
		if !info.Mode().IsRegular() {
			return errors.New("Bark Wallet data entry is not a regular file")
		}
		if os.Geteuid() == 0 {
			if err := os.Chown(path, appmanifest.BarkWalletDaemonUID, appmanifest.BarkWalletDaemonGID); err != nil {
				return err
			}
		}
		return os.Chmod(path, 0600)
	}); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(authDir, 0, appmanifest.BarkWalletAPIGID); err != nil {
			return err
		}
	}
	if err := os.Chmod(authDir, 0750); err != nil {
		return err
	}
	for _, path := range []string{passwordPath, sessionPath} {
		if err := setPrivilegedPathGroup(path, appmanifest.BarkWalletAPIGID); err != nil {
			return err
		}
		if err := os.Chmod(path, 0640); err != nil {
			return err
		}
	}
	return nil
}

func validateBarkWalletSnapshotPermissions(paths BarkWalletPaths) error {
	checks := []struct {
		path      string
		mode      os.FileMode
		uid, gid  uint32
		directory bool
	}{
		{paths.SnapshotRoot, 0700, 0, 0, true},
		{paths.TLSDir, 0700, 0, 0, true},
		{paths.ComposePath, 0600, 0, 0, false},
		{paths.CaddyfilePath, 0640, 0, uint32(appmanifest.BarkWalletProxyGID), false},
		{paths.TLSCertificate, 0640, 0, uint32(appmanifest.BarkWalletProxyGID), false},
		{paths.TLSPrivateKey, 0640, 0, uint32(appmanifest.BarkWalletProxyGID), false},
		{paths.AuthDir, 0750, 0, uint32(appmanifest.BarkWalletAPIGID), true},
		{paths.AdminPasswordPath, 0640, 0, uint32(appmanifest.BarkWalletAPIGID), false},
		{paths.SessionSecretPath, 0640, 0, uint32(appmanifest.BarkWalletAPIGID), false},
	}
	for _, check := range checks {
		info, err := os.Lstat(check.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != check.mode || check.directory != info.IsDir() {
			return errors.New("Bark Wallet snapshot permissions are unsafe")
		}
		if !check.directory && !info.Mode().IsRegular() {
			return errors.New("Bark Wallet snapshot entry type is unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != check.uid || stat.Gid != check.gid {
			return errors.New("Bark Wallet snapshot ownership is unsafe")
		}
	}
	return nil
}

func validatePublicPoolSnapshotPermissions(root, compose, env, caddy string) error {
	checks := []struct {
		path      string
		mode      os.FileMode
		uid, gid  uint32
		directory bool
	}{
		{path: root, mode: 0700, uid: 0, gid: 0, directory: true},
		{path: compose, mode: 0600, uid: 0, gid: 0},
		{path: env, mode: 0600, uid: 0, gid: 0},
		{path: caddy, mode: 0640, uid: 0, gid: uint32(appmanifest.PublicPoolContainerGID)},
	}
	for _, check := range checks {
		info, err := os.Lstat(check.path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != check.mode {
			return errors.New("Public Pool snapshot permissions are unsafe")
		}
		if check.directory != info.IsDir() || (!check.directory && !info.Mode().IsRegular()) {
			return errors.New("Public Pool snapshot entry type is unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != check.uid || stat.Gid != check.gid {
			return errors.New("Public Pool snapshot ownership is unsafe")
		}
	}
	return nil
}

func validateLNbitsHostIdentityFiles(passwdRaw, groupRaw []byte) error {
	if identityFileContainsNumericID(passwdRaw, 2, appmanifest.LNbitsContainerUID) {
		return errors.New("LNbits container UID collides with a host account")
	}
	if identityFileContainsNumericID(groupRaw, 2, appmanifest.LNbitsContainerGID) {
		return errors.New("LNbits container GID collides with a host group")
	}
	return nil
}

func identityFileContainsNumericID(raw []byte, field, id int) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) <= field {
			continue
		}
		value, err := strconv.Atoi(fields[field])
		if err == nil && value == id {
			return true
		}
	}
	return false
}

func validateSecretFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("secret file permissions are too broad")
	}
	return nil
}

func legacyFedimintComposeModeReady(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	mode := info.Mode().Perm()
	return mode == 0600 || mode == 0640
}

func legacyManagerFileModeReady(info os.FileInfo, expected os.FileMode) bool {
	return info != nil && info.Mode().Perm() == expected
}

func securePrivilegedPathOwner(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, 0, 0)
}

func setPrivilegedPathGroup(path string, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, 0, gid)
}

func validatePrivilegedPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0600 {
		return errors.New("privileged private file is unsafe")
	}
	if os.Geteuid() == 0 && !privilegedPathOwnedByRoot(info) {
		return errors.New("privileged private file is not root-owned")
	}
	return nil
}

func privilegedPathOwnedByRoot(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && stat.Gid == 0
}

func replaceRegularFilePreservingMetadata(path string, raw []byte) (retErr error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("target file is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("target ownership is unavailable")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if retErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := temporary.Chown(int(stat.Uid), int(stat.Gid)); err != nil {
			return err
		}
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
