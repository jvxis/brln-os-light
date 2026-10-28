package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var lndConfigMutationMu sync.Mutex

type lndConfigOperations struct {
	path         string
	read         func() ([]byte, error)
	write        func([]byte) error
	restart      func(context.Context) error
	probe        func(context.Context) (string, error)
	readyTimeout time.Duration
}

type lndConfigApplyResult struct {
	OK                   bool `json:"ok"`
	Applied              bool `json:"applied"`
	WalletUnlockRequired bool `json:"wallet_unlock_required,omitempty"`
	WalletSetupRequired  bool `json:"wallet_setup_required,omitempty"`
}

type lndConfigApplyError struct {
	Code                   string
	Phase                  string
	ConfigRestored         bool
	RollbackVerified       bool
	RollbackUnlockRequired bool
	RollbackSetupRequired  bool
}

func (e *lndConfigApplyError) Error() string {
	if e.RollbackSetupRequired {
		return "LND configuration failed; previous file was restored and wallet setup must be completed to verify the service"
	}
	if e.RollbackUnlockRequired {
		return "LND configuration failed; previous file was restored and the wallet must be unlocked to complete recovery"
	}
	if e.RollbackVerified {
		return "LND configuration failed; previous configuration was restored and verified"
	}
	if e.ConfigRestored {
		return "LND configuration failed; previous file was restored but service recovery could not be verified"
	}
	return "LND configuration failed; automatic recovery could not be verified"
}

// Keep the inode, ownership and permissions of existing-node configurations.
// Atomic replacement under the Manager UID would change an LND-owned file's
// owner. Check every write/truncate/sync; callers restore and verify on failure.
func writeLNDConfigBytes(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	if n, err := file.WriteAt(raw, 0); err != nil {
		return err
	} else if n != len(raw) {
		return io.ErrShortWrite
	}
	if err := file.Truncate(int64(len(raw))); err != nil {
		return err
	}
	return file.Sync()
}

func (s *Server) lndConfigOperations() lndConfigOperations {
	ops := lndConfigOperations{path: lndConfPath, readyTimeout: 30 * time.Second}
	if s.lndConfigOps != nil {
		ops = *s.lndConfigOps
	}
	if ops.path == "" {
		ops.path = lndConfPath
	}
	if ops.read == nil {
		ops.read = func() ([]byte, error) { return os.ReadFile(ops.path) }
	}
	if ops.write == nil {
		ops.write = func(raw []byte) error { return writeLNDConfigBytes(ops.path, raw) }
	}
	if ops.restart == nil {
		ops.restart = func(ctx context.Context) error { return restartLNDServiceMode(ctx, false) }
	}
	if ops.probe == nil {
		ops.probe = func(ctx context.Context) (string, error) {
			if s.lnd == nil {
				return "", errors.New("LND client unavailable")
			}
			return s.lnd.ProbeConfigState(ctx)
		}
	}
	if ops.readyTimeout <= 0 {
		ops.readyTimeout = 30 * time.Second
	}
	return ops
}

func waitLNDConfigReadiness(ctx context.Context, ops lndConfigOperations, raw string) (string, error) {
	values := lndPolicyValues(raw)
	autoUnlock := false
	for _, path := range values["walletunlockpasswordfile"] {
		autoUnlock = autoUnlock || strings.Trim(strings.TrimSpace(path), "\"'") != ""
	}
	allowCreate := false
	for _, value := range values["walletunlockallowcreate"] {
		enabled, _ := strconv.ParseBool(strings.Trim(value, "\"'"))
		allowCreate = allowCreate || enabled
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		state, err := ops.probe(probeCtx)
		cancel()
		if err == nil && (state == "active" || !autoUnlock && (state == "locked" || state == "missing") || allowCreate && state == "missing") {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// The caller holds lndConfigMutationMu for the complete read/modify/apply
// transaction. The rollback has an independent bounded lifetime, so a closed
// browser connection cannot strand a bad file or an unverified stopped daemon.
func applyLNDConfig(ctx context.Context, ops lndConfigOperations, previous, updated []byte, applyNow bool) (lndConfigApplyResult, error) {
	if err := validateLNDConfigSecurity(string(previous), string(updated)); err != nil {
		return lndConfigApplyResult{}, err
	}
	failure := &lndConfigApplyError{Code: "lnd_config_write_failed", Phase: "write"}
	restartAttempted := false
	err := ops.write(updated)
	if err == nil && !applyNow {
		return lndConfigApplyResult{OK: true}, nil
	}
	if err == nil {
		failure.Code = "lnd_config_apply_failed"
		failure.Phase = "restart"
		applyCtx, cancel := context.WithTimeout(ctx, ops.readyTimeout)
		restartAttempted = true
		err = ops.restart(applyCtx)
		if err == nil {
			failure.Phase = "readiness"
			var state string
			state, err = waitLNDConfigReadiness(applyCtx, ops, string(updated))
			if err == nil {
				cancel()
				return lndConfigApplyResult{OK: true, Applied: true, WalletUnlockRequired: state == "locked", WalletSetupRequired: state == "missing"}, nil
			}
		}
		cancel()
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ops.readyTimeout)
	defer cancel()
	if err := ops.write(previous); err != nil {
		return lndConfigApplyResult{}, failure
	}
	readback, readErr := ops.read()
	failure.ConfigRestored = readErr == nil && bytes.Equal(previous, readback)
	if !failure.ConfigRestored {
		return lndConfigApplyResult{}, failure
	}
	if restartAttempted {
		if err := ops.restart(rollbackCtx); err != nil {
			return lndConfigApplyResult{}, failure
		}
		state, err := waitLNDConfigReadiness(rollbackCtx, ops, string(previous))
		if err != nil {
			return lndConfigApplyResult{}, failure
		}
		if state != "active" {
			failure.RollbackUnlockRequired = state == "locked"
			failure.RollbackSetupRequired = state == "missing"
			return lndConfigApplyResult{}, failure
		}
	}
	failure.RollbackVerified = true
	return lndConfigApplyResult{}, failure
}
