package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLNDConfigApplyAndRecovery(t *testing.T) {
	for _, mode := range []string{"active", "save_only", "locked", "missing", "write_failure", "restart_failure", "readiness_failure", "rollback_write_failure", "rollback_restart_failure", "rollback_readback_mismatch", "cancelled_request"} {
		t.Run(mode, func(t *testing.T) {
			previous := []byte("[Application Options]\nalias=Before\n")
			updated := []byte("[Application Options]\nalias=After\n")
			stored := bytes.Clone(previous)
			writes, restarts := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ops := lndConfigOperations{readyTimeout: 20 * time.Millisecond}
			ops.read = func() ([]byte, error) { return bytes.Clone(stored), nil }
			ops.write = func(raw []byte) error {
				writes++
				if mode == "write_failure" && writes == 1 {
					stored = []byte("partial")
					return io.ErrShortWrite
				}
				if mode == "rollback_write_failure" && writes > 1 {
					return errors.New("fixture write failure")
				}
				if mode == "rollback_readback_mismatch" && writes > 1 {
					return nil
				}
				stored = bytes.Clone(raw)
				return nil
			}
			ops.restart = func(restartCtx context.Context) error {
				restarts++
				if mode == "cancelled_request" && restarts == 1 {
					cancel()
					return context.Canceled
				}
				if restartCtx.Err() != nil {
					t.Error("rollback inherited a cancelled request")
				}
				if (mode == "restart_failure" || mode == "rollback_write_failure" || mode == "rollback_readback_mismatch") && restarts == 1 || mode == "rollback_restart_failure" {
					return errors.New("fixture restart failure")
				}
				return nil
			}
			ops.probe = func(context.Context) (string, error) {
				if mode == "readiness_failure" && restarts == 1 {
					return "starting", nil
				}
				if mode == "locked" || mode == "missing" {
					return mode, nil
				}
				return "active", nil
			}
			result, err := applyLNDConfig(ctx, ops, previous, updated, mode != "save_only")
			switch mode {
			case "active", "save_only", "locked", "missing":
				if err != nil || !result.OK || !bytes.Equal(stored, updated) {
					t.Fatalf("valid apply failed: result=%+v err=%v", result, err)
				}
				if result.Applied != (mode != "save_only") || result.WalletUnlockRequired != (mode == "locked") || result.WalletSetupRequired != (mode == "missing") {
					t.Fatal("readiness state was misrepresented")
				}
			default:
				var failure *lndConfigApplyError
				if !errors.As(err, &failure) || result.OK {
					t.Fatalf("failed apply reported success: %v", err)
				}
				unrecovered := mode == "rollback_write_failure" || mode == "rollback_readback_mismatch" || mode == "rollback_restart_failure"
				if failure.RollbackVerified == unrecovered {
					t.Fatalf("wrong recovery claim: %+v", failure)
				}
				if !unrecovered && !bytes.Equal(stored, previous) {
					t.Fatal("original config was not restored")
				}
				if mode == "write_failure" && restarts != 0 {
					t.Fatal("a failed write restarted the unchanged daemon")
				}
			}
		})
	}
}

func TestLNDConfigWritePreservesFileIdentityAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lnd.conf")
	if err := os.WriteFile(path, []byte("long previous configuration with private values"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	if err := writeLNDConfigBytes(path, []byte("short\n")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || string(data) != "short\n" {
		t.Fatal("config identity/permissions/content were not preserved")
	}
}
