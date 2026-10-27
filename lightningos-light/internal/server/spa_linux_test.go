//go:build linux

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

func TestSPAExistingAssetClosesDescriptorsWithoutGC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{}).handleSPA(dir)
	// Finalizers must not be needed for per-request descriptor release.
	oldGC := debug.SetGCPercent(-1)
	t.Cleanup(func() { debug.SetGCPercent(oldGC) })
	count := func() int {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	before := count()
	for i := 0; i < 64; i++ {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/asset.txt", nil))
		if w.Code != 200 || w.Body.String() != "asset" {
			t.Fatalf("request %d: %d %q", i, w.Code, w.Body.String())
		}
	}
	if delta := count() - before; delta > 4 {
		t.Fatalf("asset requests retained %d descriptors without garbage collection", delta)
	}
}
