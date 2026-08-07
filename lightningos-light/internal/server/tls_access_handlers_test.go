package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/config"
)

func TestTLSAccessInfoAndDownloads(t *testing.T) {
	t.Parallel()

	server, caPEM := testTLSServer(t)

	infoRecorder := httptest.NewRecorder()
	server.handleTLSAccessInfo(infoRecorder, httptest.NewRequest(http.MethodGet, "/api/tls/info", nil))
	if infoRecorder.Code != http.StatusOK {
		t.Fatalf("info status=%d body=%s", infoRecorder.Code, infoRecorder.Body.String())
	}
	var info tlsAccessInfoResponse
	if err := json.Unmarshal(infoRecorder.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Available {
		t.Fatal("expected local CA to be available")
	}
	if info.LocalName != "test-node.local" {
		t.Fatalf("local_name=%q", info.LocalName)
	}
	if info.PreferredURL != "https://test-node.local:8443" {
		t.Fatalf("preferred_url=%q", info.PreferredURL)
	}
	if len(info.IPURLs) != 1 || info.IPURLs[0] != "https://192.168.68.150:8443" {
		t.Fatalf("ip_urls=%v", info.IPURLs)
	}
	if info.CAFingerprintSHA256 == "" || info.CADownloadURL != "/api/tls/ca" || info.WindowsInstallerURL != "/api/tls/windows" {
		t.Fatalf("incomplete info response: %+v", info)
	}

	caRecorder := httptest.NewRecorder()
	server.handleTLSCADownload(caRecorder, httptest.NewRequest(http.MethodGet, "/api/tls/ca", nil))
	if caRecorder.Code != http.StatusOK {
		t.Fatalf("CA status=%d body=%s", caRecorder.Code, caRecorder.Body.String())
	}
	if string(caRecorder.Body.Bytes()) != string(caPEM) {
		t.Fatal("CA download did not return the installed public certificate")
	}
	if strings.Contains(caRecorder.Body.String(), "PRIVATE KEY") {
		t.Fatal("CA download exposed private key material")
	}

	windowsRecorder := httptest.NewRecorder()
	server.handleTLSWindowsInstallerDownload(windowsRecorder, httptest.NewRequest(http.MethodGet, "/api/tls/windows", nil))
	if windowsRecorder.Code != http.StatusOK {
		t.Fatalf("Windows installer status=%d body=%s", windowsRecorder.Code, windowsRecorder.Body.String())
	}
	script := windowsRecorder.Body.String()
	for _, expected := range []string{"certutil.exe", "current Windows user", "test-node.local:8443", strings.ReplaceAll(info.CAFingerprintSHA256, ":", "")} {
		if !strings.Contains(script, expected) {
			t.Fatalf("Windows installer missing %q", expected)
		}
	}
}

func TestTLSAccessEndpointsRemainPublic(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/tls/info", "/api/tls/ca", "/api/tls/windows"} {
		if authRequiresSession(path) {
			t.Fatalf("%s unexpectedly requires an authenticated session", path)
		}
	}
}

func testTLSServer(t *testing.T) (*Server, []byte) {
	t.Helper()
	dir := t.TempDir()
	now := time.Now().Add(-time.Minute)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "LightningOS Local CA - test-node"},
		NotBefore:             now,
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-node.local"},
		NotBefore:    now,
		NotAfter:     now.Add(90 * 24 * time.Hour),
		DNSNames:     []string{"localhost", "test-node", "test-node.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("192.168.68.150")},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})

	caPath := filepath.Join(dir, "local-ca.crt")
	serverPath := filepath.Join(dir, "server.crt")
	if err := os.WriteFile(caPath, caPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(serverPath, serverPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	return &Server{cfg: &config.Config{Server: config.ServerConfig{
		Port:    8443,
		TLSCert: serverPath,
	}}}, caPEM
}
