package lndclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lightningos-light/internal/config"
)

func TestSharedConnectionIsReplacedAfterLNDCertificateRotation(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.cert")
	if err := os.WriteFile(certPath, testClientCertificate(t, 1), 0600); err != nil {
		t.Fatal(err)
	}
	client := New(&config.Config{LND: config.LNDConfig{
		GRPCHost:    "127.0.0.1:1",
		TLSCertPath: certPath,
	}}, nil)
	t.Cleanup(func() { _ = client.Close() })

	first, err := client.sharedConn(context.Background(), grpcRoleWalletUnlocker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, testClientCertificate(t, 2), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := client.sharedConn(context.Background(), grpcRoleWalletUnlocker)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("shared gRPC connection retained credentials for the old LND certificate")
	}
}

func testClientCertificate(t *testing.T, serial int64) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
