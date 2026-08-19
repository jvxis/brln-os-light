//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"lightningos-light/internal/config"
	"lightningos-light/internal/system"
)

const (
	legacyTransitionSudoersPath = "/etc/sudoers.d/lightningos"
	legacyTransitionBrokerPath  = "/usr/local/libexec/lightningos-privileged"
	legacyTransitionSocketPath  = "/run/lightningos-privileged/broker.sock"
	legacyTransitionStatePath   = "/var/lib/lightningos/rollback/0.5.3-privilege-cutover"
	legacyTransitionStagingDir  = "/var/lib/lightningos/upgrade-staging"
	legacyTransitionSudoPath    = "/usr/bin/sudo"
	legacyTransitionRunPath     = "/usr/bin/systemd-run"
)

func legacyPrivilegeTransitionCandidate(cfg *config.Config, currentVersion string) bool {
	builtVersion := legacyTransitionBuildVersion()
	if cfg == nil || cfg.Privileged.Mode != "enforce" || builtVersion == "" || normalizeAppVersion(currentVersion) != builtVersion {
		return false
	}
	currentUser, err := user.Current()
	if err != nil || currentUser.Username != "lightningos" {
		return false
	}
	if !rootRegularFileWithMode(legacyTransitionSudoersPath, 0440) {
		return false
	}
	for _, path := range []string{legacyTransitionBrokerPath, legacyTransitionSocketPath, legacyTransitionStatePath} {
		if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
			return false
		}
	}
	for _, path := range []string{legacyTransitionSudoPath, legacyTransitionRunPath, "/bin/bash", "/usr/bin/install", "/usr/bin/sha256sum", "/usr/bin/stat"} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return false
		}
	}
	return true
}

func rootRegularFileWithMode(path string, mode os.FileMode) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0 && stat.Gid == 0
}

func startLegacyPrivilegeTransition(ctx context.Context, cfg *config.Config, info appReleaseInfo, helperContent string) (legacyTransitionState, error) {
	if !legacyPrivilegeTransitionCandidate(cfg, currentAppVersion(cfg.UI.StaticDir)) {
		return legacyTransitionNotApplicable, nil
	}
	if err := validateLegacyTransitionRelease(info, currentAppVersion(cfg.UI.StaticDir)); err != nil {
		return legacyTransitionPending, err
	}
	if appUpgradeRunning(ctx) {
		return legacyTransitionPending, errors.New("the release upgrade is still finishing")
	}

	digest, err := legacyTransitionHelperDigest(helperContent)
	if err != nil {
		return legacyTransitionPending, err
	}
	sourcePath, err := stageLegacyTransitionHelper(helperContent, digest)
	if err != nil {
		return legacyTransitionPending, err
	}
	rootCommand, err := buildLegacyTransitionRootCommand(info, sourcePath, digest, uint32(os.Getuid()), uint32(os.Getgid()))
	if err != nil {
		_ = os.Remove(sourcePath)
		return legacyTransitionPending, err
	}

	// This is deliberately the only post-Phase-4 legacy privilege invocation.
	// It is reachable solely after the recognized 0.5.2 updater has installed
	// the exact authenticated Manager build while its reviewed wildcard sudoers still exists.
	// The root helper independently authenticates the release, captures rollback
	// state, installs the typed broker, and removes that sudoers file.
	cmd := exec.CommandContext(ctx,
		legacyTransitionSudoPath, "-n", legacyTransitionRunPath,
		"--unit", legacyTransitionUnitName,
		"--collect", "--quiet", "--property=Type=exec", "--",
		"/bin/bash", "-c", rootCommand,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(sourcePath)
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return legacyTransitionPending, fmt.Errorf("legacy privilege transition launch failed: %s", detail)
		}
		return legacyTransitionPending, fmt.Errorf("legacy privilege transition launch failed: %w", err)
	}
	return legacyTransitionStarted, nil
}

func legacyTransitionUnitRunning(ctx context.Context) bool {
	out, _ := system.RunCommand(ctx, "systemctl", "is-active", legacyTransitionUnitName)
	state := strings.TrimSpace(out)
	return state == "active" || state == "activating"
}

func stageLegacyTransitionHelper(content, digest string) (string, error) {
	parent, err := os.Lstat(filepath.Dir(legacyTransitionStagingDir))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("legacy privilege transition state root is unavailable")
	}
	parentStat, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || parentStat.Uid != uint32(os.Getuid()) || parent.Mode().Perm()&0022 != 0 {
		return "", errors.New("legacy privilege transition state root ownership is invalid")
	}
	if err := os.Mkdir(legacyTransitionStagingDir, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	dirInfo, err := os.Lstat(legacyTransitionStagingDir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 || dirInfo.Mode().Perm() != 0700 {
		return "", errors.New("legacy privilege transition staging directory is unsafe")
	}
	dirStat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if !ok || dirStat.Uid != uint32(os.Getuid()) || dirStat.Gid != uint32(os.Getgid()) {
		return "", errors.New("legacy privilege transition staging ownership is invalid")
	}

	name := "upgrade-app-" + digest[:16] + ".sh"
	path := filepath.Join(legacyTransitionStagingDir, name)
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != content || !ownedRegularFileWithMode(path, uint32(os.Getuid()), uint32(os.Getgid()), 0700) {
			return "", errors.New("legacy privilege transition staged helper is unsafe")
		}
		return path, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmp, err := os.CreateTemp(legacyTransitionStagingDir, ".upgrade-app-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0700); err != nil {
		return "", err
	}
	if _, err := tmp.WriteString(content); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	committed = true
	return path, nil
}

func ownedRegularFileWithMode(path string, uid, gid uint32, mode os.FileMode) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uid && stat.Gid == gid
}
