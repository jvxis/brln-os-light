package privileged

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"lightningos-light/internal/appmanifest"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHardwalletStatusReportsConfiguredAddress(t *testing.T) {
	m := testHardwalletManager(t)
	ctx := context.Background()
	if _, err := m.Control(ctx, HardwalletControlParams{Action: "ensure", Source: "club"}, false); err != nil {
		t.Fatal(err)
	}
	// Discovery may have changed since installation. Open must use the saved bind.
	m.BindIP = func() (string, error) { return "100.100.1.2", nil }
	state, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["bind_ip"] != "100.100.1.1" {
		t.Fatalf("status omitted configured app address: %s", raw)
	}
}

func testHardwalletManager(t *testing.T) *NativeHardwalletManager {
	t.Helper()
	if runtime.GOOS == "linux" && os.Geteuid() != 0 {
		t.Skip("root required for snapshot ownership integration")
	}
	root := t.TempDir()
	snapshot := filepath.Join(root, "app")
	caPath := filepath.Join(root, "local-ca.crt")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	raw := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	mustWriteTestFile(t, caPath, raw, 0644)
	encoded, _ := x509.MarshalPKCS8PrivateKey(key)
	mustWriteTestFile(t, filepath.Join(root, "local-ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0600)
	mustWriteTestFile(t, filepath.Join(root, "server.crt"), raw, 0644)
	return &NativeHardwalletManager{Runner: &composeRecordingRunner{}, BindIP: func() (string, error) { return "100.100.1.1", nil }, Paths: appmanifest.HardwalletPaths{Root: snapshot, Compose: filepath.Join(snapshot, "docker-compose.yaml"), Caddy: filepath.Join(snapshot, "Caddyfile"), Token: filepath.Join(snapshot, "proxy_token"), Settings: filepath.Join(snapshot, "settings.json"), Certificate: filepath.Join(snapshot, "server.crt"), PrivateKey: filepath.Join(snapshot, "server.key"), ManagerCA: caPath}}
}
func TestHardwalletEnsureAndTamperBoundary(t *testing.T) {
	m := testHardwalletManager(t)
	state, err := m.Control(context.Background(), HardwalletControlParams{Action: "ensure", Source: "local"}, false)
	if err != nil || !state.Installed {
		t.Fatal(state, err)
	}
	before, _ := os.ReadFile(m.Paths.Token)
	if _, err = m.Control(context.Background(), HardwalletControlParams{Action: "ensure", Source: "club"}, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(m.Paths.Token)
	if string(before) != string(after) || len(after) != 64 {
		t.Fatal("proxy identity unexpectedly changed")
	}
	certRaw, _ := os.ReadFile(m.Paths.Certificate)
	block, _ := pem.Decode(certRaw)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	caRaw, _ := os.ReadFile(m.Paths.ManagerCA)
	caBlock, _ := pem.Decode(caRaw)
	ca, _ := x509.ParseCertificate(caBlock.Bytes)
	if cert.CheckSignatureFrom(ca) != nil || cert.VerifyHostname("100.100.1.1") != nil {
		t.Fatal("app certificate is not trusted for bind address")
	}
	f, err := os.OpenFile(m.Paths.Compose, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n# altered\n")
	f.Close()
	runner := m.Runner.(*composeRecordingRunner)
	runner.commands = nil
	if _, err = m.Control(context.Background(), HardwalletControlParams{Action: "start"}, false); err == nil {
		t.Fatal("altered snapshot started")
	}
	if len(runner.commands) != 0 {
		t.Fatal("tampered snapshot reached execution")
	}
}
func TestHardwalletDryRunDoesNotWrite(t *testing.T) {
	m := testHardwalletManager(t)
	if _, err := m.Control(context.Background(), HardwalletControlParams{Action: "ensure", Source: "local"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.Paths.Root); !os.IsNotExist(err) {
		t.Fatal("dry run created files")
	}
}

func TestHardwalletEnsureRenewsForChangedAddress(t *testing.T) {
	m := testHardwalletManager(t)
	ctx := context.Background()
	if _, err := m.Control(ctx, HardwalletControlParams{Action: "ensure", Source: "club"}, false); err != nil {
		t.Fatal(err)
	}
	token, _ := os.ReadFile(m.Paths.Token)
	m.BindIP = func() (string, error) { return "100.100.1.2", nil }
	if _, err := m.Control(ctx, HardwalletControlParams{Action: "ensure"}, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(m.Paths.Certificate)
	block, _ := pem.Decode(raw)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || cert.VerifyHostname("100.100.1.2") != nil {
		t.Fatal("app certificate was not renewed for changed Tailscale address")
	}
	after, _ := os.ReadFile(m.Paths.Token)
	if string(after) != string(token) {
		t.Fatal("certificate renewal changed proxy token")
	}
}
func TestHardwalletClosedProtocol(t *testing.T) {
	for _, raw := range []string{`{"action":"shell"}`, `{"action":"ensure","source":"https://evil"}`, `{"action":"start","source":"club"}`, `{"action":"ensure","path":"/etc"}`, `{"action":"ensure","image":"evil"}`} {
		req := Request{Version: 1, RequestID: "test", Operation: OperationHardwalletControl, Params: json.RawMessage(raw)}
		if ValidateRequest(req) == nil {
			t.Fatal(raw)
		}
	}
	if err := validateHardwalletControl(HardwalletControlParams{Action: "ensure", Source: "club"}); err != nil {
		t.Fatal(err)
	}
	if !mutatingOperation(OperationHardwalletControl) || mutatingOperation(OperationHardwalletStatus) {
		t.Fatal("incorrect mutation boundary")
	}
}
func TestHardwalletFirewallOnlyAuthorizesItsBridge(t *testing.T) {
	m := testHardwalletManager(t)
	r := m.Runner.(*composeRecordingRunner)
	r.hook = func(path string, args []string) (string, error, bool) {
		if path == ufwPath && len(args) == 1 {
			return "Status: active", nil, true
		}
		return "", nil, false
	}
	if err := m.firewallRule(context.Background(), "br-0123456789ab", false); err != nil {
		t.Fatal(err)
	}
	command := strings.Join(r.commands[len(r.commands)-1].args, " ")
	if command != "allow in on br-0123456789ab to any port 8443 proto tcp" {
		t.Fatal(command)
	}
}

func TestHardwalletFailedSourceStopPreservesSettings(t *testing.T) {
	m := testHardwalletManager(t)
	ctx := context.Background()
	if _, err := m.ensure(ctx, "local", false); err != nil {
		t.Fatal(err)
	}
	r := m.Runner.(*composeRecordingRunner)
	r.hook = func(path string, args []string) (string, error, bool) {
		return "", errors.New("Docker unavailable"), true
	}
	if _, err := m.ensure(ctx, "club", false); err == nil {
		t.Fatal("source changed despite failed stop")
	}
	s, err := m.settings()
	if err != nil || s.Source != "local" {
		t.Fatal("previous source was lost", err)
	}
}

func TestHardwalletElectrsRemovalChecksRuntimeAttachments(t *testing.T) {
	m := testHardwalletManager(t)
	ctx := context.Background()
	if _, err := m.ensure(ctx, "club", false); err != nil {
		t.Fatal(err)
	}
	r := m.Runner.(*composeRecordingRunner)
	for _, tc := range []struct {
		output  string
		err     error
		blocked bool
	}{
		{"", nil, false}, {"0123456789ab", nil, true}, {"", errors.New("Docker unavailable"), true},
	} {
		r.hook = func(path string, args []string) (string, error, bool) {
			if !strings.Contains(strings.Join(args, " "), "ps -a --filter label=com.docker.compose.project=brln-hardwallet --filter network=electrs_default") {
				t.Fatal("runtime lookup does not cover stopped containers")
			}
			return tc.output, tc.err, true
		}
		err := hardwalletAllowsElectrsRemoval(ctx, r, m.Paths.Root)
		if (err != nil) != tc.blocked {
			t.Fatal("incorrect removal decision", err)
		}
	}
}

func TestHardwalletCertificateExpiryIsRejected(t *testing.T) {
	m := testHardwalletManager(t)
	if _, err := m.ensure(context.Background(), "club", false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(m.Paths.Certificate)
	block, _ := pem.Decode(raw)
	cert, _ := x509.ParseCertificate(block.Bytes)
	if !m.tlsCurrent(raw, "100.100.1.1", time.Now()) {
		t.Fatal("current certificate rejected")
	}
	if m.tlsCurrent(raw, "100.100.1.1", cert.NotAfter.Add(time.Second)) {
		t.Fatal("expired certificate accepted")
	}
}
