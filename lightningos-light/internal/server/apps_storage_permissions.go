package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"lightningos-light/internal/system"
)

// ensureAppStorageRoots reconciles the App Store's shared directories before
// every install. Besides provisioning fresh nodes, this repairs legacy nodes
// where an older installer or a root-run maintenance command left appsRoot
// owned by root, preventing the unprivileged manager from creating an app.
//
// The broker owns the fixed paths and resolves the manager identity itself;
// no path, uid, gid, mode, executable, or argument reaches it from the caller.
func ensureAppStorageRoots(ctx context.Context) error {
	handled, err := system.EnsureAppStorageWithBroker(ctx)
	if err != nil {
		return fmt.Errorf("failed to prepare app storage permissions: %w", err)
	}
	if !handled {
		return errors.New("app storage preparation requires privileged broker enforce mode")
	}
	// The shared-root reconciliation may update modes on ACL-bearing parents. If
	// Loop is already installed, immediately restore its traverse ACLs and
	// isolated subtree ownership before another app installation continues.
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
