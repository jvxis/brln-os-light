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

func (manager *NativeTorUpgradeManager) start(ctx context.Context, params TorUpgradeStartParams, dryRun bool) (TorUpgradeState, error) {
	digest := sha256.Sum256([]byte(params.HelperContent))
	if hex.EncodeToString(digest[:]) != torUpgradeHelperSHA256 {
		return TorUpgradeState{}, errors.New("Tor upgrade helper is not trusted")
	}
	if err := validateRuntimeParent(filepath.Dir(torUpgradeHelperPath)); err != nil {
		return TorUpgradeState{}, errors.New("Tor upgrade helper parent is unsafe")
	}
	if err := validateExistingTorUpgradeHelper(torUpgradeHelperPath); err != nil {
		return TorUpgradeState{}, err
	}
	unit := torUpgradeUnit
	if params.VerifyOnly {
		unit = torVerifyUnit
	}
	state := TorUpgradeState{Status: "validated", Unit: unit, VerifyOnly: params.VerifyOnly}
	if dryRun {
		return state, nil
	}
	if err := installTorUpgradeHelper(params.HelperContent); err != nil {
		return TorUpgradeState{}, err
	}
	args := []string{"--unit", unit, "--collect", "--quiet", "--", torUpgradeHelperPath}
	if params.VerifyOnly {
		args = append(args, "--verify-only")
	} else {
		args = append(args, "--yes", "--configure-repo", "--restart")
	}
	if _, err := manager.runner.Run(ctx, systemdRunPath, args...); err != nil {
		return TorUpgradeState{}, errors.New("fixed Tor upgrade unit failed to start")
	}
	state.Status = "started"
	return state, nil
}

func validateExistingTorUpgradeHelper(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("existing Tor upgrade helper is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("existing Tor upgrade helper is not root-controlled")
	}
	return nil
}

func installTorUpgradeHelper(content string) error {
	parent := filepath.Dir(torUpgradeHelperPath)
	temporary, err := os.CreateTemp(parent, ".lightningos-check-tor-update-")
	if err != nil {
		return errors.New("create Tor upgrade helper failed")
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
		return errors.New("set Tor upgrade helper mode failed")
	}
	if err := temporary.Chown(0, 0); err != nil {
		return errors.New("set Tor upgrade helper ownership failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write Tor upgrade helper failed")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync Tor upgrade helper failed")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close Tor upgrade helper failed")
	}
	if err := validateExistingTorUpgradeHelper(torUpgradeHelperPath); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, torUpgradeHelperPath); err != nil {
		return errors.New("commit Tor upgrade helper failed")
	}
	committed = true
	directoryFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("open Tor upgrade helper directory failed")
	}
	defer unix.Close(directoryFD)
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync Tor upgrade helper directory failed")
	}
	return nil
}
