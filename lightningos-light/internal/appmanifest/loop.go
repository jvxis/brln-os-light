package appmanifest

import (
	"errors"
	"fmt"
	"path"
)

const (
	LoopID           = "loop"
	LoopVersion      = "v0.33.3-beta"
	LoopUser         = "lightningos-loop"
	LoopManagerGroup = "lightningos"
	LoopService      = "lightningos-loopd"
	LoopRPCPort      = 11010
	LoopRESTPort     = 18081
	LoopStateRoot    = "/var/lib/lightningos"
	LoopAppsRoot     = LoopStateRoot + "/apps"
	LoopAppsDataRoot = LoopStateRoot + "/apps-data"
)

type LoopPaths struct {
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
	LoopDBPath      string
	LegacyLoopDB    string
	LoopLogPath     string
}

func DefaultLoopPaths() LoopPaths {
	return LoopPathsForRoots(LoopAppsRoot, LoopAppsDataRoot, "/etc/systemd/system")
}

func LoopPathsForRoots(appsRoot, appsDataRoot, systemdRoot string) LoopPaths {
	root := path.Join(appsRoot, LoopID)
	data := path.Join(appsDataRoot, LoopID)
	networkData := path.Join(data, "mainnet")
	return LoopPaths{
		Root:            root,
		BinDir:          path.Join(root, "bin"),
		ClientDir:       path.Join(root, "client"),
		DataDir:         data,
		LNDDir:          path.Join(data, "lnd"),
		LoopdPath:       path.Join(root, "bin", "loopd"),
		LoopCLIPath:     path.Join(root, "bin", "loop"),
		ConfigPath:      path.Join(data, "loopd.conf"),
		ServicePath:     path.Join(systemdRoot, LoopService+".service"),
		VersionPath:     path.Join(root, "VERSION"),
		LNDMacaroonPath: path.Join(data, "lnd", "loopd.macaroon"),
		LNDTLSCertPath:  path.Join(data, "lnd", "tls.cert"),
		LoopMacaroon:    path.Join(data, "mainnet", "loop.macaroon"),
		LoopTLSCert:     path.Join(data, "tls.cert"),
		LoopTLSKey:      path.Join(data, "tls.key"),
		ClientMacaroon:  path.Join(root, "client", "loop.macaroon"),
		ClientTLSCert:   path.Join(root, "client", "tls.cert"),
		LoopDBPath:      path.Join(networkData, "loop_sqlite.db"),
		LegacyLoopDB:    path.Join(networkData, "loop.db"),
		LoopLogPath:     path.Join(data, "logs", "mainnet", "loopd.log"),
	}
}

type LoopReleaseAsset struct {
	Archive string
	SHA256  string
}

func LoopAssetForArch(goarch string) (LoopReleaseAsset, error) {
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
		return LoopReleaseAsset{}, fmt.Errorf("Lightning Loop does not support architecture %s", goarch)
	}
	return LoopReleaseAsset{
		Archive: fmt.Sprintf("loop-linux-%s-%s.tar.gz", arch, LoopVersion),
		SHA256:  checksum,
	}, nil
}

func LoopConfig(paths LoopPaths) string {
	return fmt.Sprintf(`[Application Options]
network=mainnet
rpclisten=127.0.0.1:%d
restlisten=127.0.0.1:%d
datadir=%s
logdir=%s
tlscertpath=%s
tlskeypath=%s
macaroonpath=%s

[lnd]
lnd.host=127.0.0.1:10009
lnd.macaroonpath=%s
lnd.tlspath=%s
`, LoopRPCPort, LoopRESTPort, paths.DataDir, path.Join(paths.DataDir, "logs"),
		paths.LoopTLSCert, paths.LoopTLSKey, paths.LoopMacaroon,
		paths.LNDMacaroonPath, paths.LNDTLSCertPath)
}

func LoopServiceUnit(paths LoopPaths) string {
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
`, LoopUser, LoopUser, paths.DataDir, paths.LoopdPath, paths.ConfigPath, paths.DataDir)
}

func ValidateLoopMaterial(tlsCertificate, macaroon []byte) error {
	if len(tlsCertificate) == 0 || len(tlsCertificate) > 16*1024 {
		return errors.New("invalid LND TLS certificate")
	}
	if len(macaroon) > 16*1024 {
		return errors.New("invalid LND macaroon")
	}
	return nil
}
