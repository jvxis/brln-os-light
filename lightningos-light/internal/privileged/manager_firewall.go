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
}

func NewManagerFirewallManager(runner CommandRunner) *ManagerFirewallInspector {
	return &ManagerFirewallInspector{
		Runner:     runner,
		ConfigPath: defaultManagerFirewallConfigPath,
		UFWPath:    ufwPath,
	}
}

func (manager *ManagerFirewallInspector) Status(ctx context.Context) (ManagerFirewallState, error) {
	var state ManagerFirewallState
	if manager == nil || manager.Runner == nil {
		return state, errors.New("manager firewall inspector is unavailable")
	}
	state.ConfiguredCIDR, state.ConfigValid = readManagerFirewallConfig(manager.ConfigPath)
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
		return state, nil
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
		return state, nil
	}
	state.StatusAvailable = true
	return parseManagerFirewallOutput(output, state), nil
}

func readManagerFirewallConfig(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return "", false
	}
	configured := ""
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "LAN_CIDR" {
			configured = strings.TrimSpace(value)
		}
	}
	if configured == "" {
		return "", false
	}
	if strings.EqualFold(configured, "none") {
		return "none", true
	}
	if _, _, err := net.ParseCIDR(configured); err != nil {
		return configured, false
	}
	return configured, true
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
			state.ManagerAccessBound = true
		} else {
			state.ManagerAccessBound = state.LANRulePresent
		}
	}
	return state
}
