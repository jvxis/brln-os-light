package appmanifest

import (
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Real Python HTTPS requests, not mocked TLS/urllib. The endpoint and credential
// are synthetic fixtures; no node, wallet or privileged service is accessed.
func TestLNbitsStartupGateRealTLSAndCredentialBoundary(t *testing.T) {
	python := lnbitsTestPython(t)
	var status atomic.Int32
	status.Store(http.StatusOK)
	credential := []byte("synthetic-readiness-test-credential")
	var redirectRequests atomic.Int32
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectRequests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirect.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/balance/channels" {
			t.Error("readiness probe changed the method or path")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Grpc-Metadata-macaroon") != hex.EncodeToString(credential) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if status.Load() == http.StatusFound {
			http.Redirect(w, r, redirect.URL, http.StatusFound)
			return
		}
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"balance":"0"}`))
	}))
	defer server.Close()
	root := t.TempDir()
	certificate := filepath.Join(root, "tls.cert")
	macaroon := filepath.Join(root, "lnbits.macaroon")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(macaroon, credential, 0600); err != nil {
		t.Fatal(err)
	}
	probe := func(endpoint, cert string) (string, error) {
		t.Helper()
		cmd := exec.Command(python, "-B", "-I", "-c", `import runpy,sys
gate=runpy.run_path(sys.argv[1])
try:
    print(gate["lnd_ready"](*sys.argv[2:]))
except Exception:
    print("blocked")
`, "assets/lnbits_wait_lnd.py", endpoint, cert, macaroon)
		// Even explicitly configured process proxies cannot see the macaroon.
		cmd.Env = append(os.Environ(), "HTTPS_PROXY=http://127.0.0.1:1", "ALL_PROXY=http://127.0.0.1:1", "NO_PROXY=")
		output, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	for _, code := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusFound, http.StatusOK} {
		status.Store(int32(code))
		got, err := probe(server.URL+"/", certificate)
		want := "blocked"
		if code == http.StatusOK {
			want = "True"
		}
		if err != nil || got != want {
			t.Fatalf("HTTP %d: got %q/%v want %q", code, got, err, want)
		}
	}
	if redirectRequests.Load() != 0 {
		t.Fatal("readiness request followed a redirect")
	}
	if err := os.WriteFile(certificate, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := probe(server.URL, certificate); err != nil || got != "blocked" {
		t.Fatalf("untrusted TLS was not blocked: %q/%v", got, err)
	}
	// Refreshing the mounted file, rather than recreating a pinned SSL context,
	// must release a subsequent attempt without changing the LND certificate.
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := probe(server.URL, certificate); err != nil || got != "True" {
		t.Fatalf("certificate refresh did not recover: %q/%v", got, err)
	}
	if err := os.WriteFile(macaroon, []byte("wrong synthetic credential"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := probe(server.URL, certificate); err != nil || got != "blocked" {
		t.Fatalf("wrong macaroon did not block startup: %q/%v", got, err)
	}
	server.Close()
	if got, err := probe(server.URL, certificate); err != nil || got != "blocked" {
		t.Fatalf("offline LND did not block startup: %q/%v", got, err)
	}
}
