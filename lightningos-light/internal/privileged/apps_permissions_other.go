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

func prepareFedimintWritableData(string) error { return nil }

func fedimintWritableDataReady(string) bool { return true }

func setFedimintCredentialOwnership(string, ...string) error { return nil }

func setFedimintCredentialFileOwnership(string) error { return nil }

func preparePublicPoolWritableData(string) error { return nil }

func validatePublicPoolSnapshotPermissions(string, string, string, string) error { return nil }

func prepareBarkWalletWritableData(string, string, string, string) error { return nil }

func validateBarkWalletSnapshotPermissions(paths BarkWalletPaths) error {
	for _, path := range []string{paths.SnapshotRoot, paths.TLSDir, paths.ComposePath, paths.CaddyfilePath,
		paths.TLSCertificate, paths.TLSPrivateKey, paths.AuthDir, paths.AdminPasswordPath, paths.SessionSecretPath} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Bark Wallet snapshot entry is unsafe")
		}
	}
	return nil
}

func validateSecretFileMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("secret file is invalid")
	}
	return nil
}

func legacyFedimintComposeModeReady(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular()
}

func legacyManagerFileModeReady(info os.FileInfo, _ os.FileMode) bool {
	return info != nil && info.Mode().IsRegular()
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
