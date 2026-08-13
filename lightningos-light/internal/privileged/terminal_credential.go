package privileged

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

const (
	defaultChpasswdPath = "/usr/sbin/chpasswd"
	terminalServiceUnit = "lightningos-terminal.service"
)

var terminalCredentialPasswordPattern = regexp.MustCompile(`^[A-Za-z0-9]{16,128}$`)

type TerminalCredentialManager interface {
	Rotate(ctx context.Context, params TerminalCredentialRotateParams, dryRun bool) (TerminalCredentialState, error)
}

type NativeTerminalCredentialManager struct {
	Runner             CommandRunner
	ChpasswdPath       string
	ValidateExecutable func(string) error
	ApplyCredential    func(context.Context, string, string) error
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

	chpasswdPath := manager.ChpasswdPath
	if chpasswdPath == "" {
		chpasswdPath = defaultChpasswdPath
	}
	validateExecutable := manager.ValidateExecutable
	if validateExecutable == nil {
		validateExecutable = func(path string) error {
			return validateSystemIntegrationFile(path, 0755)
		}
	}
	if err := validateExecutable(chpasswdPath); err != nil {
		return TerminalCredentialState{}, errors.New("terminal credential executable is unsafe")
	}
	if dryRun {
		return TerminalCredentialState{Status: "validated", OperatorUser: serviceUser}, nil
	}

	applyCredential := manager.ApplyCredential
	if applyCredential == nil {
		applyCredential = applyTerminalCredential
	}
	credential := serviceUser + ":" + params.Password + "\n"
	if err := applyCredential(ctx, chpasswdPath, credential); err != nil {
		return TerminalCredentialState{}, errors.New("terminal credential update failed")
	}
	return TerminalCredentialState{Status: "applied", OperatorUser: serviceUser}, nil
}

func applyTerminalCredential(ctx context.Context, path string, credential string) error {
	command := exec.CommandContext(ctx, path)
	command.Stdin = strings.NewReader(credential)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}
