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

func (manager *NativeLNDUpgradeManager) start(ctx context.Context, params LNDUpgradeStartParams, dryRun bool) (LNDUpgradeState, error) {
	if !lndUpgradeVersionPattern.MatchString(params.Version) {
		return LNDUpgradeState{}, errors.New("LND upgrade version is invalid")
	}
	digest := sha256.Sum256([]byte(params.HelperContent))
	if hex.EncodeToString(digest[:]) != lndUpgradeHelperSHA256 {
		return LNDUpgradeState{}, errors.New("LND upgrade helper is not trusted")
	}
	if err := validateRuntimeParent(filepath.Dir(lndUpgradeHelperPath)); err != nil {
		return LNDUpgradeState{}, errors.New("LND upgrade helper parent is unsafe")
	}
	if err := validateExistingLNDUpgradeHelper(lndUpgradeHelperPath); err != nil {
		return LNDUpgradeState{}, err
	}

	unit := lndUpgradeUnit
	if params.VerifyOnly {
		unit = lndVerifyUnit
	}
	state := LNDUpgradeState{Status: "validated", Unit: unit, Version: params.Version, VerifyOnly: params.VerifyOnly}
	if dryRun {
		return state, nil
	}
	if err := installLNDUpgradeHelper(params.HelperContent); err != nil {
		return LNDUpgradeState{}, err
	}

	args := []string{
		"--unit", unit,
		"--collect",
		"--quiet",
		"--",
		lndUpgradeHelperPath,
		"--version", params.Version,
	}
	if params.VerifyOnly {
		args = append(args, "--verify-only")
	}
	if _, err := manager.runner.Run(ctx, systemdRunPath, args...); err != nil {
		return LNDUpgradeState{}, errors.New("fixed LND upgrade unit failed to start")
	}
	state.Status = "started"
	return state, nil
}

func validateExistingLNDUpgradeHelper(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("existing LND upgrade helper is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("existing LND upgrade helper is not root-controlled")
	}
	return nil
}

func installLNDUpgradeHelper(content string) error {
	parent := filepath.Dir(lndUpgradeHelperPath)
	temporary, err := os.CreateTemp(parent, ".lightningos-upgrade-lnd-")
	if err != nil {
		return errors.New("create LND upgrade helper failed")
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
		return errors.New("set LND upgrade helper mode failed")
	}
	if err := temporary.Chown(0, 0); err != nil {
		return errors.New("set LND upgrade helper ownership failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write LND upgrade helper failed")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync LND upgrade helper failed")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close LND upgrade helper failed")
	}
	if err := validateExistingLNDUpgradeHelper(lndUpgradeHelperPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, lndUpgradeHelperPath); err != nil {
		return errors.New("commit LND upgrade helper failed")
	}
	committed = true
	directoryFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("open LND upgrade helper directory failed")
	}
	defer unix.Close(directoryFD)
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync LND upgrade helper directory failed")
	}
	return nil
}
