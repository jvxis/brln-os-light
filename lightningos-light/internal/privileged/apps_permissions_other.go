//go:build !linux

package privileged

import (
	"errors"
	"os"
)

func prepareLNDgWritableData(string) error {
	return nil
}

func prepareLNbitsWritableData(string) error {
	return nil
}

func validateSecretFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("secret file is invalid")
	}
	return nil
}

func securePrivilegedPathOwner(string) error {
	return nil
}

func setPrivilegedPathGroup(string, int) error {
	return nil
}

func validatePrivilegedPrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("privileged private file is unsafe")
	}
	return nil
}

func privilegedPathOwnedByRoot(os.FileInfo) bool {
	return true
}

func replaceRegularFilePreservingMetadata(path string, raw []byte) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("target file is unsafe")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
