package privileged

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

type NativeHardwalletManager struct {
	Runner       CommandRunner
	Paths        appmanifest.HardwalletPaths
	BindIPs      func() ([]string, error)
	requireFixed bool
}

func NewNativeHardwalletManager(runner CommandRunner) *NativeHardwalletManager {
	root := filepath.Join(defaultPrivilegedAppsRoot, appmanifest.HardwalletID)
	return &NativeHardwalletManager{Runner: runner, BindIPs: hardwalletPrivateAddresses, requireFixed: true, Paths: appmanifest.HardwalletPaths{
		Root: root, Compose: filepath.Join(root, "docker-compose.yaml"), Caddy: filepath.Join(root, "Caddyfile"), Token: filepath.Join(root, "proxy_token"), Settings: filepath.Join(root, "settings.json"), Certificate: filepath.Join(root, "server.crt"), PrivateKey: filepath.Join(root, "server.key"), ManagerCA: selectBarkManagerCACertificatePath(defaultManagerCACertificatePath, legacyManagerCACertificatePath),
	}}
}
func hardwalletPrivateAddresses() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	return hardwalletInterfaceAddresses(interfaces, func(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() })
}

func hardwalletInterfaceAddresses(interfaces []net.Interface, addrs func(net.Interface) ([]net.Addr, error)) ([]string, error) {
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Name == "docker0" || strings.HasPrefix(iface.Name, "br-") || strings.HasPrefix(iface.Name, "veth") {
			continue
		}
		addresses, err := addrs(iface)
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			ip, _, err := net.ParseCIDR(a.String())
			if err == nil && appmanifest.HardwalletPrivateIPv4(ip.String()) {
				seen[ip.String()] = true
			}
		}
	}
	addresses := make([]string, 0, len(seen))
	for address := range seen {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	if len(addresses) == 0 {
		return nil, errors.New("a private LAN or VPN IPv4 address is required for Hardwallet")
	}
	return addresses, nil
}
func (m *NativeHardwalletManager) settings() (appmanifest.HardwalletSettings, error) {
	var s appmanifest.HardwalletSettings
	b, err := readRegularFile(m.Paths.Settings, 4096)
	if err != nil {
		return s, err
	}
	if err = decodeStrict(b, &s); err != nil {
		return s, err
	}
	return s, appmanifest.ValidateHardwalletSettings(s)
}
func (m *NativeHardwalletManager) Status(ctx context.Context) (HardwalletState, error) {
	state := HardwalletState{Installed: safeNonEmptyRegularFile(m.Paths.Compose), Status: "stopped"}
	if !state.Installed {
		return state, nil
	}
	s, err := m.settings()
	if err != nil {
		return state, errors.New("Hardwallet settings are invalid")
	}
	state.Source = s.Source
	state.BindIPs = appmanifest.HardwalletBindIPs(s)
	out, err := m.Runner.Run(ctx, dockerPath, "ps", "--filter", "label=com.docker.compose.project=brln-hardwallet", "--filter", "label=com.docker.compose.service=proxy", "--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		state.Status = "unknown"
		return state, errors.New("Hardwallet status unavailable")
	}
	if strings.TrimSpace(out) != "" {
		if parseDockerContainerID(out) == "" {
			return state, errors.New("ambiguous Hardwallet container")
		}
		state.Status = "running"
	}
	return state, nil
}
func (m *NativeHardwalletManager) Control(ctx context.Context, p HardwalletControlParams, dry bool) (HardwalletState, error) {
	if err := validateHardwalletControl(p); err != nil {
		return HardwalletState{}, err
	}
	switch p.Action {
	case "ensure":
		return m.ensure(ctx, p.Source, dry)
	case "stop":
		if err := stopCatalogApp(ctx, m.Runner, appmanifest.HardwalletID, dry); err != nil {
			return HardwalletState{}, err
		}
		return HardwalletState{Installed: safeNonEmptyRegularFile(m.Paths.Compose), Status: "stopped"}, nil
	case "start", "remove":
		if p.Action == "remove" && !safeNonEmptyRegularFile(m.Paths.Compose) {
			return HardwalletState{Status: "stopped"}, nil
		}
		s, err := m.validate()
		if err != nil {
			return HardwalletState{}, err
		}
		if dry {
			return HardwalletState{Installed: true, Status: "validated", Source: s.Source}, nil
		}
		if p.Action == "start" {
			if s.Source == "local" {
				if _, err = m.Runner.Run(ctx, dockerPath, "network", "inspect", "electrs_default"); err != nil {
					return HardwalletState{}, errors.New("install and start local Electrs first")
				}
			}
			for _, v := range []appmanifest.AppImageVariant{"app", "proxy"} {
				image, _ := appmanifest.HardwalletImageForVariant(v)
				if _, err = m.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
					return HardwalletState{}, errors.New("verified Hardwallet image not ready")
				}
			}
		}
		command, prefix, err := (&ComposeAppManager{Runner: m.Runner}).resolveCompose(ctx)
		if err != nil {
			return HardwalletState{}, err
		}
		args := append(prefix, "--project-name", appmanifest.HardwalletID, "--project-directory", m.Paths.Root, "-f", m.Paths.Compose)
		bridge := ""
		if p.Action == "start" {
			// Recreate to reopen atomically replaced certificate/config mounts.
			args = append(args, "up", "-d", "--remove-orphans", "--force-recreate")
		} else {
			bridge, _ = m.bridge(ctx)
			args = append(args, "down", "--remove-orphans", "--timeout", "30")
		}
		if _, err = m.Runner.Run(ctx, command, args...); err != nil {
			return HardwalletState{}, errors.New("Hardwallet compose action failed")
		}
		if p.Action == "remove" {
			if bridge != "" {
				if err = m.firewallRule(ctx, bridge, true); err != nil {
					return HardwalletState{}, err
				}
			}
			if err = removeFixedTree(m.Paths.Root, filepath.Dir(m.Paths.Root)); err != nil {
				return HardwalletState{}, err
			}
			return HardwalletState{Status: "stopped"}, nil
		}
		return m.Status(ctx)
	case "firewall":
		if dry {
			return HardwalletState{Status: "validated"}, nil
		}
		bridge, err := m.bridge(ctx)
		if err != nil {
			return HardwalletState{}, err
		}
		if err = m.firewallRule(ctx, bridge, false); err != nil {
			return HardwalletState{}, err
		}
		return m.Status(ctx)
	}
	return HardwalletState{}, errors.New("unsupported action")
}
func (m *NativeHardwalletManager) bridge(ctx context.Context) (string, error) {
	out, err := m.Runner.Run(ctx, dockerPath, "network", "inspect", "brln-hardwallet_default", "--format", "{{.Id}}")
	id := strings.TrimSpace(out)
	if err != nil || len(id) != 64 {
		return "", errors.New("Hardwallet network unavailable")
	}
	if _, err = hex.DecodeString(id); err != nil {
		return "", errors.New("invalid network identity")
	}
	return "br-" + id[:12], nil
}
func (m *NativeHardwalletManager) firewallRule(ctx context.Context, bridge string, remove bool) error {
	out, err := m.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(out), "status: active") {
		return nil
	}
	args := []string{"allow", "in", "on", bridge, "to", "any", "port", "8443", "proto", "tcp"}
	if remove {
		args = append([]string{"--force", "delete"}, args...)
	}
	if _, err = m.Runner.Run(ctx, ufwPath, args...); err != nil {
		return errors.New("Hardwallet authorization firewall failed")
	}
	return nil
}
func (m *NativeHardwalletManager) ensure(ctx context.Context, source string, dry bool) (HardwalletState, error) {
	if err := validateManagerCACertificate(m.Paths.ManagerCA, m.requireFixed); err != nil {
		return HardwalletState{}, err
	}
	bind, err := m.BindIPs()
	if err != nil {
		return HardwalletState{}, err
	}
	if source == "" {
		source = "local"
		if existing, readErr := m.settings(); readErr == nil {
			source = existing.Source
		} else if !os.IsNotExist(rootCause(readErr)) {
			return HardwalletState{}, readErr
		}
	}
	settings := appmanifest.HardwalletSettings{Source: source, BindIPs: bind}
	compose, err := appmanifest.HardwalletCompose(m.Paths, settings)
	if err != nil {
		return HardwalletState{}, err
	}
	if dry {
		return HardwalletState{Status: "validated", Source: source}, nil
	}
	// Do not keep serving an old source/address set after publishing its replacement.
	if previous, readErr := m.settings(); readErr == nil && (previous.Source != source || !slices.Equal(appmanifest.HardwalletBindIPs(previous), bind)) {
		if err = stopCatalogApp(ctx, m.Runner, appmanifest.HardwalletID, false); err != nil {
			return HardwalletState{}, err
		}
	} else if readErr != nil && !os.IsNotExist(rootCause(readErr)) {
		return HardwalletState{}, readErr
	}
	if err = ensureDirectoryTreeNoSymlink(m.Paths.Root, 0700); err != nil {
		return HardwalletState{}, err
	}
	token, err := readRegularFile(m.Paths.Token, 128)
	if os.IsNotExist(rootCause(err)) {
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return HardwalletState{}, err
		}
		token = []byte(hex.EncodeToString(raw))
	} else if err != nil {
		return HardwalletState{}, err
	}
	caddy, err := appmanifest.HardwalletCaddy(string(token))
	if err != nil {
		return HardwalletState{}, err
	}
	cert, ce := readRegularFile(m.Paths.Certificate, 64*1024)
	key, ke := readRegularFile(m.Paths.PrivateKey, 64*1024)
	if os.IsNotExist(rootCause(ce)) && os.IsNotExist(rootCause(ke)) {
		cert, key, err = m.issueTLS(bind)
		if err != nil {
			return HardwalletState{}, err
		}
	} else if ce != nil || ke != nil || validateTLSKeyPair(cert, key) != nil {
		return HardwalletState{}, errors.New("Hardwallet TLS state invalid")
	} else if !m.tlsCurrent(cert, bind, time.Now().Add(7*24*time.Hour)) {
		cert, key, err = m.issueTLS(bind)
		if err != nil {
			return HardwalletState{}, err
		}
	}
	config, _ := json.Marshal(settings)
	for _, file := range []struct {
		path string
		raw  []byte
		mode os.FileMode
		gid  int
	}{
		{m.Paths.Token, token, 0640, appmanifest.HardwalletUID}, {m.Paths.Caddy, []byte(caddy), 0640, appmanifest.HardwalletProxyGID},
		{m.Paths.Certificate, cert, 0640, appmanifest.HardwalletProxyGID}, {m.Paths.PrivateKey, key, 0640, appmanifest.HardwalletProxyGID},
		{m.Paths.Settings, config, 0600, 0}, {m.Paths.Compose, []byte(compose), 0600, 0},
	} {
		if err = writeAtomicRegularFile(file.path, file.raw, file.mode); err != nil {
			return HardwalletState{}, err
		}
		if err = setPrivilegedPathGroup(file.path, file.gid); err != nil {
			return HardwalletState{}, err
		}
	}
	if _, err = m.validate(); err != nil {
		return HardwalletState{}, err
	}
	return HardwalletState{Installed: true, Status: "stopped", Source: source}, nil
}
func (m *NativeHardwalletManager) validate() (appmanifest.HardwalletSettings, error) {
	var zero appmanifest.HardwalletSettings
	if err := validateManagerCACertificate(m.Paths.ManagerCA, m.requireFixed); err != nil {
		return zero, err
	}
	if err := validateHardwalletPermissions(m.Paths); err != nil {
		return zero, err
	}
	if err := validateExecutionSnapshotDirectoryEntries(m.Paths.Root, map[string]bool{"docker-compose.yaml": true, "Caddyfile": true, "proxy_token": true, "settings.json": true, "server.crt": true, "server.key": true}); err != nil {
		return zero, err
	}
	settings, err := m.settings()
	if err != nil {
		return zero, err
	}
	expected, err := appmanifest.HardwalletCompose(m.Paths, settings)
	if err != nil {
		return zero, err
	}
	raw, err := readRegularFile(m.Paths.Compose, 32*1024)
	if err != nil || string(raw) != expected {
		return zero, errors.New("Hardwallet compose differs from catalog")
	}
	token, err := readRegularFile(m.Paths.Token, 128)
	if err != nil {
		return zero, err
	}
	expected, err = appmanifest.HardwalletCaddy(string(token))
	if err != nil {
		return zero, err
	}
	raw, err = readRegularFile(m.Paths.Caddy, 16*1024)
	if err != nil || string(raw) != expected {
		return zero, errors.New("Hardwallet proxy differs from catalog")
	}
	cert, ce := readRegularFile(m.Paths.Certificate, 64*1024)
	key, ke := readRegularFile(m.Paths.PrivateKey, 64*1024)
	if ce != nil || ke != nil || validateTLSKeyPair(cert, key) != nil {
		return zero, errors.New("invalid Hardwallet TLS pair")
	}
	if !m.tlsCurrent(cert, appmanifest.HardwalletBindIPs(settings), time.Now()) {
		return zero, errors.New("Hardwallet TLS certificate needs renewal")
	}
	return settings, nil
}

func hardwalletAllowsElectrsRemoval(ctx context.Context, runner CommandRunner, root string) error {
	if !safeNonEmptyRegularFile(filepath.Join(root, "docker-compose.yaml")) {
		return nil
	}
	raw, err := readRegularFile(filepath.Join(root, "settings.json"), 4096)
	var settings appmanifest.HardwalletSettings
	if err != nil || decodeStrict(raw, &settings) != nil || appmanifest.ValidateHardwalletSettings(settings) != nil || settings.Source != "club" {
		return errors.New("remove BRLN Hardwallet or select club Electrum before removing local Electrs")
	}
	// A failed switch can leave stopped containers attached to the local network.
	// Check Docker's actual attachments as well as the desired configuration.
	out, err := runner.Run(ctx, dockerPath, "ps", "-a", "--filter", "label=com.docker.compose.project=brln-hardwallet", "--filter", "network=electrs_default", "--format", "{{.ID}}")
	if err != nil || strings.TrimSpace(out) != "" {
		return errors.New("finish the Hardwallet source switch or uninstall Hardwallet before removing local Electrs")
	}
	return nil
}
