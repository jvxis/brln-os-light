package server

import (
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

func TestUpfrontShutdownUnknownOriginCannotMutate(t *testing.T) {
	s := &Server{logger: log.New(io.Discard, "", 0)}
	w := httptest.NewRecorder()
	s.handleUpfrontShutdownPost(w, httptest.NewRequest("POST", "/api/lnd/config/upfront-shutdown", strings.NewReader(`{"mode":"off"}`)))
	if w.Code != 403 {
		t.Fatalf("unverified origin accepted: %d", w.Code)
	}
}

func TestUpfrontShutdownReadDoesNotExposeOtherConfig(t *testing.T) {
	s := &Server{lndConfigOps: &lndConfigOperations{read: func() ([]byte, error) { return []byte(redactionConfigFixture), nil }}}
	w := httptest.NewRecorder()
	s.handleUpfrontShutdownGet(w, httptest.NewRequest("GET", "/api/lnd/config/upfront-shutdown", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"editable":false`) {
		t.Fatal("unknown origin should be read-only", w.Code)
	}
	for _, secret := range []string{"fixture-rpc-user", "fixture-rpc-password", "fixture-comment-token"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("unrelated secret exposed")
		}
	}
}

func TestUpfrontShutdownModesPreserveConfig(t *testing.T) {
	base := "# operator comment\n[Application Options]\nalias=Node\nwallet-unlock-password-file=/data/lnd/password.txt\n[Bitcoind]\nbitcoind.rpcpass=fixture-only\n"
	address, _ := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	for _, mode := range []string{"off", "wallet", "external"} {
		setting := upfrontShutdownSetting{Mode: mode}
		if mode == "external" {
			setting.Address = address.EncodeAddress()
		}
		updated, err := buildUpfrontShutdownConfig(base, setting, &chaincfg.MainNetParams)
		if err != nil {
			t.Fatal(err)
		}
		got, err := parseUpfrontShutdown(updated)
		if err != nil || got != setting {
			t.Fatalf("mode %s did not roundtrip", mode)
		}
		for _, line := range strings.Split(base, "\n") {
			if !strings.Contains(updated, line) {
				t.Fatal("unrelated line lost")
			}
		}
		if err := validateLNDConfigSecurity(base, updated); err != nil {
			t.Fatal(err)
		}
		twice, err := buildUpfrontShutdownConfig(updated, setting, &chaincfg.MainNetParams)
		if err != nil || twice != updated {
			t.Fatal("not idempotent")
		}
		off, err := buildUpfrontShutdownConfig(updated, upfrontShutdownSetting{Mode: "off"}, &chaincfg.MainNetParams)
		if err != nil || strings.Contains(off, "upfront-shutdown") {
			t.Fatal("off left a global address enabled")
		}
	}
}

func TestUpfrontShutdownRejectsUnsafeInput(t *testing.T) {
	main, _ := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.MainNetParams)
	reg, _ := btcutil.NewAddressWitnessPubKeyHash(make([]byte, 20), &chaincfg.RegressionNetParams)
	for _, setting := range []upfrontShutdownSetting{{Mode: "bad"}, {Mode: "external"}, {Mode: "wallet", Address: main.EncodeAddress()}, {Mode: "external", Address: reg.EncodeAddress()}, {Mode: "external", Address: main.EncodeAddress() + "\nno-macaroons=true"}, {Mode: "external", Address: "bitcoin:" + main.EncodeAddress()}} {
		if _, err := buildUpfrontShutdownConfig("[Application Options]\n", setting, &chaincfg.MainNetParams); err == nil {
			t.Fatal("unsafe input accepted")
		}
	}
	for _, raw := range []string{"[Application Options]\nenable-upfront-shutdown=true\nenable-upfront-shutdown=false\n", "[tor]\nupfront-shutdown-address=x\n", "[Application Options]\nenable-upfront-shutdown=bad\n"} {
		if _, err := buildUpfrontShutdownConfig(raw, upfrontShutdownSetting{Mode: "off"}, &chaincfg.MainNetParams); err == nil {
			t.Fatal("ambiguous config accepted")
		}
	}
	if _, err := buildUpfrontShutdownConfig("[Application Options]\n", upfrontShutdownSetting{Mode: "external", Address: reg.EncodeAddress()}, &chaincfg.RegressionNetParams); err != nil {
		t.Fatal("regtest builder rejected correct network")
	}
}

func TestUpfrontShutdownOriginFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	marker := filepath.Join(dir, "installation-origin")
	if readLNDInstallationOrigin(path) != "unknown" {
		t.Fatal("missing origin inferred")
	}
	for _, value := range []string{"native-v1\n", "existing-v1\n", "garbage"} {
		if err := os.WriteFile(marker, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		// Windows emulates Unix modes as writable/read-only. Use read-only
		// here; the Linux native checks cover actual Unix permission rejection.
		if err := os.Chmod(marker, 0444); err != nil {
			t.Fatal(err)
		}
		want := "unknown"
		if strings.HasPrefix(value, "native-") {
			want = "native"
		}
		if strings.HasPrefix(value, "existing-") {
			want = "existing"
		}
		if runtime.GOOS != "linux" || os.Geteuid() != 0 {
			want = "unknown"
		}
		if got := readLNDInstallationOrigin(path); got != want {
			t.Fatalf("origin %s want %s", got, want)
		}
		if err := os.Chmod(marker, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUpfrontShutdownOriginUnsafeFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Unix ownership/mode semantics")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "installation-origin")
	if err := os.WriteFile(marker, []byte("native-v1\n"), 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(marker, 0666); err != nil {
		t.Fatal(err)
	}
	if readLNDInstallationOrigin(filepath.Join(dir, "config.yaml")) != "unknown" {
		t.Fatal("writable origin trusted")
	}
	linkDir := t.TempDir()
	if err := os.Chmod(marker, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(marker, filepath.Join(linkDir, "installation-origin")); err != nil {
		t.Fatal(err)
	}
	if readLNDInstallationOrigin(filepath.Join(linkDir, "config.yaml")) != "unknown" {
		t.Fatal("symlink origin trusted")
	}
}
