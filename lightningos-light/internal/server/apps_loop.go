package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	loopAppID       = "loop"
	loopVersion     = "v0.33.3-beta"
	loopUser        = "lightningos-loop"
	loopStateRoot   = "/var/lib/lightningos"
	loopServiceName = "lightningos-loopd"
	loopRPCPort     = 11010
	loopRESTPort    = 18081
)

type loopPaths struct {
	Root            string
	BinDir          string
	ClientDir       string
	DataDir         string
	LNDDir          string
	LoopdPath       string
	LoopCLIPath     string
	ConfigPath      string
	ServicePath     string
	VersionPath     string
	LNDMacaroonPath string
	LNDTLSCertPath  string
	LoopMacaroon    string
	LoopTLSCert     string
	LoopTLSKey      string
	ClientMacaroon  string
	ClientTLSCert   string
}

type loopApp struct{ server *Server }

func newLoopApp(s *Server) appHandler { return loopApp{server: s} }

func loopDefinition() appDefinition {
	return appDefinition{
		ID:          loopAppID,
		Name:        "Lightning Loop",
		Description: "Optional non-custodial submarine swaps for moving liquidity between Lightning channels and the Bitcoin wallet. Manual quotes and swaps only; Autoloop is disabled.",
		Port:        0,
	}
}

func (a loopApp) Definition() appDefinition { return loopDefinition() }

func (a loopApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	if !fileExists(loopAppPaths().LoopdPath) {
		return info, nil
	}
	info.Installed = true
	status, err := loopDisplayServiceState(ctx)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func loopDisplayServiceState(ctx context.Context) (string, error) {
	out, err := runSystemd(
		ctx, "systemctl", "show", loopServiceName,
		"--property=ActiveState", "--property=SubState", "--no-pager",
	)
	if err != nil {
		return "unknown", err
	}
	return parseLoopSystemdState(out), nil
}

func parseLoopSystemdState(output string) string {
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
	if values["ActiveState"] == "inactive" || values["ActiveState"] == "failed" ||
		values["ActiveState"] == "activating" || values["ActiveState"] == "deactivating" {
		return "stopped"
	}
	return "unknown"
}

func (a loopApp) Install(ctx context.Context) error   { return a.server.installLoop(ctx) }
func (a loopApp) Uninstall(ctx context.Context) error { return a.server.uninstallLoop(ctx) }
func (a loopApp) Start(ctx context.Context) error     { return a.server.startLoop(ctx) }
func (a loopApp) Stop(ctx context.Context) error      { return a.server.stopLoop(ctx) }

func loopAppPaths() loopPaths {
	root := filepath.Join(appsRoot, loopAppID)
	data := filepath.Join(appsDataRoot, loopAppID)
	return loopPaths{
		Root:            root,
		BinDir:          filepath.Join(root, "bin"),
		ClientDir:       filepath.Join(root, "client"),
		DataDir:         data,
		LNDDir:          filepath.Join(data, "lnd"),
		LoopdPath:       filepath.Join(root, "bin", "loopd"),
		LoopCLIPath:     filepath.Join(root, "bin", "loop"),
		ConfigPath:      filepath.Join(data, "loopd.conf"),
		ServicePath:     filepath.Join("/etc/systemd/system", loopServiceName+".service"),
		VersionPath:     filepath.Join(root, "VERSION"),
		LNDMacaroonPath: filepath.Join(data, "lnd", "loopd.macaroon"),
		LNDTLSCertPath:  filepath.Join(data, "lnd", "tls.cert"),
		LoopMacaroon:    filepath.Join(data, "mainnet", "loop.macaroon"),
		LoopTLSCert:     filepath.Join(data, "tls.cert"),
		LoopTLSKey:      filepath.Join(data, "tls.key"),
		ClientMacaroon:  filepath.Join(root, "client", "loop.macaroon"),
		ClientTLSCert:   filepath.Join(root, "client", "tls.cert"),
	}
}

type loopReleaseAsset struct {
	Archive string
	SHA256  string
}

func loopAssetForArch(goarch string) (loopReleaseAsset, error) {
	checksums := map[string]string{
		"amd64": "f7b3c0983324c70413e0853fb26eb633016f8678dd3a10def96da34a241acaf2",
		"arm64": "f35f42328891a033a76e76f6b20e088444fd0c99f854e75816d5a7e35a46bb89",
		"armv7": "2a125bb900b14ec718de82084e0dd0e736b21197088937f32a43fa3c0e882db4",
	}
	arch := goarch
	if arch == "arm" {
		arch = "armv7"
	}
	checksum, ok := checksums[arch]
	if !ok {
		return loopReleaseAsset{}, fmt.Errorf("Lightning Loop does not support architecture %s", goarch)
	}
	return loopReleaseAsset{
		Archive: fmt.Sprintf("loop-linux-%s-%s.tar.gz", arch, loopVersion),
		SHA256:  checksum,
	}, nil
}

func (s *Server) installLoop(ctx context.Context) error {
	paths := loopAppPaths()
	if err := ensureLoopDirectories(ctx, paths); err != nil {
		return err
	}
	if err := ensureLoopBinary(ctx, paths); err != nil {
		return err
	}
	if err := s.ensureLoopLNDMaterial(ctx, paths); err != nil {
		return err
	}
	if err := ensureLoopConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensureLoopService(ctx, paths); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "enable", "--now", loopServiceName); err != nil {
		return fmt.Errorf("failed to start Lightning Loop: %w", err)
	}
	return nil
}

func (s *Server) startLoop(ctx context.Context) error {
	paths := loopAppPaths()
	if !fileExists(paths.LoopdPath) {
		return errors.New("Lightning Loop is not installed")
	}
	if err := ensureLoopDirectories(ctx, paths); err != nil {
		return err
	}
	if err := s.ensureLoopLNDMaterial(ctx, paths); err != nil {
		return err
	}
	if err := ensureLoopConfig(ctx, paths); err != nil {
		return err
	}
	if err := ensureLoopService(ctx, paths); err != nil {
		return err
	}
	if _, err := runSystemd(ctx, "systemctl", "restart", loopServiceName); err != nil {
		return fmt.Errorf("failed to start Lightning Loop: %w", err)
	}
	return nil
}

func (s *Server) stopLoop(ctx context.Context) error {
	if !fileExists(loopAppPaths().LoopdPath) {
		return errors.New("Lightning Loop is not installed")
	}
	status, err := serviceActiveState(ctx, loopServiceName)
	if err != nil {
		return fmt.Errorf("failed to verify Lightning Loop status: %w", err)
	}
	if status == "stopped" {
		return nil
	}
	if err := s.ensureNoPendingLoopSwaps(ctx, "stop"); err != nil {
		return err
	}
	_, err = runSystemd(ctx, "systemctl", "stop", loopServiceName)
	return err
}

func (s *Server) uninstallLoop(ctx context.Context) error {
	paths := loopAppPaths()
	if !fileExists(paths.LoopdPath) {
		return nil
	}
	status, err := serviceActiveState(ctx, loopServiceName)
	if err != nil {
		return fmt.Errorf("failed to verify Lightning Loop status: %w", err)
	}
	if status != "running" {
		return errors.New("start Lightning Loop before uninstalling so pending swaps can be verified")
	}
	if err := s.ensureNoPendingLoopSwaps(ctx, "uninstall"); err != nil {
		return err
	}
	_, _ = runSystemd(ctx, "systemctl", "disable", "--now", loopServiceName)
	_, _ = runSystemd(ctx, "/bin/sh", "-c", "rm -f "+shellQuote(paths.ServicePath)+" "+shellQuote("/etc/systemd/system/multi-user.target.wants/"+loopServiceName+".service"))
	_, _ = runSystemd(ctx, "systemctl", "daemon-reload")
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "rm -rf "+shellQuote(paths.Root)); err != nil {
		return fmt.Errorf("failed to remove Lightning Loop app files: %w", err)
	}
	_, _ = runSystemd(ctx, "/bin/sh", "-c", fmt.Sprintf(
		"if command -v setfacl >/dev/null 2>&1; then setfacl -x u:%s %s 2>/dev/null || true; fi",
		shellQuote(loopUser), shellQuote(loopStateRoot),
	))
	// Swap history, the loop database, TLS material, and the dedicated LND
	// macaroon deliberately remain in apps-data for a safe reinstall/recovery.
	return nil
}

func ensureLoopDirectories(ctx context.Context, paths loopPaths) error {
	managerGroupID, err := loopManagerGroupID()
	if err != nil {
		return err
	}
	script := loopDirectorySetupScript(paths, managerGroupID)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to prepare Lightning Loop service account and directories: %w", err)
	}
	return nil
}

// loopDirectorySetupScript deliberately provisions a dedicated daemon user at
// app-install time. Existing-node installations may not have the optional
// terminal operator (historically named losop), and a daemon must not depend on
// or run as a human login account in either installation mode.
func loopDirectorySetupScript(paths loopPaths, managerGroupID string) string {
	return fmt.Sprintf(`set -eu
if ! getent group %s >/dev/null 2>&1; then
  groupadd --system %s
fi
if ! id -u %s >/dev/null 2>&1; then
  useradd --system --gid %s --home-dir %s --no-create-home --shell /usr/sbin/nologin %s
fi
if ! command -v setfacl >/dev/null 2>&1; then
  if command -v apt-get >/dev/null 2>&1; then
    echo "Installing acl package required by Lightning Loop" >&2
    DEBIAN_FRONTEND=noninteractive apt-get install -y acl >/dev/null 2>&1 || {
      apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y acl
    }
  fi
fi
if ! command -v setfacl >/dev/null 2>&1; then
  echo "setfacl is required to grant the isolated Loop service traverse-only access; install the acl package" >&2
  exit 1
fi
setfacl -m u:%s:--x %s
mkdir -p %s %s %s %s %s
chown -R %s:%s %s %s
chmod 2750 %s %s %s %s %s
`, shellQuote(loopUser), shellQuote(loopUser), shellQuote(loopUser), shellQuote(loopUser), shellQuote(paths.DataDir), shellQuote(loopUser),
		shellQuote(loopUser), shellQuote(loopStateRoot),
		shellQuote(paths.Root), shellQuote(paths.BinDir), shellQuote(paths.ClientDir), shellQuote(paths.DataDir), shellQuote(paths.LNDDir),
		loopUser, managerGroupID, shellQuote(paths.Root), shellQuote(paths.DataDir),
		shellQuote(paths.Root), shellQuote(paths.BinDir), shellQuote(paths.ClientDir), shellQuote(paths.DataDir), shellQuote(paths.LNDDir))
}

func ensureLoopBinary(ctx context.Context, paths loopPaths) error {
	if readSecretFile(paths.VersionPath) == loopVersion && fileExists(paths.LoopdPath) && fileExists(paths.LoopCLIPath) {
		return nil
	}
	asset, err := loopAssetForArch(runtime.GOARCH)
	if err != nil {
		return err
	}
	url := "https://github.com/lightninglabs/loop/releases/download/" + loopVersion + "/" + asset.Archive
	script := fmt.Sprintf(`set -e
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl --fail --location --proto '=https' --tlsv1.2 --output "$tmp/%s" %s
printf '%%s  %%s\n' %s %s | (cd "$tmp" && sha256sum -c -)
tar -xzf "$tmp/%s" -C "$tmp"
loopd="$(find "$tmp" -type f -name loopd -print -quit)"
loopcli="$(find "$tmp" -type f -name loop -print -quit)"
test -n "$loopd"
test -n "$loopcli"
install -m 0755 "$loopd" %s
install -m 0755 "$loopcli" %s
chown %s:%s %s %s
`, asset.Archive, shellQuote(url), shellQuote(asset.SHA256), shellQuote(asset.Archive), asset.Archive,
		shellQuote(paths.LoopdPath), shellQuote(paths.LoopCLIPath), loopUser, loopUser,
		shellQuote(paths.LoopdPath), shellQuote(paths.LoopCLIPath))
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to install verified Lightning Loop binaries: %w", err)
	}
	managerGroupID, err := loopManagerGroupID()
	if err != nil {
		return err
	}
	return installLoopOwnedFile(
		ctx, paths.VersionPath, []byte(loopVersion+"\n"), 0640,
		loopUser, managerGroupID,
	)
}

func (s *Server) ensureLoopLNDMaterial(ctx context.Context, paths loopPaths) error {
	cert, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil {
		return fmt.Errorf("failed to read LND TLS certificate: %w", err)
	}
	if len(cert) == 0 {
		return errors.New("LND TLS certificate is empty")
	}
	managerGroupID, err := loopManagerGroupID()
	if err != nil {
		return err
	}
	if err := installLoopOwnedFile(
		ctx, paths.LNDTLSCertPath, cert, 0640, loopUser,
		managerGroupID,
	); err != nil {
		return fmt.Errorf("failed to copy LND TLS certificate: %w", err)
	}
	if !fileExists(paths.LNDMacaroonPath) {
		if s.lnd == nil {
			return errors.New("LND client unavailable")
		}
		ids, err := s.lnd.ListMacaroonIDs(ctx)
		if err != nil {
			return fmt.Errorf("failed to list LND macaroon IDs: %w", err)
		}
		rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
		if err != nil {
			return err
		}
		baked, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
			Permissions:              loopMacaroonPermissions(),
			RootKeyID:                rootKeyID,
			AllowExternalPermissions: true,
		})
		if err != nil {
			return fmt.Errorf("failed to bake dedicated Lightning Loop macaroon: %w", err)
		}
		raw, err := hex.DecodeString(baked.MacaroonHex)
		if err != nil || len(raw) == 0 {
			return errors.New("invalid LND macaroon response")
		}
		if err := installLoopOwnedFile(
			ctx, paths.LNDMacaroonPath, raw, 0600, loopUser,
			managerGroupID,
		); err != nil {
			return fmt.Errorf("failed to write Lightning Loop LND macaroon: %w", err)
		}
	}
	return nil
}

func loopMacaroonPermissions() []lndclient.MacaroonPermission {
	return []lndclient.MacaroonPermission{
		{Entity: "address", Action: "read"}, {Entity: "address", Action: "write"},
		{Entity: "info", Action: "read"}, {Entity: "info", Action: "write"},
		{Entity: "invoices", Action: "read"}, {Entity: "invoices", Action: "write"},
		{Entity: "message", Action: "read"}, {Entity: "message", Action: "write"},
		{Entity: "offchain", Action: "read"}, {Entity: "offchain", Action: "write"},
		{Entity: "onchain", Action: "read"}, {Entity: "onchain", Action: "write"},
		{Entity: "peers", Action: "read"}, {Entity: "peers", Action: "write"},
		{Entity: "signer", Action: "generate"}, {Entity: "signer", Action: "read"},
	}
}

func ensureLoopConfig(ctx context.Context, paths loopPaths) error {
	managerGroupID, err := loopManagerGroupID()
	if err != nil {
		return err
	}
	if err := installLoopOwnedFile(
		ctx, paths.ConfigPath, []byte(loopConfigContents(paths)), 0640,
		loopUser, managerGroupID,
	); err != nil {
		return fmt.Errorf("failed to write loopd.conf: %w", err)
	}
	return nil
}

func loopConfigContents(paths loopPaths) string {
	return fmt.Sprintf(`network=mainnet
rpclisten=127.0.0.1:%d
restlisten=127.0.0.1:%d
datadir=%s
logdir=%s
tlscertpath=%s
tlskeypath=%s
macaroonpath=%s
lnd.host=127.0.0.1:10009
lnd.macaroonpath=%s
lnd.tlspath=%s
`, loopRPCPort, loopRESTPort, paths.DataDir, filepath.Join(paths.DataDir, "logs"),
		paths.LoopTLSCert, paths.LoopTLSKey, paths.LoopMacaroon,
		paths.LNDMacaroonPath, paths.LNDTLSCertPath)
}

func ensureLoopService(ctx context.Context, paths loopPaths) error {
	contents := loopServiceContents(paths)
	if existing, err := os.ReadFile(paths.ServicePath); err == nil && string(existing) == contents {
		return nil
	}
	if err := installLoopOwnedFile(
		ctx, paths.ServicePath, []byte(contents), 0644, "root", "root",
	); err != nil {
		return fmt.Errorf("failed to install Lightning Loop service: %w", err)
	}
	if _, err := runSystemd(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

// installLoopOwnedFile stages bytes in /var/lib/lightningos, which is writable
// by the unprivileged manager, then uses the existing systemd-run privilege
// boundary to install the final file with explicit ownership and mode. Loop's
// directories belong to the service user, so direct os.WriteFile calls from
// lightningos-manager would fail after the first ownership handoff.
func installLoopOwnedFile(ctx context.Context, destination string, content []byte, mode os.FileMode, owner, group string) error {
	stagingDir := filepath.Dir(appsRoot)
	tmp, err := os.CreateTemp(stagingDir, ".loop-install-*")
	if err != nil {
		return fmt.Errorf("failed to stage %s: %w", destination, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to stage %s: %w", destination, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to stage %s: %w", destination, err)
	}
	modeValue := fmt.Sprintf("%04o", mode.Perm())
	if _, err := runSystemd(
		ctx, "install", "-o", owner, "-g", group, "-m", modeValue,
		tmpPath, destination,
	); err != nil {
		return fmt.Errorf("failed to install %s: %w", destination, err)
	}
	return nil
}

func loopManagerGroupID() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to resolve LightningOS manager group: %w", err)
	}
	return normalizeLoopManagerGroupID(current.Gid)
}

func normalizeLoopManagerGroupID(value string) (string, error) {
	groupID := strings.TrimSpace(value)
	if _, err := strconv.ParseUint(groupID, 10, 32); err != nil {
		return "", fmt.Errorf("invalid LightningOS manager group ID %q", groupID)
	}
	return groupID, nil
}

func loopServiceContents(paths loopPaths) string {
	clientMaterialScript := loopClientMaterialSyncScript(paths, true)
	return fmt.Sprintf(`[Unit]
Description=LightningOS Lightning Loop daemon
After=network-online.target lnd.service
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
ExecStart=%s --configfile=%s
ExecStartPost=/bin/sh -c %s
Restart=on-failure
RestartSec=5
UMask=0027
PrivateTmp=true
PrivateDevices=true
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, loopUser, loopUser, paths.DataDir, paths.LoopdPath, paths.ConfigPath, shellQuote(clientMaterialScript), paths.DataDir)
}

func loopClientMaterialSyncScript(paths loopPaths, wait bool) string {
	waitScript := ""
	if wait {
		waitScript = fmt.Sprintf(`i=0
while [ "$i" -lt 100 ]; do
  if [ -s %s ] && [ -s %s ]; then
    break
  fi
  i=$((i + 1))
  sleep 0.2
done
`, shellQuote(paths.LoopTLSCert), shellQuote(paths.LoopMacaroon))
	}
	return fmt.Sprintf(`set -eu
%s
test -s %s
test -s %s
test ! -L %s
test ! -L %s
mkdir -p %s
chmod 2750 %s
cp %s %s.tmp
cp %s %s.tmp
chmod 0640 %s.tmp %s.tmp
mv -f %s.tmp %s
mv -f %s.tmp %s
`, waitScript, shellQuote(paths.LoopTLSCert), shellQuote(paths.LoopMacaroon),
		shellQuote(paths.LoopTLSCert), shellQuote(paths.LoopMacaroon),
		shellQuote(paths.ClientDir), shellQuote(paths.ClientDir),
		shellQuote(paths.LoopTLSCert), shellQuote(paths.ClientTLSCert),
		shellQuote(paths.LoopMacaroon), shellQuote(paths.ClientMacaroon),
		shellQuote(paths.ClientTLSCert), shellQuote(paths.ClientMacaroon),
		shellQuote(paths.ClientTLSCert), shellQuote(paths.ClientTLSCert),
		shellQuote(paths.ClientMacaroon), shellQuote(paths.ClientMacaroon))
}

func ensureLoopClientMaterial(ctx context.Context, paths loopPaths) error {
	cert, certErr := os.ReadFile(paths.ClientTLSCert)
	macaroon, macaroonErr := os.ReadFile(paths.ClientMacaroon)
	if certErr == nil && len(cert) > 0 && macaroonErr == nil && len(macaroon) > 0 {
		return nil
	}
	if err := validateLoopDaemonMaterial(paths.LoopTLSCert, "API certificate"); err != nil {
		return err
	}
	if err := validateLoopDaemonMaterial(paths.LoopMacaroon, "API macaroon"); err != nil {
		return err
	}
	if _, err := runSystemd(
		ctx,
		"--uid="+loopUser,
		"--gid="+loopUser,
		"/bin/sh", "-c", loopClientMaterialSyncScript(paths, false),
	); err != nil {
		return fmt.Errorf("failed to prepare manager access to the Lightning Loop API: %w", err)
	}
	cert, certErr = os.ReadFile(paths.ClientTLSCert)
	macaroon, macaroonErr = os.ReadFile(paths.ClientMacaroon)
	if certErr != nil {
		return fmt.Errorf("Lightning Loop API certificate remains unreadable after repair: %w", certErr)
	}
	if len(cert) == 0 {
		return errors.New("Lightning Loop API certificate remains empty after repair")
	}
	if macaroonErr != nil {
		return fmt.Errorf("Lightning Loop API macaroon remains unreadable after repair: %w", macaroonErr)
	}
	if len(macaroon) == 0 {
		return errors.New("Lightning Loop API macaroon remains empty after repair")
	}
	return nil
}

func validateLoopDaemonMaterial(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("Lightning Loop daemon %s is unavailable at %s: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("Lightning Loop daemon %s must not be a symbolic link: %s", label, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("Lightning Loop daemon %s is not a regular file: %s", label, path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("Lightning Loop daemon %s is empty: %s", label, path)
	}
	return nil
}

func isPendingLoopState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "SUCCESS", "FAILED":
		return false
	default:
		return true
	}
}
