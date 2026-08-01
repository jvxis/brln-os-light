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
	if err := ensureWritableDirectories(appsRoot, appsDataRoot); err == nil {
		return nil
	}

	uid, gid, err := currentNumericIdentity()
	if err != nil {
		return fmt.Errorf("failed to resolve manager identity: %w", err)
	}
	if _, err := runSystemd(ctx, appStorageInstallArgs(uid, gid)...); err != nil {
		return fmt.Errorf("failed to prepare app storage permissions: %w", err)
	}
	if err := ensureWritableDirectories(appsRoot, appsDataRoot); err != nil {
		return fmt.Errorf("app storage is still not writable after permission repair: %w", err)
	}
	return nil
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

func appStorageInstallArgs(uid, gid int) []string {
	return []string{
		"/usr/bin/install",
		"-d",
		"-m", "0750",
		"-o", strconv.Itoa(uid),
		"-g", strconv.Itoa(gid),
		"--",
		lightningOSStateRoot,
		appsRoot,
		appsDataRoot,
	}
}
