package privileged

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestExtractElementsBinariesRejectsMissingAndExtractsFixedNames(t *testing.T) {
	archive := elementsTestArchive(t, map[string]string{
		"elements-23.3.3/bin/elementsd":    "daemon",
		"elements-23.3.3/bin/elements-cli": "client",
		"elements-23.3.3/bin/other":        "ignored",
	})
	binaries, err := extractElementsBinaries(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(binaries["elementsd"]) != "daemon" || string(binaries["elements-cli"]) != "client" || len(binaries) != 2 {
		t.Fatalf("unexpected extracted binaries: %#v", binaries)
	}
	if _, err := extractElementsBinaries(elementsTestArchive(t, map[string]string{"elements-23.3.3/bin/elementsd": "daemon"})); err == nil {
		t.Fatal("expected missing elements-cli rejection")
	}
}

func TestNativeElementsCLIHasFixedMethodAllowlist(t *testing.T) {
	manager := &NativeElementsManager{}
	if _, err := manager.elementsCLI(t.Context(), appmanifestElementsTestPaths(t), "stop"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected arbitrary RPC method rejection, got %v", err)
	}
}

func appmanifestElementsTestPaths(t *testing.T) appmanifest.ElementsPaths {
	t.Helper()
	paths, err := appmanifest.DefaultElementsPaths(appmanifest.ElementsDefaultDataDir)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func elementsTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}
