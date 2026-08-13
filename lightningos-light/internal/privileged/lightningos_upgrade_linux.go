//go:build linux

package privileged

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func (manager *NativeLightningOSUpgradeManager) start(ctx context.Context, params LightningOSUpgradeStartParams, dryRun bool) (LightningOSUpgradeState, error) {
	digest := sha256.Sum256([]byte(params.HelperContent))
	if hex.EncodeToString(digest[:]) != lightningOSUpgradeHelperSHA256 {
		return LightningOSUpgradeState{}, errors.New("LightningOS upgrade helper is not trusted")
	}
	if err := validateRuntimeParent(filepath.Dir(lightningOSUpgradeHelperPath)); err != nil {
		return LightningOSUpgradeState{}, errors.New("LightningOS upgrade helper parent is unsafe")
	}
	if err := validateExistingLightningOSUpgradeHelper(lightningOSUpgradeHelperPath); err != nil {
		return LightningOSUpgradeState{}, err
	}
	unit := lightningOSUpgradeUnit
	if params.VerifyOnly {
		unit = lightningOSVerifyUnit
	}
	state := LightningOSUpgradeState{
		Status:     "validated",
		Version:    params.Version,
		Commit:     params.Commit,
		Unit:       unit,
		VerifyOnly: params.VerifyOnly,
	}
	if dryRun {
		return state, nil
	}
	if err := installLightningOSUpgradeHelper(params.HelperContent); err != nil {
		return LightningOSUpgradeState{}, err
	}
	args := []string{
		"--unit", unit,
		"--collect",
		"--quiet",
		"--",
		lightningOSUpgradeHelperPath,
		"--version", params.Version,
		"--tag", params.Tag,
		"--commit", params.Commit,
	}
	if params.VerifyOnly {
		args = append(args, "--verify-only")
	}
	if _, err := manager.runner.Run(ctx, systemdRunPath, args...); err != nil {
		return LightningOSUpgradeState{}, errors.New("fixed LightningOS upgrade unit failed to start")
	}
	state.Status = "started"
	return state, nil
}

func validateExistingLightningOSUpgradeHelper(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("existing LightningOS upgrade helper is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("existing LightningOS upgrade helper is not root-controlled")
	}
	return nil
}

func installLightningOSUpgradeHelper(content string) error {
	parent := filepath.Dir(lightningOSUpgradeHelperPath)
	temporary, err := os.CreateTemp(parent, ".lightningos-upgrade-app-")
	if err != nil {
		return errors.New("create LightningOS upgrade helper failed")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o755); err != nil {
		return errors.New("set LightningOS upgrade helper mode failed")
	}
	if err := temporary.Chown(0, 0); err != nil {
		return errors.New("set LightningOS upgrade helper ownership failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write LightningOS upgrade helper failed")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync LightningOS upgrade helper failed")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close LightningOS upgrade helper failed")
	}
	if err := validateExistingLightningOSUpgradeHelper(lightningOSUpgradeHelperPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, lightningOSUpgradeHelperPath); err != nil {
		return errors.New("commit LightningOS upgrade helper failed")
	}
	committed = true
	directoryFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("open LightningOS upgrade helper directory failed")
	}
	defer unix.Close(directoryFD)
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync LightningOS upgrade helper directory failed")
	}
	return nil
}
