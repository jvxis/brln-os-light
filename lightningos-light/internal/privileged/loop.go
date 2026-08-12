package privileged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	groupaddPath       = "/usr/sbin/groupadd"
	useraddPath        = "/usr/sbin/useradd"
	getentPath         = "/usr/bin/getent"
	idPath             = "/usr/bin/id"
	setfaclPath        = "/usr/bin/setfacl"
	loopAptGetPath     = "/usr/bin/apt-get"
	curlPath           = "/usr/bin/curl"
	systemdAnalyzePath = "/usr/bin/systemd-analyze"
)

type NativeLoopManager struct {
	Runner       CommandRunner
	Paths        appmanifest.LoopPaths
	StateRoot    string
	AppsRoot     string
	AppsDataRoot string
	TempRoot     string
	GOARCH       string
}

func NewNativeLoopManager(runner CommandRunner) *NativeLoopManager {
	return &NativeLoopManager{
		Runner:       runner,
		Paths:        appmanifest.DefaultLoopPaths(),
		StateRoot:    appmanifest.LoopStateRoot,
		AppsRoot:     appmanifest.LoopAppsRoot,
		AppsDataRoot: appmanifest.LoopAppsDataRoot,
		GOARCH:       runtime.GOARCH,
	}
}

func (manager *NativeLoopManager) Status(ctx context.Context) (LoopState, error) {
	var state LoopState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("Lightning Loop manager is unavailable")
	}
	state.Installed = safeNonEmptyRegularFile(manager.Paths.LoopdPath)
	state.HasLNDMacaroon = safeNonEmptyRegularFile(manager.Paths.LNDMacaroonPath)
	state.HasPersistentState = loopPersistentState(manager.Paths)
	if !state.Installed {
		state.Status = "stopped"
		return state, nil
	}
	if !safeNonEmptyRegularFile(manager.Paths.ServicePath) {
		state.Status = "stopped"
		return state, nil
	}
	output, err := manager.Runner.Run(ctx, systemctlPath, "show", appmanifest.LoopService,
		"--property=ActiveState", "--property=SubState", "--no-pager")
	state.Status = parseLoopServiceState(output)
	if state.Status == "unknown" && err != nil {
		return state, err
	}
	return state, nil
}

func (manager *NativeLoopManager) Ensure(ctx context.Context, params LoopEnsureParams, dryRun bool) (LoopState, error) {
	var state LoopState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("Lightning Loop manager is unavailable")
	}
	if err := appmanifest.ValidateLoopMaterial(params.LNDTLSCertificate, params.LNDMacaroon); err != nil {
		return state, err
	}
	asset, err := appmanifest.LoopAssetForArch(manager.arch())
	if err != nil {
		return state, err
	}
	if dryRun {
		return LoopState{Status: "validated"}, nil
	}
	if err := manager.ensurePermissions(ctx); err != nil {
		return state, err
	}
	if !safeNonEmptyRegularFile(manager.Paths.LoopdPath) || !safeNonEmptyRegularFile(manager.Paths.LoopCLIPath) || strings.TrimSpace(readFixedFile(manager.Paths.VersionPath)) != appmanifest.LoopVersion {
		if err := manager.installRelease(ctx, asset); err != nil {
			return state, err
		}
	}
	if err := manager.writeOwnedFile(ctx, manager.Paths.LNDTLSCertPath, params.LNDTLSCertificate, 0640, appmanifest.LoopUser+":"+appmanifest.LoopUser); err != nil {
		return state, errors.New("failed to install Lightning Loop LND certificate")
	}
	if len(params.LNDMacaroon) > 0 {
		if err := manager.writeOwnedFile(ctx, manager.Paths.LNDMacaroonPath, params.LNDMacaroon, 0600, appmanifest.LoopUser+":"+appmanifest.LoopUser); err != nil {
			return state, errors.New("failed to install Lightning Loop LND macaroon")
		}
	} else if !safeNonEmptyRegularFile(manager.Paths.LNDMacaroonPath) {
		return state, errors.New("Lightning Loop LND macaroon is unavailable")
	}
	if err := manager.writeOwnedFile(ctx, manager.Paths.ConfigPath, []byte(appmanifest.LoopConfig(manager.Paths)), 0640, appmanifest.LoopUser+":"+appmanifest.LoopUser); err != nil {
		return state, errors.New("failed to install Lightning Loop configuration")
	}
	if err := writeAtomicRegularFile(manager.Paths.ServicePath, []byte(appmanifest.LoopServiceUnit(manager.Paths)), 0644); err != nil {
		return state, errors.New("failed to install Lightning Loop service")
	}
	if _, err := manager.Runner.Run(ctx, systemdAnalyzePath, "verify", manager.Paths.ServicePath); err != nil {
		return state, errors.New("Lightning Loop service validation failed")
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "daemon-reload"); err != nil {
		return state, errors.New("Lightning Loop systemd reload failed")
	}
	return manager.Status(ctx)
}

func (manager *NativeLoopManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (LoopState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return LoopState{}, errors.New("Lightning Loop lifecycle action is not allowed")
	}
	if dryRun {
		return LoopState{Status: "validated"}, nil
	}
	if !safeNonEmptyRegularFile(manager.Paths.LoopdPath) {
		return LoopState{}, errors.New("Lightning Loop is not installed")
	}
	verb := "enable"
	if action == AppLifecycleStop {
		verb = "disable"
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, verb, "--now", appmanifest.LoopService); err != nil {
		return LoopState{}, err
	}
	return manager.Status(ctx)
}

func (manager *NativeLoopManager) Remove(ctx context.Context, dryRun bool) error {
	if dryRun {
		return nil
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.LoopService)
	if err := removeFixedRegularFile(manager.Paths.ServicePath); err != nil {
		return err
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "daemon-reload")
	if err := removeFixedTree(manager.Paths.Root, manager.AppsRoot); err != nil {
		return err
	}
	_, _ = manager.Runner.Run(ctx, setfaclPath, "-x", "u:"+appmanifest.LoopUser, manager.StateRoot, manager.AppsRoot, manager.AppsDataRoot)
	return nil
}

func (manager *NativeLoopManager) EnsurePermissions(ctx context.Context, dryRun bool) error {
	if dryRun {
		return nil
	}
	return manager.ensurePermissions(ctx)
}

func (manager *NativeLoopManager) EnsureClientMaterial(ctx context.Context, dryRun bool) error {
	if dryRun {
		return nil
	}
	certificate, err := readSafeNonEmptyRegularFile(manager.Paths.LoopTLSCert)
	if err != nil {
		return errors.New("Lightning Loop API certificate is unavailable")
	}
	macaroon, err := readSafeNonEmptyRegularFile(manager.Paths.LoopMacaroon)
	if err != nil {
		return errors.New("Lightning Loop API macaroon is unavailable")
	}
	if err := manager.writeOwnedFile(ctx, manager.Paths.ClientTLSCert, certificate, 0640, appmanifest.LoopUser+":"+appmanifest.LoopManagerGroup); err != nil {
		return err
	}
	return manager.writeOwnedFile(ctx, manager.Paths.ClientMacaroon, macaroon, 0640, appmanifest.LoopUser+":"+appmanifest.LoopManagerGroup)
}

func (manager *NativeLoopManager) ensurePermissions(ctx context.Context) error {
	if _, err := manager.Runner.Run(ctx, getentPath, "group", appmanifest.LoopUser); err != nil {
		if _, err := manager.Runner.Run(ctx, groupaddPath, "--system", appmanifest.LoopUser); err != nil {
			return errors.New("failed to create Lightning Loop group")
		}
	}
	if _, err := manager.Runner.Run(ctx, idPath, "-u", appmanifest.LoopUser); err != nil {
		if _, err := manager.Runner.Run(ctx, useraddPath, "--system", "--gid", appmanifest.LoopUser, "--home-dir", manager.Paths.DataDir, "--no-create-home", "--shell", "/usr/sbin/nologin", appmanifest.LoopUser); err != nil {
			return errors.New("failed to create Lightning Loop user")
		}
	}
	if _, err := manager.Runner.Run(ctx, setfaclPath, "--version"); err != nil {
		if _, err := manager.Runner.Run(ctx, loopAptGetPath, "install", "-y", "acl"); err != nil {
			if _, updateErr := manager.Runner.Run(ctx, loopAptGetPath, "update"); updateErr != nil {
				return errors.New("acl package is required by Lightning Loop")
			}
			if _, installErr := manager.Runner.Run(ctx, loopAptGetPath, "install", "-y", "acl"); installErr != nil {
				return errors.New("acl package is required by Lightning Loop")
			}
		}
	}
	for _, dir := range []string{manager.AppsRoot, manager.AppsDataRoot, manager.Paths.Root, manager.Paths.BinDir, manager.Paths.ClientDir, manager.Paths.DataDir, manager.Paths.LNDDir} {
		if err := ensureFixedDirectory(manager.StateRoot, dir, 0750); err != nil {
			return err
		}
	}
	if _, err := manager.Runner.Run(ctx, setfaclPath, "-m", "u:"+appmanifest.LoopUser+":--x", manager.StateRoot, manager.AppsRoot, manager.AppsDataRoot); err != nil {
		return errors.New("failed to grant Lightning Loop traverse access")
	}
	if err := setLoopTreeOwnership([]string{manager.Paths.Root, manager.Paths.DataDir}, appmanifest.LoopUser, appmanifest.LoopUser); err != nil {
		return errors.New("failed to set Lightning Loop ownership")
	}
	if err := setLoopDirectoryOwnership(manager.Paths.Root, appmanifest.LoopUser, appmanifest.LoopManagerGroup); err != nil {
		return errors.New("failed to grant manager traverse access to Lightning Loop")
	}
	if err := setLoopTreeOwnership([]string{manager.Paths.ClientDir}, appmanifest.LoopUser, appmanifest.LoopManagerGroup); err != nil {
		return errors.New("failed to grant manager access to Lightning Loop client material")
	}
	for _, dir := range []string{manager.Paths.Root, manager.Paths.BinDir, manager.Paths.ClientDir, manager.Paths.DataDir, manager.Paths.LNDDir} {
		if err := os.Chmod(dir, 0750|os.ModeSetgid); err != nil {
			return err
		}
	}
	return nil
}

func (manager *NativeLoopManager) installRelease(ctx context.Context, asset appmanifest.LoopReleaseAsset) error {
	tempRoot := manager.TempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	tempDir, err := os.MkdirTemp(tempRoot, "lightningos-loop-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, asset.Archive)
	url := "https://github.com/lightninglabs/loop/releases/download/" + appmanifest.LoopVersion + "/" + asset.Archive
	if _, err := manager.Runner.Run(ctx, curlPath, "--fail", "--location", "--proto", "=https", "--tlsv1.2", "--max-filesize", "268435456", "--output", archivePath, url); err != nil {
		return errors.New("failed to download Lightning Loop release")
	}
	info, err := os.Lstat(archivePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 256*1024*1024 {
		return errors.New("invalid Lightning Loop release archive")
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != asset.SHA256 {
		return errors.New("Lightning Loop release checksum mismatch")
	}
	binaries, err := extractLoopBinaries(raw)
	if err != nil {
		return err
	}
	for path, data := range map[string][]byte{manager.Paths.LoopdPath: binaries["loopd"], manager.Paths.LoopCLIPath: binaries["loop"]} {
		if err := manager.writeOwnedFile(ctx, path, data, 0755, appmanifest.LoopUser+":"+appmanifest.LoopUser); err != nil {
			return err
		}
	}
	return manager.writeOwnedFile(ctx, manager.Paths.VersionPath, []byte(appmanifest.LoopVersion+"\n"), 0640, appmanifest.LoopUser+":"+appmanifest.LoopManagerGroup)
}

func (manager *NativeLoopManager) writeOwnedFile(ctx context.Context, path string, raw []byte, mode os.FileMode, owner string) error {
	if err := validateFixedParent(manager.StateRoot, filepath.Dir(path)); err != nil {
		return err
	}
	if err := writeAtomicRegularFile(path, raw, mode); err != nil {
		return err
	}
	ownerName, groupName, ok := strings.Cut(owner, ":")
	if !ok || ownerName == "" || groupName == "" {
		return errors.New("invalid fixed Lightning Loop ownership")
	}
	if err := setLoopFileOwnership(path, ownerName, groupName); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (manager *NativeLoopManager) arch() string {
	if manager.GOARCH != "" {
		return manager.GOARCH
	}
	return runtime.GOARCH
}

func extractLoopBinaries(raw []byte) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid Lightning Loop release archive")
	}
	defer gzipReader.Close()
	result := make(map[string][]byte)
	tarReader := tar.NewReader(gzipReader)
	var expandedBytes int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errors.New("invalid Lightning Loop release archive")
		}
		if header.Size < 0 || expandedBytes > 256*1024*1024-header.Size {
			return nil, errors.New("Lightning Loop release archive is too large")
		}
		expandedBytes += header.Size
		name := filepath.Base(filepath.ToSlash(header.Name))
		if (name != "loop" && name != "loopd") || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if header.Size <= 0 || header.Size > 128*1024*1024 {
			return nil, errors.New("invalid Lightning Loop binary size")
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, errors.New("invalid Lightning Loop binary")
		}
		result[name] = data
	}
	if len(result["loop"]) == 0 || len(result["loopd"]) == 0 {
		return nil, errors.New("Lightning Loop release binaries are missing")
	}
	return result, nil
}

func ensureFixedDirectory(root, target string, mode os.FileMode) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("Lightning Loop directory is outside its fixed root")
	}
	if err := validateExistingDirectory(root); err != nil {
		return err
	}
	current := root
	if relative != "." {
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			if err := os.Mkdir(current, mode); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			if err := validateExistingDirectory(current); err != nil {
				return err
			}
		}
	}
	return os.Chmod(target, mode)
}

func validateFixedParent(root, target string) error {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("Lightning Loop path is outside its fixed root")
	}
	current := filepath.Clean(root)
	if err := validateExistingDirectory(current); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		if err := validateExistingDirectory(current); err != nil {
			return err
		}
	}
	return nil
}

func validateExistingDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe Lightning Loop directory: %s", path)
	}
	return nil
}

func removeFixedRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe Lightning Loop service path")
	}
	return os.Remove(path)
}

func removeFixedTree(path, parent string) error {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(path))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("unsafe Lightning Loop removal path")
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe Lightning Loop app root")
	}
	return os.RemoveAll(path)
}

func safeNonEmptyRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() > 0
}

func readSafeNonEmptyRegularFile(path string) ([]byte, error) {
	if !safeNonEmptyRegularFile(path) {
		return nil, errors.New("unsafe or empty file")
	}
	return os.ReadFile(path)
}

func readFixedFile(path string) string {
	raw, err := readSafeNonEmptyRegularFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func loopPersistentState(paths appmanifest.LoopPaths) bool {
	for _, path := range []string{paths.LoopDBPath, paths.LoopDBPath + "-wal", paths.LegacyLoopDB} {
		if safeNonEmptyRegularFile(path) {
			return true
		}
	}
	return false
}

func parseLoopServiceState(output string) string {
	values := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if values["ActiveState"] == "active" && values["SubState"] == "running" {
		return "running"
	}
	switch values["ActiveState"] {
	case "inactive", "failed", "activating", "deactivating":
		return "stopped"
	default:
		return "unknown"
	}
}
