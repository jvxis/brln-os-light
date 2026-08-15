package privileged

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
)

const defaultManagerFirewallConfigPath = "/etc/lightningos/manager-firewall.conf"

type ManagerFirewallInspector struct {
	Runner     CommandRunner
	ConfigPath string
	UFWPath    string
	HelperPath string
}

func NewManagerFirewallManager(runner CommandRunner) *ManagerFirewallInspector {
	return &ManagerFirewallInspector{
		Runner:     runner,
		ConfigPath: defaultManagerFirewallConfigPath,
		UFWPath:    ufwPath,
		HelperPath: defaultManagerFirewallHelperPath,
	}
}

func (manager *ManagerFirewallInspector) Status(ctx context.Context) (ManagerFirewallState, error) {
	var state ManagerFirewallState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("manager firewall inspector is unavailable")
	}
	state = readManagerFirewallConfig(manager.ConfigPath, state)
	if manager.ConfigPath == defaultManagerFirewallConfigPath {
		_, statErr := os.Lstat(manager.ConfigPath)
		if statErr == nil {
			statErr = validateRootOwnedRegularFile(manager.ConfigPath, 0644)
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return state, errors.New("manager firewall config ownership is unsafe")
		}
	}

	info, err := os.Lstat(manager.UFWPath)
	if errors.Is(err, os.ErrNotExist) {
		return classifyManagerFirewallState(state), nil
	}
	if err != nil {
		return state, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return state, errors.New("ufw executable is unsafe")
	}
	if manager.UFWPath == ufwPath {
		if err := validateRootOwnedRegularFile(manager.UFWPath, 0755); err != nil {
			return state, errors.New("ufw executable ownership is unsafe")
		}
	}
	state.Installed = true

	output, err := manager.Runner.Run(ctx, manager.UFWPath, "status")
	if err != nil {
		return classifyManagerFirewallState(state), nil
	}
	state.StatusAvailable = true
	return classifyManagerFirewallState(parseManagerFirewallOutput(output, state)), nil
}

func readManagerFirewallConfig(path string, state ManagerFirewallState) ManagerFirewallState {
	if strings.TrimSpace(path) == "" {
		return state
	}
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return state
	}
	configured := ""
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "LAN_CIDR":
			configured = strings.TrimSpace(value)
		case "ACCESS_MODE":
			state.AccessMode = strings.TrimSpace(value)
		case "ACKNOWLEDGED_UNPROTECTED":
			state.Acknowledged = strings.TrimSpace(value) == "1"
		}
	}
	state.ConfiguredCIDR = configured
	if configured == "" {
		return state
	}
	if strings.EqualFold(configured, "none") {
		state.ConfigValid = true
		return state
	}
	if _, _, err := net.ParseCIDR(configured); err != nil {
		return state
	}
	state.ConfigValid = true
	return state
}

func classifyManagerFirewallState(state ManagerFirewallState) ManagerFirewallState {
	if state.AccessMode == "unprotected" && state.Acknowledged {
		state.ProtectionState = "acknowledged_unprotected"
		return state
	}
	if !state.Installed {
		state.ProtectionState = "firewall_unavailable"
		return state
	}
	if !state.StatusAvailable {
		state.ProtectionState = "protection_unverified"
		return state
	}
	if !state.Active {
		state.ProtectionState = "firewall_inactive"
		return state
	}
	if state.BroadRulePresent {
		state.ProtectionState = "broad_exposure"
		return state
	}
	if state.ManagerAccessBound {
		if state.LANRulePresent && state.TailscaleRule {
			state.ProtectionState = "protected_lan_and_vpn"
			return state
		}
		switch state.AccessMode {
		case "vpn":
			state.ProtectionState = "protected_vpn_only"
		default:
			state.ProtectionState = "protected_lan_only"
		}
		return state
	}
	state.ProtectionState = "protection_unverified"
	return state
}

func (manager *ManagerFirewallInspector) Configure(ctx context.Context, params ManagerFirewallConfigureParams, dryRun bool) (ManagerFirewallState, error) {
	if manager == nil || manager.Runner == nil {
		return ManagerFirewallState{}, errors.New("manager firewall controller is unavailable")
	}
	helperPath := manager.HelperPath
	if helperPath == "" {
		helperPath = defaultManagerFirewallHelperPath
	}
	if helperPath == defaultManagerFirewallHelperPath {
		if err := validateRootOwnedRegularFile(helperPath, 0755); err != nil {
			return ManagerFirewallState{}, errors.New("manager firewall helper is unsafe")
		}
	}
	args := []string{"--mode", params.Mode}
	if params.Mode == "lan" {
		args = append(args, "--lan-cidr", params.LANCIDR)
	}
	if params.Mode == "unprotected" {
		args = append(args, "--acknowledge-unprotected")
	}
	if dryRun {
		return ManagerFirewallState{AccessMode: params.Mode, ConfiguredCIDR: params.LANCIDR}, nil
	}
	if _, err := manager.Runner.Run(ctx, helperPath, args...); err != nil {
		return ManagerFirewallState{}, errors.New("manager firewall policy application failed")
	}
	state, err := manager.Status(ctx)
	if err != nil {
		return ManagerFirewallState{}, err
	}
	if state.AccessMode != params.Mode {
		return ManagerFirewallState{}, errors.New("manager firewall policy verification failed")
	}
	if params.Mode == "unprotected" {
		if !state.Acknowledged {
			return ManagerFirewallState{}, errors.New("unprotected manager exposure was not acknowledged")
		}
		return state, nil
	}
	if !state.ManagerAccessBound || state.BroadRulePresent {
		return ManagerFirewallState{}, errors.New("manager firewall policy verification failed")
	}
	return state, nil
}

func parseManagerFirewallOutput(output string, state ManagerFirewallState) ManagerFirewallState {
	configuredLower := strings.ToLower(strings.TrimSpace(state.ConfiguredCIDR))
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.ToLower(strings.TrimSpace(rawLine))
		if strings.HasPrefix(line, "status:") {
			state.Active = strings.Contains(line, "active") && !strings.Contains(line, "inactive")
		}
		if !strings.Contains(line, "8443") || !strings.Contains(line, "allow") {
			continue
		}
		if configuredLower != "" && configuredLower != "none" && strings.Contains(line, configuredLower) {
			state.LANRulePresent = true
		}
		if strings.Contains(line, "tailscale0") {
			state.TailscaleRule = true
		}
		if strings.Contains(line, "anywhere") && !strings.Contains(line, "tailscale0") {
			state.BroadRulePresent = true
		}
	}
	if state.Active && state.ConfigValid && !state.BroadRulePresent {
		if configuredLower == "none" {
			state.ManagerAccessBound = state.TailscaleRule
		} else {
			state.ManagerAccessBound = state.LANRulePresent && (state.AccessMode != "lan" || !state.TailscaleRule)
		}
	}
	return state
}
