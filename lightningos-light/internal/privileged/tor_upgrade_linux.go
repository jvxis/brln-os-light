//go:build linux

package privileged

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

//go:embed assets/lnd-onion.py
var lndOnionHelper string

const lndOnionHelperPath = "/usr/local/libexec/lightningos-lnd-onion"
const lndOnionDropIn = "/etc/systemd/system/lnd.service.d/50-lightningos-onion.conf"
const lndOnionDropInContent = "[Service]\nExecStartPre=-+/usr/local/libexec/lightningos-lnd-onion release\n"

// Install before apt can restart Tor. This also covers upgrades from an older
// LOS updater: the new broker carries the helper, independent of installer hooks.
func (manager *NativeTorUpgradeManager) prepareOnionRecovery(ctx context.Context) error {
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		return errors.New("Python 3 is required for LND onion recovery")
	}
	for _, parent := range []string{"/usr/local/libexec", "/etc/systemd/system"} {
		if err := validateRuntimeParent(parent); err != nil {
			return err
		}
	}
	dir := filepath.Dir(lndOnionDropIn)
	if err := os.Mkdir(dir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	if err := validateRuntimeParent(dir); err != nil {
		return err
	}
	if err := installTrustedTorFile(lndOnionHelperPath, lndOnionHelper, 0o755); err != nil {
		return err
	}
	if err := installTrustedTorFile(lndOnionDropIn, lndOnionDropInContent, 0o644); err != nil {
		return err
	}
	if _, err := manager.runner.Run(ctx, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return errors.New("LND onion release hook could not be loaded")
	}
	return nil
}

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
	if !params.VerifyOnly {
		if err := manager.prepareOnionRecovery(ctx); err != nil {
			return TorUpgradeState{}, err
		}
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
	return installTrustedTorFile(torUpgradeHelperPath, content, 0o755)
}

func installTrustedTorFile(path, content string, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if err := validateRuntimeParent(parent); err != nil {
		return err
	}
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
	if err := temporary.Chmod(mode); err != nil {
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
	if err := validateExistingTorUpgradeHelper(path); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
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
