package privileged

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMeshMissingBinaryRepair(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root filesystem migration test")
	}
	dir := t.TempDir()
	source, target := filepath.Join(dir, "broker"), filepath.Join(dir, "mesh")
	if err := os.WriteFile(source, []byte("trusted executable"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := repairMeshBinaryFrom(source, target); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(target)
	if !bytes.Equal(raw, []byte("trusted executable")) {
		t.Fatal("copy mismatch")
	}
	// Idempotence: don't replace an already-installed standalone bridge.
	os.WriteFile(source, []byte("different executable"), 0755)
	if err := repairMeshBinaryFrom(source, target); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(target)
	if !bytes.Equal(raw, []byte("trusted executable")) {
		t.Fatal("existing bridge overwritten")
	}
	os.Remove(target)
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if repairMeshBinaryFrom(source, target) == nil {
		t.Fatal("symlink target accepted")
	}
	os.Remove(target)
	os.Chmod(source, 0777)
	if repairMeshBinaryFrom(source, target) == nil {
		t.Fatal("writable source accepted")
	}
}
