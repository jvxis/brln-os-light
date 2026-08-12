package privileged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	elementsStorageDataDirFile = "storage-data-dir"
	elementsMinFreeKiB         = uint64(1024 * 1024)
	runuserPath                = "/usr/sbin/runuser"
)

type NativeElementsManager struct {
	Runner             CommandRunner
	PrivilegedAppsRoot string
	TempRoot           string
	GOARCH             string
	MinFreeKiB         uint64
}

func NewNativeElementsManager(runner CommandRunner) *NativeElementsManager {
	return &NativeElementsManager{Runner: runner, GOARCH: runtime.GOARCH}
}

func (manager *NativeElementsManager) Status(ctx context.Context, dataDir string) (ElementsState, error) {
	if manager == nil || manager.Runner == nil {
		return ElementsState{}, errors.New("Elements command runner is unavailable")
	}
	paths, err := appmanifest.DefaultElementsPaths(dataDir)
	if err != nil {
		return ElementsState{}, err
	}
	state := ElementsState{Installed: safeNonEmptyRegularFile(paths.Elementsd), Status: "stopped", DataDir: paths.DataDir}
	if !state.Installed || !safeNonEmptyRegularFile(paths.Service) {
		return state, nil
	}
	output, runErr := manager.Runner.Run(ctx, systemctlPath, "show", appmanifest.ElementsService,
		"--property=ActiveState", "--property=SubState", "--no-pager")
	state.Status = parseLoopServiceState(output)
	if state.Status == "unknown" && runErr != nil {
		return state, errors.New("Elements service status failed")
	}
	if state.Status != "running" {
		return state, nil
	}
	// A running service must always be queried through the storage path enrolled
	// by the broker. This prevents a caller-selected external mount from being
	// used as an arbitrary Elements configuration/RPC probe.
	paths, err = manager.enrolledPaths(dataDir)
	if err != nil {
		return state, err
	}
	chainRaw, err := manager.elementsCLI(ctx, paths, "getblockchaininfo")
	if err != nil {
		return state, nil
	}
	var chain struct {
		Chain                string  `json:"chain"`
		Blocks               int64   `json:"blocks"`
		Headers              int64   `json:"headers"`
		VerificationProgress float64 `json:"verificationprogress"`
		InitialBlockDownload bool    `json:"initialblockdownload"`
		SizeOnDisk           int64   `json:"size_on_disk"`
	}
	if json.Unmarshal([]byte(chainRaw), &chain) != nil {
		return state, nil
	}
	networkRaw, err := manager.elementsCLI(ctx, paths, "getnetworkinfo")
	if err != nil {
		return state, nil
	}
	var network struct {
		Version     int    `json:"version"`
		Subversion  string `json:"subversion"`
		Connections int    `json:"connections"`
	}
	if json.Unmarshal([]byte(networkRaw), &network) != nil {
		return state, nil
	}
	state.RPCOK = true
	state.Chain, state.Blocks, state.Headers = chain.Chain, chain.Blocks, chain.Headers
	state.VerificationProgress, state.InitialBlockDownload, state.SizeOnDisk = chain.VerificationProgress, chain.InitialBlockDownload, chain.SizeOnDisk
	state.Version, state.Subversion, state.Peers = network.Version, network.Subversion, network.Connections
	return state, nil
}

func (manager *NativeElementsManager) Config(ctx context.Context, dataDir string) (ElementsConfigState, error) {
	paths, err := manager.enrolledPaths(dataDir)
	if err != nil {
		return ElementsConfigState{}, err
	}
	raw, err := readRegularFile(paths.Config, 64*1024)
	if os.IsNotExist(err) {
		return ElementsConfigState{Status: "missing"}, nil
	}
	if err != nil {
		return ElementsConfigState{}, errors.New("Elements configuration read failed")
	}
	return ElementsConfigState{Status: "ready", Content: strings.TrimRight(string(raw), "\n") + "\n"}, nil
}

func (manager *NativeElementsManager) Ensure(ctx context.Context, params ElementsEnsureParams, dryRun bool) (ElementsState, error) {
	paths, err := appmanifest.DefaultElementsPaths(params.DataDir)
	if err != nil || paths.DataDir != params.DataDir {
		return ElementsState{}, errors.New("Elements data directory is not canonical")
	}
	if err := appmanifest.ValidateElementsConfig(params.Content); err != nil {
		return ElementsState{}, err
	}
	if err := manager.validateStorage(paths.DataDir); err != nil {
		return ElementsState{}, err
	}
	if dryRun {
		return ElementsState{Status: "validated", DataDir: paths.DataDir}, nil
	}
	if err := manager.ensureIdentity(ctx); err != nil {
		return ElementsState{}, err
	}
	if err := manager.enrollStorage(paths.DataDir); err != nil {
		return ElementsState{}, err
	}
	for _, dir := range []string{appmanifest.ElementsStateRoot, appmanifest.ElementsAppsRoot, appmanifest.ElementsAppsDataRoot, paths.Root, paths.BinDir} {
		if err := ensureFixedDirectory(appmanifest.ElementsStateRoot, dir, 0750); err != nil {
			return ElementsState{}, err
		}
	}
	if err := setLoopTreeOwnership([]string{paths.Root}, appmanifest.ElementsUser, appmanifest.ElementsManagerGroup); err != nil {
		return ElementsState{}, errors.New("Elements application ownership migration failed")
	}
	for _, dir := range []string{paths.Root, paths.BinDir} {
		if err := os.Chmod(dir, 0750|os.ModeSetgid); err != nil {
			return ElementsState{}, errors.New("Elements application permissions update failed")
		}
	}
	if err := manager.prepareDataDir(ctx, paths.DataDir); err != nil {
		return ElementsState{}, err
	}
	asset, err := appmanifest.ElementsAssetForArch(manager.arch())
	if err != nil {
		return ElementsState{}, err
	}
	if !safeNonEmptyRegularFile(paths.Elementsd) || !safeNonEmptyRegularFile(paths.ElementsCLI) || strings.TrimSpace(readFixedFile(paths.Version)) != appmanifest.ElementsVersion {
		if err := manager.installRelease(ctx, paths, asset); err != nil {
			return ElementsState{}, err
		}
	}
	content := params.Content
	if existing, readErr := readRegularFile(paths.Config, 64*1024); readErr == nil {
		content, err = appmanifest.MergeElementsConfig(string(existing), params.Content)
		if err != nil {
			return ElementsState{}, errors.New("Elements configuration merge failed")
		}
	} else if !os.IsNotExist(readErr) {
		return ElementsState{}, errors.New("Elements configuration read failed")
	}
	if err := manager.writeOwnedFile(paths.Config, []byte(content), 0600, appmanifest.ElementsUser, appmanifest.ElementsUser); err != nil {
		return ElementsState{}, errors.New("Elements configuration install failed")
	}
	if err := writeAtomicRegularFile(paths.Service, []byte(appmanifest.ElementsServiceUnit(paths)), 0644); err != nil {
		return ElementsState{}, errors.New("Elements service install failed")
	}
	if _, err := manager.Runner.Run(ctx, systemdAnalyzePath, "verify", paths.Service); err != nil {
		return ElementsState{}, errors.New("Elements service validation failed")
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, "daemon-reload"); err != nil {
		return ElementsState{}, errors.New("Elements systemd reload failed")
	}
	return manager.Status(ctx, paths.DataDir)
}

func (manager *NativeElementsManager) Lifecycle(ctx context.Context, dataDir string, action AppLifecycleAction, dryRun bool) (ElementsState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return ElementsState{}, errors.New("Elements lifecycle action is not allowed")
	}
	paths, err := manager.enrolledPaths(dataDir)
	if err != nil {
		return ElementsState{}, err
	}
	if dryRun {
		return ElementsState{Status: "validated", DataDir: paths.DataDir}, nil
	}
	if !safeNonEmptyRegularFile(paths.Elementsd) {
		return ElementsState{}, errors.New("Elements is not installed")
	}
	verb := "enable"
	if action == AppLifecycleStop {
		verb = "disable"
	}
	if _, err := manager.Runner.Run(ctx, systemctlPath, verb, "--now", appmanifest.ElementsService); err != nil {
		return ElementsState{}, errors.New("Elements lifecycle failed")
	}
	return manager.Status(ctx, paths.DataDir)
}

func (manager *NativeElementsManager) Remove(ctx context.Context, dataDir string, dryRun bool) error {
	paths, err := appmanifest.DefaultElementsPaths(dataDir)
	if err != nil || paths.DataDir != dataDir {
		return errors.New("Elements data directory is not canonical")
	}
	if err := manager.validateStorage(paths.DataDir); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "disable", "--now", appmanifest.ElementsService)
	if err := removeFixedRegularFile(paths.Service); err != nil {
		return err
	}
	_, _ = manager.Runner.Run(ctx, systemctlPath, "daemon-reload")
	if err := removeFixedTree(paths.Root, appmanifest.ElementsAppsRoot); err != nil {
		return err
	}
	if _, err := os.Lstat(manager.storageRoot()); os.IsNotExist(err) {
		return nil
	}
	return removeFixedTree(manager.storageRoot(), filepath.Dir(manager.storageRoot()))
}

func (manager *NativeElementsManager) ensureIdentity(ctx context.Context) error {
	if _, err := manager.Runner.Run(ctx, getentPath, "group", appmanifest.ElementsUser); err != nil {
		if _, err := manager.Runner.Run(ctx, groupaddPath, "--system", appmanifest.ElementsUser); err != nil {
			return errors.New("failed to create Elements group")
		}
	}
	if _, err := manager.Runner.Run(ctx, idPath, "-u", appmanifest.ElementsUser); err != nil {
		if _, err := manager.Runner.Run(ctx, useraddPath, "--system", "--gid", appmanifest.ElementsUser, "--home-dir", appmanifest.ElementsDefaultDataDir, "--no-create-home", "--shell", "/usr/sbin/nologin", appmanifest.ElementsUser); err != nil {
			return errors.New("failed to create Elements user")
		}
	}
	return nil
}

func (manager *NativeElementsManager) validateStorage(dataDir string) error {
	normalized, err := appmanifest.NormalizeElementsDataDir(dataDir)
	if err != nil || normalized != dataDir {
		return errors.New("Elements storage target is not canonical")
	}
	nearest, err := nearestExistingDirectory(normalized)
	if err != nil || validateExistingDirectoryTree(nearest) != nil {
		return errors.New("Elements storage path is unsafe")
	}
	if normalized != appmanifest.ElementsDefaultDataDir {
		rootDevice, rootErr := storageDeviceID("/")
		targetDevice, targetErr := storageDeviceID(nearest)
		if rootErr != nil || targetErr != nil || rootDevice == targetDevice {
			return errors.New("custom Elements storage must be on a mounted non-root filesystem")
		}
	}
	minimum := manager.MinFreeKiB
	if minimum == 0 {
		minimum = elementsMinFreeKiB
	}
	available, err := storageAvailableKiB(nearest)
	if err != nil || available < minimum {
		return errors.New("Elements storage has insufficient free space")
	}
	return nil
}

func (manager *NativeElementsManager) enrollStorage(dataDir string) error {
	root := manager.storageRoot()
	if err := ensureDirectoryTreeNoSymlink(root, 0700); err != nil || validateRootOwnedDirectory(root, 0700) != nil {
		return errors.New("Elements storage metadata root is unsafe")
	}
	metadata := filepath.Join(root, elementsStorageDataDirFile)
	stored, err := readStorageMetadata(metadata)
	if err == nil {
		if stored != dataDir {
			return errors.New("Elements storage metadata does not match the request")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return errors.New("Elements storage metadata is unavailable")
	}
	if err := writeAtomicRegularFile(metadata, []byte(dataDir+"\n"), 0600); err != nil {
		return errors.New("Elements storage target persistence failed")
	}
	return nil
}

func (manager *NativeElementsManager) enrolledPaths(dataDir string) (appmanifest.ElementsPaths, error) {
	paths, err := appmanifest.DefaultElementsPaths(dataDir)
	if err != nil || paths.DataDir != dataDir {
		return paths, errors.New("Elements data directory is not canonical")
	}
	stored, err := readStorageMetadata(filepath.Join(manager.storageRoot(), elementsStorageDataDirFile))
	if err != nil || stored != paths.DataDir {
		return paths, errors.New("Elements storage target is not enrolled")
	}
	return paths, nil
}

func (manager *NativeElementsManager) prepareDataDir(ctx context.Context, dataDir string) error {
	if err := os.MkdirAll(dataDir, 0750); err != nil || ensureDirectoryTreeNoSymlink(dataDir, 0750) != nil {
		return errors.New("Elements data directory creation failed")
	}
	for parent := filepath.Dir(dataDir); parent != "/" && parent != "."; parent = filepath.Dir(parent) {
		_, _ = manager.Runner.Run(ctx, setfaclPath, "-m", "u:"+appmanifest.ElementsUser+":--x", parent)
	}
	if err := setLoopTreeOwnership([]string{dataDir}, appmanifest.ElementsUser, appmanifest.ElementsUser); err != nil {
		return errors.New("Elements data ownership migration failed")
	}
	return os.Chmod(dataDir, 0750)
}

func (manager *NativeElementsManager) installRelease(ctx context.Context, paths appmanifest.ElementsPaths, asset appmanifest.ElementsReleaseAsset) error {
	tempRoot := manager.TempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	tempDir, err := os.MkdirTemp(tempRoot, "lightningos-elements-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	archivePath := filepath.Join(tempDir, asset.Archive)
	url := "https://github.com/ElementsProject/elements/releases/download/elements-" + appmanifest.ElementsVersion + "/" + asset.Archive
	if _, err := manager.Runner.Run(ctx, curlPath, "--fail", "--location", "--proto", "=https", "--tlsv1.2", "--max-filesize", "536870912", "--output", archivePath, url); err != nil {
		return errors.New("failed to download official Elements release")
	}
	raw, err := os.ReadFile(archivePath)
	if err != nil || len(raw) == 0 || len(raw) > 512*1024*1024 {
		return errors.New("invalid Elements release archive")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != asset.SHA256 {
		return errors.New("Elements release checksum mismatch")
	}
	binaries, err := extractElementsBinaries(raw)
	if err != nil {
		return err
	}
	for target, data := range map[string][]byte{paths.Elementsd: binaries["elementsd"], paths.ElementsCLI: binaries["elements-cli"]} {
		if err := manager.writeOwnedFile(target, data, 0755, appmanifest.ElementsUser, appmanifest.ElementsManagerGroup); err != nil {
			return err
		}
	}
	return manager.writeOwnedFile(paths.Version, []byte(appmanifest.ElementsVersion+"\n"), 0640, appmanifest.ElementsUser, appmanifest.ElementsManagerGroup)
}

func extractElementsBinaries(raw []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("invalid Elements release archive")
	}
	defer gz.Close()
	result := make(map[string][]byte)
	tarReader := tar.NewReader(gz)
	var expanded int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || header.Size < 0 || expanded > 1024*1024*1024-header.Size {
			return nil, errors.New("invalid Elements release archive")
		}
		expanded += header.Size
		name := filepath.Base(filepath.ToSlash(header.Name))
		if (name != "elementsd" && name != "elements-cli") || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		if header.Size <= 0 || header.Size > 512*1024*1024 {
			return nil, errors.New("invalid Elements binary size")
		}
		data, err := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if err != nil || int64(len(data)) != header.Size {
			return nil, errors.New("invalid Elements binary")
		}
		result[name] = data
	}
	if len(result["elementsd"]) == 0 || len(result["elements-cli"]) == 0 {
		return nil, errors.New("Elements release binaries are missing")
	}
	return result, nil
}

func (manager *NativeElementsManager) writeOwnedFile(path string, raw []byte, mode os.FileMode, owner, group string) error {
	if err := writeAtomicRegularFile(path, raw, mode); err != nil {
		return err
	}
	if err := setLoopFileOwnership(path, owner, group); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func (manager *NativeElementsManager) elementsCLI(ctx context.Context, paths appmanifest.ElementsPaths, method string) (string, error) {
	if method != "getblockchaininfo" && method != "getnetworkinfo" {
		return "", errors.New("Elements RPC method is not allowed")
	}
	return manager.Runner.Run(ctx, runuserPath, "-u", appmanifest.ElementsUser, "--", paths.ElementsCLI,
		"-conf="+paths.Config, "-datadir="+paths.DataDir, "-rpcwait", "-rpcwaittimeout=5", method)
}

func (manager *NativeElementsManager) storageRoot() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.ElementsID)
}

func (manager *NativeElementsManager) arch() string {
	if manager.GOARCH != "" {
		return manager.GOARCH
	}
	return runtime.GOARCH
}
