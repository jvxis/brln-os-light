package appmanifest

import (
	"strings"
	"testing"
)

func TestElementsReleaseAssetsArePinned(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		asset, err := ElementsAssetForArch(arch)
		if err != nil || len(asset.SHA256) != 64 || !strings.Contains(asset.Archive, ElementsVersion) {
			t.Fatalf("invalid %s Elements asset: %+v, %v", arch, asset, err)
		}
	}
	if _, err := ElementsAssetForArch("386"); err == nil {
		t.Fatal("expected unsupported architecture error")
	}
}

func TestNormalizeElementsDataDir(t *testing.T) {
	for _, value := range []string{"/", "/etc/elements", "/data/bitcoin/elements", `C:\elements`, "/media/a path"} {
		if _, err := NormalizeElementsDataDir(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if got, err := NormalizeElementsDataDir("/media/disk/elements"); err != nil || got != "/media/disk/elements" {
		t.Fatalf("unexpected normalization: %q, %v", got, err)
	}
}

func TestElementsServiceIsConfinedAndDedicated(t *testing.T) {
	paths, err := DefaultElementsPaths(ElementsDefaultDataDir)
	if err != nil {
		t.Fatal(err)
	}
	unit := ElementsServiceUnit(paths)
	for _, want := range []string{"User=" + ElementsUser, "ProtectSystem=strict", "ProtectHome=true", "NoNewPrivileges=true", "ReadWritePaths=" + paths.DataDir} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q", want)
		}
	}
	if strings.Contains(unit, "User=losop") {
		t.Fatal("Elements must not run as the human operator")
	}
}

func TestValidateElementsConfig(t *testing.T) {
	valid := "chain=liquidv1\ndaemon=0\nserver=1\nrpcbind=127.0.0.1\nrpcallowip=127.0.0.1\nrpcport=7041\nrpcuser=elements\nrpcpassword=secret\n"
	if err := ValidateElementsConfig(valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElementsConfig(strings.Replace(valid, "rpcbind=127.0.0.1", "rpcbind=0.0.0.0", 1)); err == nil {
		t.Fatal("expected public RPC bind rejection")
	}
	if err := ValidateElementsConfig(valid + "rpcallowip=0.0.0.0/0\n"); err == nil {
		t.Fatal("expected additional public RPC allowlist rejection")
	}
}

func TestMergeElementsConfigPreservesUnknownAndForcesManagedValues(t *testing.T) {
	desired := "chain=liquidv1\ndaemon=0\nserver=1\nrpcbind=127.0.0.1\nrpcallowip=127.0.0.1\nrpcport=7041\nrpcuser=new\nrpcpassword=new-secret\nassetdir=asset:Name\n"
	existing := "# custom\nchain=liquidv1\ndaemon=0\nserver=1\nrpcbind=127.0.0.1\nrpcallowip=127.0.0.1\nrpcport=7041\nrpcuser=old\nrpcpassword=old-secret\ncustomoption=keep\n"
	merged, err := MergeElementsConfig(existing, desired)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# custom", "rpcuser=new", "rpcpassword=new-secret", "customoption=keep", "assetdir=asset:Name"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged config missing %q: %s", want, merged)
		}
	}
	if strings.Contains(merged, "rpcuser=old") || strings.Contains(merged, "rpcpassword=old-secret") {
		t.Fatalf("managed values were not replaced: %s", merged)
	}
}
