package privileged

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
)

const smartLSBLKPath = "/usr/bin/lsblk"

var smartctlFixedPaths = []string{"/usr/sbin/smartctl", "/usr/bin/smartctl"}

type NativeSMARTManager struct {
	Runner           CommandRunner
	LSBLKPath        string
	SMARTCTLPaths    []string
	RequireRootFiles bool
}

func NewNativeSMARTManager(runner CommandRunner) *NativeSMARTManager {
	return &NativeSMARTManager{
		Runner:           runner,
		LSBLKPath:        smartLSBLKPath,
		SMARTCTLPaths:    append([]string(nil), smartctlFixedPaths...),
		RequireRootFiles: true,
	}
}

func (manager *NativeSMARTManager) Read(ctx context.Context, device string) (SMARTReadState, error) {
	state := SMARTReadState{Device: device}
	if manager == nil || manager.Runner == nil || manager.LSBLKPath == "" || len(manager.SMARTCTLPaths) == 0 {
		return state, errors.New("SMART manager is unavailable")
	}
	if !smartDevicePattern.MatchString(device) {
		return state, errors.New("SMART device is invalid")
	}
	if manager.RequireRootFiles {
		if manager.LSBLKPath != smartLSBLKPath || !sameStringSet(manager.SMARTCTLPaths, smartctlFixedPaths) {
			return state, errors.New("SMART executable policy is invalid")
		}
		if err := validateRootOwnedRegularFile(manager.LSBLKPath, 0o755); err != nil {
			return state, errors.New("lsblk executable is unsafe")
		}
	}
	listing, err := manager.Runner.Run(ctx, manager.LSBLKPath, "-dn", "-o", "PATH,TYPE")
	if err != nil {
		return state, errors.New("block device inventory failed")
	}
	if !listedSMARTDisk(listing, device) {
		return state, errors.New("SMART target is not an enumerated disk")
	}
	smartctlPath, err := manager.smartctlPath()
	if err != nil {
		return state, err
	}
	output, runErr := manager.Runner.Run(ctx, smartctlPath, "-a", device)
	if len(output) > maxCommandOutputBytes || strings.ContainsRune(output, '\x00') {
		return state, errors.New("SMART output is invalid")
	}
	if runErr != nil && strings.TrimSpace(output) == "" {
		return state, errors.New("SMART read failed")
	}
	state.Output = output
	state.Available = strings.TrimSpace(output) != ""
	return state, nil
}

func (manager *NativeSMARTManager) smartctlPath() (string, error) {
	for _, path := range manager.SMARTCTLPaths {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", errors.New("smartctl executable is unsafe")
		}
		if manager.RequireRootFiles {
			if err := validateRootOwnedRegularFile(path, 0o755); err != nil {
				return "", errors.New("smartctl executable is unsafe")
			}
		}
		return path, nil
	}
	return "", errors.New("smartctl is unavailable")
}

func listedSMARTDisk(output, device string) bool {
	scanner := bufio.NewScanner(bytes.NewBufferString(output))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == device && fields[1] == "disk" {
			return true
		}
	}
	return false
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
