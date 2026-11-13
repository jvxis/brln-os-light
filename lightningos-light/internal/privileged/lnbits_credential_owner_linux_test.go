package privileged

import (
	"context"
	"lightningos-light/internal/appmanifest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLNbitsEnrollmentRejectsUnsafeLinuxOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires disposable Linux root")
	}
	for _, tc := range []struct {
		name     string
		uid, gid int
		mode     os.FileMode
	}{
		{"non-root-owner", 1000, appmanifest.LNbitsContainerGID, 0640},
		{"wrong-group", 0, 0, 0640},
		{"group-write", 0, appmanifest.LNbitsContainerGID, 0660},
		{"world-read", 0, appmanifest.LNbitsContainerGID, 0644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, rpc := lnbitsRevokeFixture(t, uint64(time.Now().Add(-time.Hour).UnixMilli()), legacyLNbitsTestKeys)
			p := filepath.Join(m.PrivilegedAppsRoot, appmanifest.LNbitsID, appmanifest.LNbitsLNDDir, appmanifest.LNbitsMacaroonFile)
			if err := os.Chown(p, tc.uid, tc.gid); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := m.RevokeLNbitsLegacyCredential(context.Background(), false); err == nil || rpc.deleted != 0 {
				t.Fatal("unsafe enrollment accepted")
			}
		})
	}
}
