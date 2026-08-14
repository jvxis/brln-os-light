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
)

const (
	defaultTerminalEnvPath = "/etc/lightningos/terminal.env"
	terminalServiceUnit    = "lightningos-terminal.service"
)

var terminalCredentialPasswordPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)

type TerminalCredentialManager interface {
	Rotate(ctx context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error)
}

type NativeTerminalCredentialManager struct {
	Runner          CommandRunner
	RuntimeEnvPath  string
	ApplyCredential func(path string, operatorUser string, password string) error
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
