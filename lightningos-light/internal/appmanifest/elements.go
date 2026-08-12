package appmanifest

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	ElementsID             = "elements"
	ElementsVersion        = "23.3.3"
	ElementsUser           = "lightningos-elements"
	ElementsManagerGroup   = "lightningos"
	ElementsService        = "lightningos-elements"
	ElementsRPCPort        = 7041
	ElementsStateRoot      = "/var/lib/lightningos"
	ElementsAppsRoot       = ElementsStateRoot + "/apps"
	ElementsAppsDataRoot   = ElementsStateRoot + "/apps-data"
	ElementsDefaultDataDir = "/data/elements"
	ElementsStorageRoot    = "/var/lib/lightningos-privileged/apps/elements"
)

var elementsPathPattern = regexp.MustCompile(`^[A-Za-z0-9/._-]+$`)

type ElementsPaths struct {
	Root        string
	BinDir      string
	DataDir     string
	Elementsd   string
	ElementsCLI string
	Config      string
	Service     string
	Version     string
}

type ElementsReleaseAsset struct {
	Archive string
	SHA256  string
}

func DefaultElementsPaths(dataDir string) (ElementsPaths, error) {
	normalized, err := NormalizeElementsDataDir(dataDir)
	if err != nil {
		return ElementsPaths{}, err
	}
	root := path.Join(ElementsAppsRoot, ElementsID)
	return ElementsPaths{
		Root:        root,
		BinDir:      path.Join(root, "bin"),
		DataDir:     normalized,
		Elementsd:   path.Join(root, "bin", "elementsd"),
		ElementsCLI: path.Join(root, "bin", "elements-cli"),
		Config:      path.Join(normalized, "elements.conf"),
		Service:     path.Join("/etc/systemd/system", ElementsService+".service"),
		Version:     path.Join(root, "VERSION"),
	}, nil
}

func ElementsAssetForArch(goarch string) (ElementsReleaseAsset, error) {
	var suffix, checksum string
	switch goarch {
	case "amd64":
		suffix = "x86_64-linux-gnu"
		checksum = "90d6659a4f5d6d94bbf2321f6114e1286fbec8031cfc614b2f2319ddfcd9b3e1"
	case "arm64":
		suffix = "aarch64-linux-gnu"
		checksum = "279c6cf96ca0583e93fa8531ca671ffde91694254fce4719e6f3b1d0d883dd34"
	default:
		return ElementsReleaseAsset{}, fmt.Errorf("Elements does not support architecture %s", goarch)
	}
	return ElementsReleaseAsset{
		Archive: fmt.Sprintf("elements-%s-%s.tar.gz", ElementsVersion, suffix),
		SHA256:  checksum,
	}, nil
}

func NormalizeElementsDataDir(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = ElementsDefaultDataDir
	}
	if strings.Contains(trimmed, `\`) || !strings.HasPrefix(trimmed, "/") || !elementsPathPattern.MatchString(trimmed) {
		return "", errors.New("invalid Elements data directory")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return "", errors.New("Elements data directory cannot be the filesystem root")
	}
	for _, blocked := range []string{
		"/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/proc", "/root",
		"/run", "/sbin", "/sys", "/tmp", "/usr", "/var", "/data/bitcoin", "/data/lnd",
	} {
		if cleaned == blocked || strings.HasPrefix(cleaned, blocked+"/") {
			return "", fmt.Errorf("Elements data directory cannot be inside %s", blocked)
		}
	}
	return cleaned, nil
}

func ValidateElementsConfig(raw string) error {
	if len(raw) == 0 || len(raw) > 64*1024 || strings.ContainsRune(raw, '\x00') {
		return errors.New("invalid Elements configuration")
	}
	required := map[string]string{
		"chain": "liquidv1", "daemon": "0", "server": "1", "rpcbind": "127.0.0.1",
		"rpcallowip": "127.0.0.1", "rpcport": fmt.Sprint(ElementsRPCPort),
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if (key == "rpcbind" || key == "rpcallowip") && value != "127.0.0.1" {
			return fmt.Errorf("Elements configuration has unsafe %s", key)
		}
		if expected, ok := required[key]; ok && value == expected {
			seen[key] = true
		}
	}
	for key := range required {
		if !seen[key] {
			return fmt.Errorf("Elements configuration is missing required %s", key)
		}
	}
	return nil
}

func MergeElementsConfig(existing, desired string) (string, error) {
	if err := ValidateElementsConfig(desired); err != nil {
		return "", err
	}
	desiredValues := make(map[string]string)
	desiredOrder := make([]string, 0)
	desiredAssets := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(desired, "\r\n", "\n"), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "assetdir" {
			desiredAssets = append(desiredAssets, value)
			continue
		}
		if _, exists := desiredValues[key]; !exists {
			desiredOrder = append(desiredOrder, key)
		}
		desiredValues[key] = value
	}
	if strings.TrimSpace(existing) == "" {
		return strings.TrimRight(desired, "\n") + "\n", nil
	}

	seen := make(map[string]bool)
	assets := make(map[string]bool)
	updated := make([]string, 0)
	for _, line := range strings.Split(strings.TrimRight(strings.ReplaceAll(existing, "\r\n", "\n"), "\n"), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) != 2 {
			updated = append(updated, line)
			continue
		}
		key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "assetdir" {
			assets[value] = true
			updated = append(updated, line)
			continue
		}
		desiredValue, managed := desiredValues[key]
		if !managed {
			updated = append(updated, line)
			continue
		}
		if !seen[key] {
			updated = append(updated, key+"="+desiredValue)
			seen[key] = true
		}
	}
	for _, key := range desiredOrder {
		if !seen[key] {
			updated = append(updated, key+"="+desiredValues[key])
		}
	}
	for _, asset := range desiredAssets {
		if !assets[asset] {
			updated = append(updated, "assetdir="+asset)
		}
	}
	result := strings.Join(updated, "\n") + "\n"
	if err := ValidateElementsConfig(result); err != nil {
		return "", err
	}
	return result, nil
}

func ElementsServiceUnit(paths ElementsPaths) string {
	return fmt.Sprintf(`[Unit]
Description=LightningOS Elements (Liquid)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
Environment=HOME=%s
ExecStart=%s -datadir=%s -conf=%s
Restart=on-failure
RestartSec=3
TimeoutStartSec=infinity
TimeoutStopSec=600
UMask=0027
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
NoNewPrivileges=true
PrivateDevices=true
MemoryDenyWriteExecute=true
ReadWritePaths=%s

[Install]
WantedBy=multi-user.target
Alias=elementsd.service
`, ElementsUser, ElementsUser, paths.DataDir, paths.Elementsd, paths.DataDir, paths.Config, paths.DataDir)
}
