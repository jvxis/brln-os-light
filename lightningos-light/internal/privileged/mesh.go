package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"lightningos-light/internal/mesh"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const OperationMesh Operation = "app.mesh.control"
const meshUnitPath = "/etc/systemd/system/lightningos-mesh.service"
const meshConfigPath = "/var/lib/lightningos-mesh/device.json"
const meshBinaryPath = "/usr/local/libexec/lightningos-mesh"
const meshService = "lightningos-mesh.service"

type MeshParams struct {
	Action string `json:"action"`
	Device string `json:"device,omitempty"`
}
type MeshState struct {
	Installed bool     `json:"installed"`
	Status    string   `json:"status"`
	Device    string   `json:"device"`
	Devices   []string `json:"devices"`
}
type MeshManager interface {
	Control(context.Context, MeshParams, bool) (MeshState, error)
}
type NativeMeshManager struct{ Runner CommandRunner }

var meshDeviceName = regexp.MustCompile(`^/dev/serial/by-id/[a-zA-Z0-9_.:+-]{1,200}$`)

func validateMeshParams(p MeshParams) error {
	switch p.Action {
	case "status", "start", "stop", "remove":
		if p.Device != "" {
			return errors.New("unexpected mesh device")
		}
	case "install":
		if strings.HasPrefix(p.Device, "tcp://") {
			if _, err := mesh.TCPAddress(p.Device); err != nil {
				return err
			}
		} else if !meshDeviceName.MatchString(p.Device) {
			return errors.New("select a stable USB serial device")
		}
	default:
		return errors.New("unsupported mesh action")
	}
	return nil
}

func (m *NativeMeshManager) status(ctx context.Context) (MeshState, error) {
	s := MeshState{Status: "not_installed", Devices: []string{}}
	paths, _ := filepath.Glob("/dev/serial/by-id/*")
	for _, p := range paths {
		if meshDeviceName.MatchString(p) {
			if _, err := mesh.ResolveDevice(p); err == nil {
				s.Devices = append(s.Devices, p)
			}
		}
	}
	sort.Strings(s.Devices)
	s.Installed = safeNonEmptyRegularFile(meshUnitPath)
	if !s.Installed {
		return s, nil
	}
	if raw, err := readRegularFile(meshConfigPath, 1024); err == nil {
		var p MeshParams
		if json.Unmarshal(raw, &p) == nil {
			s.Device = p.Device
		}
	}
	output, err := m.Runner.Run(ctx, systemctlPath, "show", meshService, "--property=ActiveState", "--property=SubState", "--no-pager")
	s.Status = parseLoopServiceState(output)
	if err != nil && s.Status == "unknown" {
		return s, err
	}
	if s.Status == "running" && !strings.HasPrefix(s.Device, "tcp://") {
		if _, err := mesh.ResolveDevice(s.Device); err != nil {
			s.Status = "hardware_disconnected"
		}
	}
	if !safeNonEmptyRegularFile(meshBinaryPath) {
		s.Status = "upgrade_required"
	}
	return s, nil
}

func (m *NativeMeshManager) Control(ctx context.Context, p MeshParams, dry bool) (MeshState, error) {
	if err := validateMeshParams(p); err != nil {
		return MeshState{}, err
	}
	if p.Action == "status" {
		return m.status(ctx)
	}
	if dry {
		return MeshState{Status: "validated"}, nil
	}
	if p.Action == "install" {
		if strings.HasPrefix(p.Device, "tcp://") {
			address, _ := mesh.TCPAddress(p.Device)
			p.Device = "tcp://" + address
		}
		device := p.Device
		var err error
		if !strings.HasPrefix(p.Device, "tcp://") {
			device, err = mesh.ResolveDevice(p.Device)
		}
		if err != nil {
			return MeshState{}, errors.New("selected USB radio is unavailable")
		}
		if _, missing := os.Lstat(meshBinaryPath); os.IsNotExist(missing) {
			if _, err = m.Runner.Run(ctx, systemdRunPath, "--wait", "--pipe", "--collect", "--quiet", "--unit=lightningos-mesh-repair", "--", "/usr/local/libexec/lightningos-privileged", "--repair-mesh-binary"); err != nil {
				return MeshState{}, errors.New("mesh binary repair failed")
			}
		}
		if !safeNonEmptyRegularFile(meshBinaryPath) || validateRootOwnedRegularFile(meshBinaryPath, 0755) != nil {
			return MeshState{}, errors.New("upgrade LightningOS to install the radio bridge")
		}
		// Fixed user and paths only. Account creation runs in a fixed transient
		// host unit because the broker intentionally mounts /etc read-only.
		// No caller-supplied arguments, dialout membership or generic device access.
		if _, err = m.Runner.Run(ctx, idPath, "-u", "losmesh"); err != nil {
			if _, err = m.Runner.Run(ctx, systemdRunPath, "--wait", "--pipe", "--collect", "--quiet", "--unit=lightningos-mesh-identity", "--", useraddPath, "--system", "--user-group", "--home-dir", "/nonexistent", "--no-create-home", "--shell", "/usr/sbin/nologin", "losmesh"); err != nil {
				return MeshState{}, err
			}
		}
		if info, err := os.Lstat("/var/lib/lightningos-mesh"); err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return MeshState{}, errors.New("invalid mesh configuration directory")
			}
		} else if !os.IsNotExist(err) {
			return MeshState{}, err
		}
		if err = os.MkdirAll("/var/lib/lightningos-mesh", 0755); err != nil {
			return MeshState{}, err
		}
		if err = validateMeshRootDirectory("/var/lib/lightningos-mesh"); err != nil {
			return MeshState{}, err
		}
		// The broker's 0077 umask masks MkdirAll's requested mode. This directory
		// contains only the public device path and must be traversable by losmesh.
		if err = os.Chmod("/var/lib/lightningos-mesh", 0755); err != nil {
			return MeshState{}, err
		}
		for _, parent := range []string{"/usr/local/libexec", "/etc/systemd/system"} {
			if err = validateMeshRootDirectory(parent); err != nil {
				return MeshState{}, err
			}
		}
		account, err := m.Runner.Run(ctx, getentPath, "passwd", "losmesh")
		if err != nil {
			return MeshState{}, err
		}
		fields := strings.Split(strings.TrimSpace(account), ":")
		if len(fields) != 7 || fields[2] == "0" || fields[5] != "/nonexistent" || fields[6] != "/usr/sbin/nologin" {
			return MeshState{}, errors.New("unexpected radio service identity")
		}
		groups, err := m.Runner.Run(ctx, idPath, "-G", "losmesh")
		if err != nil || strings.TrimSpace(groups) != fields[3] {
			return MeshState{}, errors.New("radio service must not have supplementary groups")
		}
		// Exclusive ownership of the daemon identity is required by the installer.
		if !strings.HasPrefix(device, "tcp://") {
			if _, err = m.Runner.Run(ctx, setfaclPath, "-m", "u:losmesh:rw", device); err != nil {
				return MeshState{}, err
			}
		}
		body, _ := json.Marshal(struct {
			Device string `json:"device"`
		}{p.Device})
		if err = writeAtomicRegularFile(meshConfigPath, body, 0644); err != nil {
			return MeshState{}, err
		}
		if err = writeAtomicRegularFile(meshUnitPath, []byte(meshServiceUnit(device)), 0644); err != nil {
			return MeshState{}, err
		}
		if _, err = m.Runner.Run(ctx, systemdAnalyzePath, "verify", meshUnitPath); err != nil {
			return MeshState{}, err
		}
		if _, err = m.Runner.Run(ctx, systemctlPath, "daemon-reload"); err != nil {
			return MeshState{}, err
		}
		if _, err = m.Runner.Run(ctx, systemctlPath, "enable", meshService); err != nil {
			return MeshState{}, err
		}
		if _, err = m.Runner.Run(ctx, systemctlPath, "restart", meshService); err != nil {
			return MeshState{}, err
		}
		return m.status(ctx)
	}
	s, err := m.status(ctx)
	if err != nil {
		return s, err
	}
	if !s.Installed {
		return s, errors.New("LOS Mesh is not installed")
	}
	switch p.Action {
	case "start":
		// Revalidate the stable identity and regenerate exact DeviceAllow on hotplug.
		return m.Control(ctx, MeshParams{Action: "install", Device: s.Device}, false)
	case "stop":
		_, err = m.Runner.Run(ctx, systemctlPath, "stop", meshService)
	case "remove":
		if _, err = m.Runner.Run(ctx, systemctlPath, "disable", "--now", meshService); err != nil {
			return s, err
		}
		if device, e := mesh.ResolveDevice(s.Device); e == nil {
			if _, err = m.Runner.Run(ctx, setfaclPath, "-x", "u:losmesh", device); err != nil {
				return s, err
			}
		}
		if err = os.Remove(meshUnitPath); err != nil {
			return s, err
		}
		if err = os.Remove(meshConfigPath); err != nil && !os.IsNotExist(err) {
			return s, err
		}
		_, err = m.Runner.Run(ctx, systemctlPath, "daemon-reload")
	}
	if err != nil {
		return s, err
	}
	return m.status(ctx)
}

func meshServiceUnit(device string) string {
	network := "RestrictAddressFamilies=AF_UNIX\nDevicePolicy=closed\nDeviceAllow=" + device + " rw"
	if strings.HasPrefix(device, "tcp://") {
		address, err := mesh.TCPAddress(device)
		if err != nil {
			return ""
		}
		host, _, _ := net.SplitHostPort(address)
		network = "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6\nDevicePolicy=closed\nIPAddressDeny=any\nIPAddressAllow=" + host
	}
	return `[Unit]
Description=LOS Mesh restricted Meshtastic bridge
After=local-fs.target

[Service]
Type=simple
User=losmesh
Group=losmesh
ExecStart=/usr/local/libexec/lightningos-mesh
Restart=on-failure
RestartSec=5
RuntimeDirectory=lightningos-mesh
RuntimeDirectoryMode=0755
UMask=0077
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
` + network + `
InaccessiblePaths=-/etc/lightningos -/data/lnd -/data/bitcoin -/run/lightningos-privileged
MemoryMax=64M
TasksMax=32

[Install]
WantedBy=multi-user.target
`
}

func (client *Client) MeshControl(ctx context.Context, action, device string) (string, error) {
	response, err := client.call(ctx, OperationMesh, MeshParams{Action: action, Device: device}, false)
	if err != nil {
		return "", err
	}
	var state MeshState
	if err = decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker mesh response")
	}
	raw, err := json.Marshal(state)
	return strings.TrimSpace(string(raw)), err
}
