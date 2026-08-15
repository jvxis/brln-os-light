package privileged

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	electrsImageAttestationFile = "image-attestation"
	electrsImageFailureFile     = "image-failure"
	maxElectrsCookieBytes       = 2048
	maxElectrsRPCResponseBytes  = 1024 * 1024
)

var errElectrsBitcoinRPCAuthentication = errors.New("Bitcoin RPC authentication failed")

type electrsImageAttestation struct {
	ImageID      string
	Release      string
	TagObject    string
	Commit       string
	SourceSHA256 string
	BaseImage    string
}

type electrsImageIdentity struct {
	Release      string
	TagObject    string
	Commit       string
	SourceSHA256 string
	BaseImage    string
}

type electrsValidatedFiles struct {
	composeRaw []byte
	envRaw     []byte
	cookieRaw  []byte
	runtime    appmanifest.ElectrsRuntime
}

type electrsRPCProbeFunc func(context.Context, string, string, string, string) error

type electrsBlockchainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	Pruned               bool    `json:"pruned"`
}

type electrsIndexInfo struct {
	Synced          bool  `json:"synced"`
	BestBlockHeight int64 `json:"best_block_height"`
}

func (manager *ComposeAppManager) validatedElectrsFiles() (electrsValidatedFiles, error) {
	var files electrsValidatedFiles
	appsRoot := manager.AppsRoot
	if appsRoot == "" {
		appsRoot = defaultAppsRoot
	}
	appRoot := filepath.Join(appsRoot, appmanifest.ElectrsID)
	if err := validateSnapshotDirectoryEntries(appRoot, map[string]bool{
		appmanifest.ElectrsComposeFile: true,
		appmanifest.ElectrsEnvFile:     true,
		appmanifest.ElectrsCookieFile:  true,
	}); err != nil {
		return files, errors.New("Electrs app declaration contains unexpected assets")
	}
	envPath := filepath.Join(appRoot, appmanifest.ElectrsEnvFile)
	envRaw, err := readRegularFile(envPath, 256)
	if err != nil {
		return files, errors.New("Electrs environment is unavailable")
	}
	runtime, err := appmanifest.ParseElectrsRuntimeEnv(envRaw)
	if err != nil {
		return files, errors.New("Electrs environment does not match the catalog")
	}
	composeRaw, err := readRegularFile(filepath.Join(appRoot, appmanifest.ElectrsComposeFile), 64*1024)
	if err != nil {
		return files, errors.New("Electrs compose manifest is unavailable")
	}
	expectedCompose, err := appmanifest.ElectrsCompose(runtime)
	if err != nil || !bytes.Equal(composeRaw, []byte(expectedCompose)) {
		return files, errors.New("Electrs compose manifest does not match the catalog")
	}
	cookiePath := filepath.Join(appRoot, appmanifest.ElectrsCookieFile)
	if err := validateSecretFileMode(cookiePath); err != nil {
		return files, errors.New("Electrs Bitcoin credential is not private")
	}
	cookieRaw, err := readRegularFile(cookiePath, maxElectrsCookieBytes)
	if err != nil {
		return files, errors.New("Electrs Bitcoin credential is unavailable")
	}
	if _, _, err := parseElectrsCookie(cookieRaw); err != nil {
		return files, err
	}
	files = electrsValidatedFiles{composeRaw: composeRaw, envRaw: envRaw, cookieRaw: cookieRaw, runtime: runtime}
	return files, nil
}

func parseElectrsCookie(raw []byte) (string, string, error) {
	if len(raw) < 3 || len(raw) > maxElectrsCookieBytes || bytes.ContainsAny(raw, "\r\n\x00") {
		return "", "", errors.New("Electrs Bitcoin credential is invalid")
	}
	user, password, ok := strings.Cut(string(raw), ":")
	if !ok || user == "" || password == "" {
		return "", "", errors.New("Electrs Bitcoin credential is invalid")
	}
	for _, value := range []string{user, password} {
		for _, char := range []byte(value) {
			if char < 0x21 || char > 0x7e {
				return "", "", errors.New("Electrs Bitcoin credential is invalid")
			}
		}
	}
	return user, password, nil
}

func (manager *ComposeAppManager) createElectrsSnapshot(files electrsValidatedFiles) (composeAppSnapshot, func(), error) {
	var snapshot composeAppSnapshot
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	if err := ensureDirectoryTreeNoSymlink(privilegedAppsRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure privileged app root")
	}
	snapshotRoot := filepath.Join(privilegedAppsRoot, appmanifest.ElectrsID)
	if err := ensureDirectoryTreeNoSymlink(snapshotRoot, 0700); err != nil {
		return snapshot, func() {}, errors.New("failed to secure Electrs execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(snapshotRoot, map[string]bool{
		appmanifest.ElectrsComposeFile: true,
		appmanifest.ElectrsEnvFile:     true,
		appmanifest.ElectrsCookieFile:  true,
		electrsImageAttestationFile:    true,
		electrsImageFailureFile:        true,
		catalogStorageDataDirFile:      true,
		catalogStorageIDFile:           true,
		catalogStorageMigrationFile:    true,
	}); err != nil {
		return snapshot, func() {}, errors.New("Electrs execution snapshot contains unexpected assets")
	}
	snapshot = composeAppSnapshot{
		root:        snapshotRoot,
		composePath: filepath.Join(snapshotRoot, appmanifest.ElectrsComposeFile),
		envPath:     filepath.Join(snapshotRoot, appmanifest.ElectrsEnvFile),
	}
	if files.runtime.DataDir != "" {
		if err := manager.validateCatalogStorageEnrollment(appmanifest.ElectrsID, files.runtime.DataDir); err != nil {
			return composeAppSnapshot{}, func() {}, err
		}
	}
	compose, err := appmanifest.ElectrsCompose(files.runtime)
	if err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to generate Electrs execution manifest")
	}
	if err := writeAtomicRegularFile(snapshot.composePath, []byte(compose), 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Electrs compose manifest")
	}
	if err := writeAtomicRegularFile(snapshot.envPath, files.envRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Electrs environment")
	}
	cookiePath := filepath.Join(snapshotRoot, appmanifest.ElectrsCookieFile)
	if err := writeAtomicRegularFile(cookiePath, files.cookieRaw, 0600); err != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to snapshot Electrs Bitcoin credential")
	}
	if err := setPrivilegedPathGroup(cookiePath, appmanifest.ElectrsContainerGID); err != nil || os.Chmod(cookiePath, 0640) != nil {
		return composeAppSnapshot{}, func() {}, errors.New("failed to secure Electrs Bitcoin credential")
	}
	return snapshot, func() {}, nil
}

func (manager *ComposeAppManager) removeElectrsExecutionSnapshot(snapshotRoot string) error {
	privilegedAppsRoot := manager.PrivilegedAppsRoot
	if privilegedAppsRoot == "" {
		privilegedAppsRoot = defaultPrivilegedAppsRoot
	}
	expectedRoot := filepath.Join(filepath.Clean(privilegedAppsRoot), appmanifest.ElectrsID)
	if filepath.Clean(snapshotRoot) != expectedRoot {
		return errors.New("invalid Electrs execution snapshot")
	}
	if err := validateRegularDirectory(expectedRoot); err != nil {
		return errors.New("invalid Electrs execution snapshot")
	}
	if err := validateSnapshotDirectoryEntries(expectedRoot, map[string]bool{
		appmanifest.ElectrsComposeFile: true,
		appmanifest.ElectrsEnvFile:     true,
		appmanifest.ElectrsCookieFile:  true,
		electrsImageAttestationFile:    true,
		electrsImageFailureFile:        true,
		catalogStorageDataDirFile:      true,
		catalogStorageIDFile:           true,
		catalogStorageMigrationFile:    true,
	}); err != nil {
		return errors.New("Electrs execution snapshot contains unexpected assets")
	}
	for _, name := range []string{appmanifest.ElectrsComposeFile, appmanifest.ElectrsEnvFile, appmanifest.ElectrsCookieFile} {
		path := filepath.Join(expectedRoot, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("invalid Electrs execution asset")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (manager *ComposeAppManager) validateElectrsBitcoin(ctx context.Context, runtime appmanifest.ElectrsRuntime, cookieRaw []byte) error {
	user, password, err := parseElectrsCookie(cookieRaw)
	if err != nil {
		return err
	}
	address, err := appmanifest.ElectrsProbeAddress(runtime)
	if err != nil {
		return errors.New("Electrs Bitcoin probe address is invalid")
	}
	network, err := appmanifest.ElectrsNetworkForName(runtime.Network)
	if err != nil {
		return errors.New("Electrs Bitcoin network is invalid")
	}
	probe := manager.ElectrsRPCProbe
	if probe == nil {
		probe = probeElectrsBitcoinRPC
	}
	return probe(ctx, "http://"+address+"/", user, password, network.BitcoinName)
}

func probeElectrsBitcoinRPC(ctx context.Context, endpoint, user, password, expectedChain string) error {
	client := &http.Client{Timeout: 10 * time.Second}
	var chain electrsBlockchainInfo
	if err := callElectrsBitcoinRPC(ctx, client, endpoint, user, password, "getblockchaininfo", nil, &chain); err != nil {
		return err
	}
	if chain.Chain != expectedChain {
		return errors.New("Bitcoin chain does not match the Electrs catalog")
	}
	if chain.Pruned {
		return errors.New("Electrs requires an unpruned Bitcoin Full Node")
	}
	if chain.Blocks < 0 || chain.Headers < 0 || chain.VerificationProgress < 0.999 || chain.VerificationProgress > 1 || chain.InitialBlockDownload || chain.Blocks < chain.Headers {
		return errors.New("Electrs requires a fully synchronized Bitcoin Full Node")
	}
	var indexes map[string]electrsIndexInfo
	if err := callElectrsBitcoinRPC(ctx, client, endpoint, user, password, "getindexinfo", []string{"txindex"}, &indexes); err != nil {
		return err
	}
	txindex, ok := indexes["txindex"]
	if !ok {
		return errors.New("Electrs requires Bitcoin txindex=1")
	}
	if !txindex.Synced || txindex.BestBlockHeight < chain.Blocks {
		return errors.New("Electrs requires a fully synchronized Bitcoin txindex")
	}
	return nil
}

func callElectrsBitcoinRPC(ctx context.Context, client *http.Client, endpoint, user, password, method string, params any, target any) error {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "1.0", "id": "electrs-gate", "method": method, "params": params})
	if err != nil {
		return errors.New("Bitcoin RPC request is invalid")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.New("Bitcoin RPC endpoint is invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(user, password)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("Bitcoin RPC is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxElectrsRPCResponseBytes))
		return errElectrsBitcoinRPCAuthentication
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxElectrsRPCResponseBytes))
		return errors.New("Bitcoin RPC returned an invalid status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxElectrsRPCResponseBytes+1))
	if err != nil || len(body) > maxElectrsRPCResponseBytes {
		return errors.New("Bitcoin RPC response is invalid")
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Result) == 0 || (len(envelope.Error) > 0 && string(envelope.Error) != "null") {
		return errors.New("Bitcoin RPC request failed")
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return errors.New("Bitcoin RPC response is invalid")
	}
	return nil
}

func (manager *ComposeAppManager) prepareElectrsImage(ctx context.Context, unit string) (AppImageState, error) {
	state, err := manager.electrsImageStatus(ctx, unit)
	if err != nil || state.Status == "ready" || state.Status == "preparing" {
		return state, err
	}
	attestationPath, err := manager.ensureElectrsImageRoot()
	if err != nil {
		return AppImageState{}, err
	}
	if err := manager.clearElectrsImageFailure(); err != nil {
		return AppImageState{}, err
	}
	args := []string{
		"--quiet",
		"--collect",
		"--unit=" + unit,
		"--property=Type=exec",
		"--property=RuntimeMaxSec=60min",
		"/bin/sh",
		"-c",
		electrsImageBuildScript(attestationPath),
	}
	if _, err := manager.Runner.Run(ctx, systemdRunPath, args...); err != nil {
		return AppImageState{}, errors.New("Electrs image preparation could not be scheduled")
	}
	return AppImageState{Status: "preparing"}, nil
}

func (manager *ComposeAppManager) electrsImageStatus(ctx context.Context, unit string) (AppImageState, error) {
	attestationPath := manager.electrsImageAttestationPath()
	if attestation, err := readElectrsImageAttestation(attestationPath); err == nil {
		if attestation.Release == appmanifest.ElectrsRelease &&
			attestation.TagObject == appmanifest.ElectrsTagObject &&
			attestation.Commit == appmanifest.ElectrsSourceCommit &&
			attestation.SourceSHA256 == appmanifest.ElectrsSourceSHA256 &&
			attestation.BaseImage == appmanifest.ElectrsBaseImage {
			output, inspectErr := manager.Runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.ElectrsImage)
			if inspectErr == nil && strings.TrimSpace(output) == attestation.ImageID {
				return AppImageState{Status: "ready"}, nil
			}
		}
	}
	failurePath := manager.electrsImageFailurePath()
	if _, statErr := os.Lstat(failurePath); statErr == nil {
		identity, readErr := readElectrsImageIdentity(failurePath)
		if readErr != nil {
			return AppImageState{}, errors.New("Electrs image failure marker is invalid")
		}
		if identity.matchesCatalog() {
			return AppImageState{Status: "failed"}, nil
		}
	} else if !os.IsNotExist(statErr) {
		return AppImageState{}, errors.New("Electrs image failure marker is unavailable")
	}
	output, showErr := manager.Runner.Run(ctx, systemctlPath, "show",
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager", unit)
	values := parseSystemdProperties(output)
	if values["LoadState"] == "not-found" || (showErr != nil && values["LoadState"] == "") {
		return AppImageState{Status: "absent"}, nil
	}
	switch values["ActiveState"] {
	case "active", "activating", "reloading":
		return AppImageState{Status: "preparing"}, nil
	case "failed", "inactive", "deactivating":
		return AppImageState{Status: "failed"}, nil
	default:
		return AppImageState{}, errors.New("Electrs image preparation state is invalid")
	}
}

func (manager *ComposeAppManager) electrsImageAttestationPath() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.ElectrsID, electrsImageAttestationFile)
}

func (manager *ComposeAppManager) electrsImageFailurePath() string {
	return filepath.Join(filepath.Dir(manager.electrsImageAttestationPath()), electrsImageFailureFile)
}

func (manager *ComposeAppManager) ensureElectrsImageRoot() (string, error) {
	attestationPath := manager.electrsImageAttestationPath()
	if err := ensureDirectoryTreeNoSymlink(filepath.Dir(attestationPath), 0700); err != nil {
		return "", errors.New("failed to secure Electrs image root")
	}
	return attestationPath, nil
}

func readElectrsImageAttestation(path string) (electrsImageAttestation, error) {
	var attestation electrsImageAttestation
	if err := validatePrivilegedPrivateFile(path); err != nil {
		return attestation, err
	}
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return attestation, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return attestation, errors.New("invalid Electrs image attestation")
		}
		if _, exists := values[key]; exists {
			return attestation, errors.New("duplicate Electrs image attestation field")
		}
		values[key] = value
	}
	if len(values) != 6 || !dockerImageIDPattern.MatchString(values["image_id"]) {
		return attestation, errors.New("invalid Electrs image attestation fields")
	}
	attestation = electrsImageAttestation{
		ImageID:      values["image_id"],
		Release:      values["release"],
		TagObject:    values["tag_object"],
		Commit:       values["commit"],
		SourceSHA256: values["source_sha256"],
		BaseImage:    values["base_image"],
	}
	if attestation.Release == "" || attestation.TagObject == "" || attestation.Commit == "" || attestation.SourceSHA256 == "" || attestation.BaseImage == "" {
		return electrsImageAttestation{}, errors.New("incomplete Electrs image attestation")
	}
	return attestation, nil
}

func readElectrsImageIdentity(path string) (electrsImageIdentity, error) {
	var identity electrsImageIdentity
	if err := validatePrivilegedPrivateFile(path); err != nil {
		return identity, err
	}
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return identity, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return identity, errors.New("invalid Electrs image identity")
		}
		if _, exists := values[key]; exists {
			return identity, errors.New("duplicate Electrs image identity field")
		}
		values[key] = value
	}
	if len(values) != 5 {
		return identity, errors.New("invalid Electrs image identity fields")
	}
	identity = electrsImageIdentity{
		Release:      values["release"],
		TagObject:    values["tag_object"],
		Commit:       values["commit"],
		SourceSHA256: values["source_sha256"],
		BaseImage:    values["base_image"],
	}
	if identity.Release == "" || identity.TagObject == "" || identity.Commit == "" || identity.SourceSHA256 == "" || identity.BaseImage == "" {
		return electrsImageIdentity{}, errors.New("incomplete Electrs image identity")
	}
	return identity, nil
}

func (identity electrsImageIdentity) matchesCatalog() bool {
	return identity.Release == appmanifest.ElectrsRelease &&
		identity.TagObject == appmanifest.ElectrsTagObject &&
		identity.Commit == appmanifest.ElectrsSourceCommit &&
		identity.SourceSHA256 == appmanifest.ElectrsSourceSHA256 &&
		identity.BaseImage == appmanifest.ElectrsBaseImage
}

func (manager *ComposeAppManager) clearElectrsImageFailure() error {
	path := manager.electrsImageFailurePath()
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return errors.New("Electrs image failure marker is unavailable")
	}
	if _, err := readElectrsImageIdentity(path); err != nil {
		return errors.New("Electrs image failure marker is invalid")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("failed to clear Electrs image failure marker")
	}
	return nil
}

func electrsImageBuildScript(attestationPath string) string {
	dockerfile := base64.StdEncoding.EncodeToString([]byte(appmanifest.ElectrsDockerfile()))
	appRoot := filepath.Dir(attestationPath)
	buildRoot := filepath.Dir(appRoot)
	failurePath := filepath.Join(appRoot, electrsImageFailureFile)
	return fmt.Sprintf(`set -eu
umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
build_root=%s
attestation=%s
failure_marker=%s
work="$(/usr/bin/mktemp -d "$build_root/electrs-image.XXXXXX")"
cleanup() {
  rc=$?
  case "$work" in
    "$build_root"/electrs-image.*) ;;
    *) exit 99 ;;
  esac
  if [ "$rc" -ne 0 ]; then
    printf 'release=%s\ntag_object=%s\ncommit=%s\nsource_sha256=%s\nbase_image=%s\n' > "$work/image-failure"
    /bin/chmod 0600 "$work/image-failure"
    /bin/mv -f "$work/image-failure" "$failure_marker"
  fi
  /bin/rm -rf -- "$work"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/source.tar.gz" %s
printf '%%s  %%s\n' %s source.tar.gz | (cd "$work" && /usr/bin/sha256sum --check --strict -)
/usr/bin/tar --no-same-owner --no-same-permissions -xzf "$work/source.tar.gz" -C "$work"
[ -d "$work/%s" ] && [ ! -L "$work/%s" ]
[ -f "$work/%s/Cargo.toml" ] && [ ! -L "$work/%s/Cargo.toml" ]
[ -f "$work/%s/Cargo.lock" ] && [ ! -L "$work/%s/Cargo.lock" ]
printf '%%s' %s | /usr/bin/base64 -d > "$work/Dockerfile"
/bin/chmod 0600 "$work/Dockerfile"
/usr/bin/docker build --pull --no-cache --tag %s --file "$work/Dockerfile" "$work"
/usr/bin/docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges %s --version | /usr/bin/grep -F %s >/dev/null
image_id="$(/usr/bin/docker image inspect --format '{{.Id}}' %s)"
printf 'image_id=%%s\nrelease=%s\ntag_object=%s\ncommit=%s\nsource_sha256=%s\nbase_image=%s\n' "$image_id" > "$work/image-attestation"
/bin/chmod 0600 "$work/image-attestation"
/bin/mv -f "$work/image-attestation" "$attestation"
/bin/rm -f -- "$failure_marker"
`, shellLiteral(buildRoot), shellLiteral(attestationPath), shellLiteral(failurePath),
		appmanifest.ElectrsRelease, appmanifest.ElectrsTagObject, appmanifest.ElectrsSourceCommit,
		appmanifest.ElectrsSourceSHA256, appmanifest.ElectrsBaseImage, shellLiteral(appmanifest.ElectrsSourceURL),
		shellLiteral(appmanifest.ElectrsSourceSHA256), appmanifest.ElectrsSourceDir, appmanifest.ElectrsSourceDir,
		appmanifest.ElectrsSourceDir, appmanifest.ElectrsSourceDir, appmanifest.ElectrsSourceDir, appmanifest.ElectrsSourceDir,
		shellLiteral(dockerfile), shellLiteral(appmanifest.ElectrsImage), shellLiteral(appmanifest.ElectrsImage),
		shellLiteral(appmanifest.ElectrsRelease), shellLiteral(appmanifest.ElectrsImage), appmanifest.ElectrsRelease,
		appmanifest.ElectrsTagObject, appmanifest.ElectrsSourceCommit, appmanifest.ElectrsSourceSHA256, appmanifest.ElectrsBaseImage)
}
