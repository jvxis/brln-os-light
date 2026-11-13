//go:build linux

package privileged

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// Separate real-network observation from the rollback/reload fault-injection
// suite. No production node, credentials, or insecure TLS verification.
func TestTorLNDRESTSinglePublication(t *testing.T) {
	if os.Getenv("LOS_TOR_REST_SINGLE_PUBLICATION") != "1" {
		t.Skip("requires authorized disposable regtest and real Tor network")
	}
	marker, err := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if err != nil || strings.TrimSpace(string(marker)) != "LOS_REGTEST_ONLY" || os.Geteuid() != 0 {
		t.Fatal("not a root regtest fixture")
	}
	original, info, err := readTorrc()
	if err != nil {
		t.Fatal(err)
	}
	if _, found := findLNDRESTService(original); found {
		t.Fatal("fixture contains REST onion")
	}
	manager := NewNativeTorLNDRESTManager(&liveTorRunner{})
	manager.lndUnit = "los-regtest-alice.service"
	defer func() {
		if err := writeTorrc(original, info); err != nil {
			t.Error(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := manager.reloadTor(ctx); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	state, err := manager.Ensure(ctx, false)
	if err != nil || state.Status != "configured" {
		t.Fatalf("ensure: %v status=%s", err, state.Status)
	}
	cert, err := os.ReadFile("/var/lib/los-regtest/alice/tls.cert")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("invalid public certificate")
	}
	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:9050", nil, &net.Dialer{Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext,
		TLSClientConfig:     &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: 15 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 45 * time.Second}
	start := time.Now()
	for time.Since(start) < 6*time.Minute {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://"+state.Hostname+":8080/v1/getinfo", nil)
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if readErr == nil && resp.StatusCode >= 400 && strings.Contains(strings.ToLower(string(body)), "macaroon") {
				t.Logf("PASS single publication: verified TLS and macaroon boundary after %s", time.Since(start).Round(time.Second))
				return
			}
			t.Fatalf("unexpected REST response status=%d read=%v", resp.StatusCode, readErr)
		}
		t.Logf("elapsed=%s %s", time.Since(start).Round(time.Second), strings.ReplaceAll(err.Error(), state.Hostname, "[test-onion]"))
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}
	t.Fatal("single publication did not become reachable within observation window")
}
