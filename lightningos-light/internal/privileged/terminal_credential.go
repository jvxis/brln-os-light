package privileged

import (
	"context"
	"errors"
	"fmt"
	"os"
	osuser "os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTerminalEnvPath  = "/etc/lightningos/terminal.env"
	terminalServiceUnit     = "lightningos-terminal.service"
	terminalServiceUnitPath = "/etc/systemd/system/lightningos-terminal.service"
	terminalLauncherPath    = "/usr/local/sbin/lightningos-terminal"
	terminalGoTTYPath       = "/usr/local/bin/gotty"
)

var terminalCredentialPasswordPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)

type TerminalCredentialManager interface {
	Rotate(ctx context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error)
}

type TerminalControlManager interface {
	SetEnabled(ctx context.Context, params TerminalControlParams, dryRun bool) (TerminalControlState, error)
}

type NativeTerminalCredentialManager struct {
	Runner          CommandRunner
	RuntimeEnvPath  string
	ApplyCredential func(path string, operatorUser string, password string) error
	ApplyControl    func(ctx context.Context, path string, operatorUser string, password string, enabled bool) error
}

func (manager *NativeTerminalCredentialManager) SetEnabled(ctx context.Context, params TerminalControlParams, dryRun bool) (TerminalControlState, error) {
	if manager == nil || manager.Runner == nil {
		return TerminalControlState{}, errors.New("terminal control manager is unavailable")
	}
	enabled := false
	switch params.Action {
	case TerminalControlEnable:
		enabled = true
	case TerminalControlDisable:
	default:
		return TerminalControlState{}, errors.New("terminal control action is invalid")
	}

	output, err := manager.Runner.Run(ctx, systemctlPath, "show", "--property=User", "--value", terminalServiceUnit)
	serviceUser := strings.TrimSpace(output)
	if err != nil || serviceUser == "root" || !systemIntegrationIdentityPattern.MatchString(serviceUser) {
		return TerminalControlState{}, errors.New("terminal operator does not match the installed service")
	}

	runtimeEnvPath := manager.RuntimeEnvPath
	if runtimeEnvPath == "" {
		runtimeEnvPath = defaultTerminalEnvPath
	}
	raw, err := readRegularFile(runtimeEnvPath, 4096)
	if err != nil {
		return TerminalControlState{}, errors.New("terminal runtime configuration is unavailable")
	}
	operatorUser, password, err := parseTerminalRuntimeCredential(raw)
	if err != nil || operatorUser != serviceUser {
		return TerminalControlState{}, errors.New("terminal runtime credential is invalid")
	}

	status := "validated"
	if !dryRun {
		applyControl := manager.ApplyControl
		if applyControl == nil {
			applyControl = manager.applyTerminalControl
		}
		if err := applyControl(ctx, runtimeEnvPath, operatorUser, password, enabled); err != nil {
			return TerminalControlState{}, errors.New("terminal control update failed")
		}
		status = "applied"
	}
	return TerminalControlState{Status: status, Enabled: enabled}, nil
}

func parseTerminalRuntimeCredential(raw []byte) (string, string, error) {
	credential := ""
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key == "TERMINAL_CREDENTIAL" {
			credential = strings.TrimSpace(value)
		}
	}
	operatorUser, password, ok := strings.Cut(credential, ":")
	if !ok || !systemIntegrationIdentityPattern.MatchString(operatorUser) || !terminalCredentialPasswordPattern.MatchString(password) {
		return "", "", errors.New("terminal credential is invalid")
	}
	return operatorUser, password, nil
}

func (manager *NativeTerminalCredentialManager) applyTerminalControl(ctx context.Context, path string, operatorUser string, password string, enabled bool) error {
	if path != defaultTerminalEnvPath {
		return errors.New("terminal runtime path is not allowlisted")
	}
	if enabled {
		for _, asset := range []struct {
			path string
			mode os.FileMode
		}{
			{path: terminalServiceUnitPath, mode: 0644},
			{path: terminalLauncherPath, mode: 0755},
			{path: terminalGoTTYPath, mode: 0755},
		} {
			if err := validateSystemIntegrationFile(asset.path, asset.mode); err != nil {
				return errors.New("terminal system integration is invalid")
			}
		}
	}
	group, err := osuser.LookupGroup("lightningos")
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	enabledValue := "0"
	if enabled {
		enabledValue = "1"
	}
	if err := writeAtomicTerminalRuntimeFile(path, []byte(terminalRuntimeEnvContent(enabledValue, operatorUser, password)), gid); err != nil {
		return err
	}

	args := []string{"disable", "--now", terminalServiceUnit}
	if enabled {
		args = []string{"enable", "--now", terminalServiceUnit}
	}
	transitionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := manager.Runner.Run(transitionCtx, systemctlPath, args...); err != nil {
		manager.failClosedTerminal(path, operatorUser, password, gid)
		return err
	}
	active, _ := manager.terminalServiceState(transitionCtx, "is-active")
	unitEnabled, _ := manager.terminalServiceState(transitionCtx, "is-enabled")
	if (enabled && (active != "active" || unitEnabled != "enabled")) || (!enabled && (active == "active" || unitEnabled == "enabled")) {
		manager.failClosedTerminal(path, operatorUser, password, gid)
		return errors.New("terminal service did not reach the requested state")
	}
	return nil
}

func (manager *NativeTerminalCredentialManager) terminalServiceState(ctx context.Context, command string) (string, error) {
	output, err := manager.Runner.Run(ctx, systemctlPath, command, terminalServiceUnit)
	return strings.TrimSpace(output), err
}

func (manager *NativeTerminalCredentialManager) failClosedTerminal(path string, operatorUser string, password string, gid int) {
	_ = writeAtomicTerminalRuntimeFile(path, []byte(terminalRuntimeEnvContent("0", operatorUser, password)), gid)
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = manager.Runner.Run(rollbackCtx, systemctlPath, "disable", terminalServiceUnit)
	_, _ = manager.Runner.Run(rollbackCtx, systemctlPath, "stop", "--no-block", terminalServiceUnit)
}

func NewNativeTerminalCredentialManager(runner CommandRunner) *NativeTerminalCredentialManager {
	return &NativeTerminalCredentialManager{Runner: runner}
}

func (manager *NativeTerminalCredentialManager) Rotate(ctx context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error) {
	if manager == nil || manager.Runner == nil {
		return TerminalCredentialState{}, errors.New("terminal credential manager is unavailable")
	}
	if !systemIntegrationIdentityPattern.MatchString(params.OperatorUser) || !terminalCredentialPasswordPattern.MatchString(params.Password) {
		return TerminalCredentialState{}, errors.New("terminal credential request is invalid")
	}
	output, err := manager.Runner.Run(ctx, systemctlPath, "show", "--property=User", "--value", terminalServiceUnit)
	serviceUser := strings.TrimSpace(output)
	if err != nil || serviceUser == "root" || !systemIntegrationIdentityPattern.MatchString(serviceUser) || serviceUser != params.OperatorUser {
		return TerminalCredentialState{}, errors.New("terminal operator does not match the installed service")
	}

	if dryRun {
		return TerminalCredentialState{Status: "validated", OperatorUser: serviceUser}, nil
	}

	runtimeEnvPath := manager.RuntimeEnvPath
	if runtimeEnvPath == "" {
		runtimeEnvPath = defaultTerminalEnvPath
	}
	applyCredential := manager.ApplyCredential
	if applyCredential == nil {
		applyCredential = applyTerminalRuntimeCredential
	}
	if err := applyCredential(runtimeEnvPath, serviceUser, params.Password); err != nil {
		return TerminalCredentialState{}, errors.New("terminal credential update failed")
	}
	return TerminalCredentialState{Status: "applied", OperatorUser: serviceUser}, nil
}

func applyTerminalRuntimeCredential(path string, operatorUser string, password string) error {
	if path != defaultTerminalEnvPath {
		return errors.New("terminal runtime path is not allowlisted")
	}
	enabled := "0"
	if raw, err := readRegularFile(path, 4096); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && key == "TERMINAL_ENABLED" && strings.TrimSpace(value) == "1" {
				enabled = "1"
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	group, err := osuser.LookupGroup("lightningos")
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	content := terminalRuntimeEnvContent(enabled, operatorUser, password)
	return writeAtomicTerminalRuntimeFile(path, []byte(content), gid)
}

func terminalRuntimeEnvContent(enabled string, operatorUser string, password string) string {
	if enabled != "1" {
		enabled = "0"
	}
	return fmt.Sprintf("TERMINAL_ENABLED=%s\nTERMINAL_CREDENTIAL=%s:%s\nTERMINAL_ALLOW_WRITE=0\nTERMINAL_PORT=7681\nTERMINAL_OPERATOR_USER=%s\nTERMINAL_TERM=xterm\nTERMINAL_SHELL=/bin/bash\nTERMINAL_WS_ORIGIN=\n", enabled, operatorUser, password, operatorUser)
}

func writeAtomicTerminalRuntimeFile(path string, content []byte, gid int) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".terminal-runtime-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0640); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Chown(0, gid); err != nil {
		cleanup()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}
