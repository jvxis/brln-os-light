package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	loopAppID       = "loop"
	loopVersion     = "v0.33.3-beta"
	loopUser        = "losop"
	loopServiceName = "lightningos-loopd"
	loopRPCPort     = 11010
	loopRESTPort    = 18081
)

type loopPaths struct {
	Root            string
	BinDir          string
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
	status, err := serviceActiveState(ctx, loopServiceName)
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
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
	// Swap history, the loop database, TLS material, and the dedicated LND
	// macaroon deliberately remain in apps-data for a safe reinstall/recovery.
	return nil
}

func ensureLoopDirectories(ctx context.Context, paths loopPaths) error {
	script := fmt.Sprintf(`set -e
mkdir -p %s %s %s %s
chown -R %s:%s %s %s
chmod 750 %s %s %s
`, shellQuote(paths.Root), shellQuote(paths.BinDir), shellQuote(paths.DataDir), shellQuote(paths.LNDDir),
		loopUser, loopUser, shellQuote(paths.Root), shellQuote(paths.DataDir),
		shellQuote(paths.Root), shellQuote(paths.DataDir), shellQuote(paths.LNDDir))
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to prepare Lightning Loop directories: %w", err)
	}
	return nil
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
	return writeFile(paths.VersionPath, loopVersion+"\n", 0640)
}

func (s *Server) ensureLoopLNDMaterial(ctx context.Context, paths loopPaths) error {
	cert, err := os.ReadFile("/data/lnd/tls.cert")
	if err != nil {
		return fmt.Errorf("failed to read LND TLS certificate: %w", err)
	}
	if len(cert) == 0 {
		return errors.New("LND TLS certificate is empty")
	}
	if err := os.WriteFile(paths.LNDTLSCertPath, cert, 0640); err != nil {
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
		if err := os.WriteFile(paths.LNDMacaroonPath, raw, 0600); err != nil {
			return fmt.Errorf("failed to write Lightning Loop LND macaroon: %w", err)
		}
	}
	script := fmt.Sprintf("chown %s:%s %s %s && chmod 0640 %s && chmod 0600 %s",
		loopUser, loopUser, shellQuote(paths.LNDTLSCertPath), shellQuote(paths.LNDMacaroonPath),
		shellQuote(paths.LNDTLSCertPath), shellQuote(paths.LNDMacaroonPath))
	if _, err := runSystemd(ctx, "/bin/sh", "-c", script); err != nil {
		return fmt.Errorf("failed to secure Lightning Loop LND credentials: %w", err)
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
	if err := writeFile(paths.ConfigPath, loopConfigContents(paths), 0600); err != nil {
		return fmt.Errorf("failed to write loopd.conf: %w", err)
	}
	if _, err := runSystemd(ctx, "chown", loopUser+":"+loopUser, paths.ConfigPath); err != nil {
		return fmt.Errorf("failed to secure loopd.conf ownership: %w", err)
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
	tmpPath := filepath.Join(paths.Root, "loopd.service.tmp")
	if err := writeFile(tmpPath, contents, 0644); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if _, err := runSystemd(ctx, "/bin/sh", "-c", "install -m 0644 "+shellQuote(tmpPath)+" "+shellQuote(paths.ServicePath)); err != nil {
		return fmt.Errorf("failed to install Lightning Loop service: %w", err)
	}
	if _, err := runSystemd(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

func loopServiceContents(paths loopPaths) string {
	return fmt.Sprintf(`[Unit]
Description=LightningOS Lightning Loop daemon
After=network-online.target lnd.service
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
SupplementaryGroups=lnd
Environment=HOME=/home/%s
ExecStart=%s --configfile=%s
Restart=on-failure
RestartSec=5
PrivateTmp=true
PrivateDevices=true
NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
`, loopUser, loopUser, loopUser, paths.LoopdPath, paths.ConfigPath, paths.DataDir)
}

func isPendingLoopState(state string) bool {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "SUCCESS", "FAILED":
		return false
	default:
		return true
	}
}
