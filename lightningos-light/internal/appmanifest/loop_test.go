package appmanifest

import (
	"strings"
	"testing"
)

func TestLoopManifestIsClosedAndHardened(t *testing.T) {
	paths := DefaultLoopPaths()
	for _, path := range []string{paths.Root, paths.DataDir, paths.ServicePath, paths.LoopdPath, paths.ConfigPath} {
		if !strings.HasPrefix(path, "/var/lib/lightningos/") && !strings.HasPrefix(path, "/etc/systemd/system/") {
			t.Fatalf("Loop manifest escaped fixed roots: %s", path)
		}
	}
	config := LoopConfig(paths)
	for _, expected := range []string{"network=mainnet", "rpclisten=127.0.0.1:11010", "restlisten=127.0.0.1:18081", "lnd.host=127.0.0.1:10009"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("Loop config missing %q", expected)
		}
	}
	for _, forbidden := range []string{"0.0.0.0", "admin.macaroon", "autoloop"} {
		if strings.Contains(strings.ToLower(config), forbidden) {
			t.Fatalf("Loop config contains unsafe value %q", forbidden)
		}
	}
	unit := LoopServiceUnit(paths)
	for _, expected := range []string{"User=lightningos-loop", "NoNewPrivileges=true", "PrivateDevices=true", "ProtectSystem=full", "ProtectHome=true", "UMask=0027"} {
		if !strings.Contains(unit, expected) {
			t.Fatalf("Loop unit missing %q", expected)
		}
	}
}

func TestLoopReleaseCatalogPinsEveryArchitecture(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "arm", "armv7"} {
		asset, err := LoopAssetForArch(arch)
		if err != nil || !strings.Contains(asset.Archive, LoopVersion) || len(asset.SHA256) != 64 {
			t.Fatalf("invalid Loop asset for %s: %+v, %v", arch, asset, err)
		}
	}
	if _, err := LoopAssetForArch("386"); err == nil {
		t.Fatal("unsupported Loop architecture accepted")
	}
}

func TestValidateLoopMaterialBoundsSecrets(t *testing.T) {
	if err := ValidateLoopMaterial([]byte("cert"), []byte("macaroon")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLoopMaterial(nil, nil); err == nil {
		t.Fatal("empty certificate accepted")
	}
	if err := ValidateLoopMaterial([]byte("cert"), make([]byte, 16*1024+1)); err == nil {
		t.Fatal("oversized macaroon accepted")
	}
}
