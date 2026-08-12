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
