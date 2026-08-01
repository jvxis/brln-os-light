package server

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
)

const lightningOSStateRoot = "/var/lib/lightningos"

// ensureAppStorageRoots reconciles the App Store's shared directories before
// every install. Besides provisioning fresh nodes, this repairs legacy nodes
// where an older installer or a root-run maintenance command left appsRoot
// owned by root, preventing the unprivileged manager from creating an app.
//
// The transient root command is deliberately restricted to fixed paths and to
// the manager process's own numeric uid/gid; no request data reaches it.
func ensureAppStorageRoots(ctx context.Context) error {
	repairPaths := appStoragePathsNeedingRepair(lightningOSStateRoot, appsRoot, appsDataRoot)
	if len(repairPaths) == 0 {
		return nil
	}

	uid, gid, err := currentNumericIdentity()
	if err != nil {
		return fmt.Errorf("failed to resolve manager identity: %w", err)
	}
	if _, err := runSystemd(ctx, appStorageInstallArgs(uid, gid, repairPaths...)...); err != nil {
		return fmt.Errorf("failed to prepare app storage permissions: %w", err)
	}
	// install -d may update modes on ACL-bearing shared parents. If Loop is
	// already installed, immediately restore its traverse ACLs and isolated
	// subtree ownership before another app installation continues.
	loopPaths := loopAppPaths()
	if fileExists(loopPaths.LoopdPath) {
		if err := ensureLoopDirectories(ctx, loopPaths); err != nil {
			return fmt.Errorf("failed to restore Lightning Loop permissions: %w", err)
		}
	}
	if err := ensureWritableDirectories(appsRoot, appsDataRoot); err != nil {
		return fmt.Errorf("app storage is still not writable after permission repair: %w", err)
	}
	return nil
}

func appStoragePathsNeedingRepair(paths ...string) []string {
	repair := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := ensureWritableDirectories(path); err != nil {
			repair = append(repair, path)
		}
	}
	return repair
}

func ensureWritableDirectories(paths ...string) error {
	for _, path := range paths {
		if err := os.MkdirAll(path, 0750); err != nil {
			return err
		}
		probe, err := os.MkdirTemp(path, ".lightningos-write-check-")
		if err != nil {
			return err
		}
		if err := os.Remove(probe); err != nil {
			return err
		}
	}
	return nil
}

func currentNumericIdentity() (int, int, error) {
	current, err := user.Current()
	if err != nil {
		return 0, 0, err
	}
	uid, err := strconv.Atoi(current.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid uid %q", current.Uid)
	}
	gid, err := strconv.Atoi(current.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid gid %q", current.Gid)
	}
	return uid, gid, nil
}

func appStorageInstallArgs(uid, gid int, paths ...string) []string {
	args := []string{
		"/usr/bin/install",
		"-d",
		"-m", "0750",
		"-o", strconv.Itoa(uid),
		"-g", strconv.Itoa(gid),
		"--",
	}
	return append(args, paths...)
}
