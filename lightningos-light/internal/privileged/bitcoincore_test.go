package privileged

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

const testBitcoinCoreImageID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestBitcoinCoreImageStatusRequiresMatchingAttestationAndImageID(t *testing.T) {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manager := &ComposeAppManager{PrivilegedAppsRoot: root}
	attestationPath := manager.bitcoinCoreImageAttestationPath()
	if err := os.MkdirAll(filepath.Dir(attestationPath), 0700); err != nil {
		t.Fatal(err)
	}
	writeBitcoinCoreTestAttestation(t, attestationPath, artifact, testBitcoinCoreImageID, appmanifest.BitcoinCoreSignatureThreshold)

	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && reflect.DeepEqual(args, []string{"image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage}) {
			return testBitcoinCoreImageID + "\n", nil, true
		}
		return "", nil, false
	}}
	manager.Runner = runner
	state, err := manager.ImageStatus(context.Background(), appmanifest.BitcoinCoreID, appmanifest.BitcoinCoreImageNode)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	if len(runner.commands) != 1 || runner.commands[0].path != dockerPath {
		t.Fatalf("unexpected commands: %#v", runner.commands)
	}
}

func TestBitcoinCoreImageStatusRejectsUnattestedOrMismatchedImages(t *testing.T) {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		attestation string
		inspectID   string
	}{
		{name: "missing attestation"},
		{name: "insufficient signatures", attestation: bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, appmanifest.BitcoinCoreSignatureThreshold-1)},
		{name: "wrong release", attestation: strings.Replace(bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3), "release=31.1", "release=31.0", 1)},
		{name: "wrong archive", attestation: strings.Replace(bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3), artifact.ArchiveSHA256, strings.Repeat("b", 64), 1)},
		{name: "wrong base", attestation: strings.Replace(bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3), artifact.BaseImage, "debian:bookworm-slim@sha256:"+strings.Repeat("c", 64), 1)},
		{name: "wrong image id", attestation: bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3), inspectID: "sha256:" + strings.Repeat("d", 64)},
		{name: "extra field", attestation: bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3) + "unexpected=true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			manager := &ComposeAppManager{PrivilegedAppsRoot: root}
			if test.attestation != "" {
				path := manager.bitcoinCoreImageAttestationPath()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(test.attestation), 0600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
				if path == dockerPath {
					if test.inspectID == "" {
						return testBitcoinCoreImageID, nil, true
					}
					return test.inspectID, nil, true
				}
				if path == systemctlPath {
					return "LoadState=not-found\nActiveState=inactive\n", errors.New("not found"), true
				}
				return "", nil, false
			}}
			manager.Runner = runner
			state, err := manager.ImageStatus(context.Background(), appmanifest.BitcoinCoreID, appmanifest.BitcoinCoreImageNode)
			if err != nil || state.Status != "absent" {
				t.Fatalf("state/error=%#v/%v", state, err)
			}
			if test.name == "missing attestation" && len(runner.commands) > 0 && runner.commands[0].path == dockerPath {
				t.Fatalf("unattested local image was inspected as trusted: %#v", runner.commands)
			}
		})
	}
}

func TestReadBitcoinCoreImageAttestationRejectsSymlink(t *testing.T) {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte(bitcoinCoreTestAttestation(artifact, testBitcoinCoreImageID, 3)), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "attestation")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := readBitcoinCoreImageAttestation(link); err == nil {
		t.Fatal("expected symlinked attestation to be rejected")
	}
}

func TestBitcoinCoreBuildScriptPinsEveryTrustedBuilderFingerprint(t *testing.T) {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	script := bitcoinCoreImageBuildScript(artifact, "/var/lib/lightningos/privileged-apps/bitcoincore/image-attestation")
	for _, builder := range appmanifest.BitcoinCoreTrustedBuilders() {
		for _, value := range []string{"\"$work/" + builder.Name + ".gpg\"", builder.Fingerprint} {
			if !strings.Contains(script, value) {
				t.Fatalf("build script missing trusted builder value %q", value)
			}
		}
	}
	for _, forbidden := range []string{"bitcoin/bitcoin", ":latest", "--privileged", "/var/run/docker.sock", "'$work/"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("build script contains forbidden value %q", forbidden)
		}
	}
	if !strings.Contains(script, `$1=="pub" {want_fpr=1`) || !strings.Contains(script, `primary_fingerprints`) {
		t.Fatal("build script does not require exactly the pinned primary key per builder file")
	}
}

func writeBitcoinCoreTestAttestation(t *testing.T, path string, artifact appmanifest.BitcoinCoreReleaseArtifact, imageID string, signatures int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(bitcoinCoreTestAttestation(artifact, imageID, signatures)), 0600); err != nil {
		t.Fatal(err)
	}
}

func bitcoinCoreTestAttestation(artifact appmanifest.BitcoinCoreReleaseArtifact, imageID string, signatures int) string {
	return "image_id=" + imageID + "\n" +
		"release=" + appmanifest.BitcoinCoreRelease + "\n" +
		"archive_sha256=" + artifact.ArchiveSHA256 + "\n" +
		"base_image=" + artifact.BaseImage + "\n" +
		"signatures=" + strconv.Itoa(signatures) + "\n"
}
