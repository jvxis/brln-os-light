//go:build linux

package privileged

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialPreflightModesAndNoMutation(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		mode                    os.FileMode
		wrongOwner, unsupported bool
		code                    string
	}{
		{"private", 0600, false, false, ""},
		{"group-readable", 0640, false, false, ""},
		{"reported-755", 0755, false, false, "admin_macaroon_mode"},
		{"wrong-owner", 0600, true, false, "admin_macaroon_owner"},
		{"unsupported-path", 0600, false, true, "macaroon_path_unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			admin := filepath.Join(root, "admin.macaroon")
			if err := os.WriteFile(admin, []byte("fixture-admin-credential"), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(admin, tc.mode); err != nil {
				t.Fatal(err)
			}
			cfg := &memoryLNDManagerCredentialConfig{path: admin}
			if tc.unsupported {
				cfg.path = filepath.Join(root, "unsupported")
			}
			beforePath := cfg.path
			rpc := &fakeLNDManagerCredentialRPC{}
			manager := &NativeLNDManagerCredentialManager{adminPath: admin, credentialPath: filepath.Join(root, "restricted"), statePath: filepath.Join(root, "state"), config: cfg, rpc: rpc}
			uid := os.Getuid()
			if tc.wrongOwner {
				uid++
			}
			state, err := manager.ensure(context.Background(), os.Getuid(), os.Getgid(), uid, os.Getgid(), true)
			if tc.code == "" {
				if err != nil || state.Status != "validated" {
					t.Fatalf("valid preflight: %#v %v", state, err)
				}
			} else {
				code, _ := LNDManagerCredentialDiagnostic(err)
				if err == nil || code != tc.code {
					t.Fatalf("diagnostic = %s %v; want %s", code, err, tc.code)
				}
			}
			info, err := os.Stat(admin)
			if err != nil || info.Mode().Perm() != tc.mode || cfg.path != beforePath || rpc.bakes != 0 || rpc.verifies != 0 || len(rpc.deleted) != 0 {
				t.Fatal("preflight mutated credentials or called LND")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("preflight created files: %v %v", entries, err)
			}
		})
	}
}
