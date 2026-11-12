//go:build linux

package privileged

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// Explicit opt-in, root-only, isolated regtest VM. Never enable on a real node.
// Restores torrc; generated test onions remain on disk for evidence.
type liveTorRunner struct{ failReload bool }

func (r *liveTorRunner) Run(ctx context.Context, binary string, args ...string) (string, error) {
	if r.failReload && len(args) > 0 && args[0] == "reload" {
		r.failReload = false
		return "", errors.New("injected reload failure")
	}
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	return string(out), err
}
func TestTorLNDRESTLive(t *testing.T) {
	if os.Getenv("LOS_TOR_REST_LIVE") != "1" {
		t.Skip("requires explicitly authorized disposable regtest VM")
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
		t.Fatal("fixture already contains REST onion; refusing overwrite")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	runner := &liveTorRunner{}
	manager := NewNativeTorLNDRESTManager(runner)
	manager.lndUnit = "los-regtest-alice.service"
	defer func() {
		if err := writeTorrc(original, info); err != nil {
			t.Error(err)
		}
		if err := manager.reloadTor(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	state, err := manager.Status(ctx, false)
	if err != nil || state.Status != "absent" || !state.RESTReachable {
		t.Fatalf("baseline: %+v %v", state, err)
	}
	runner.failReload = true
	if _, err := manager.Ensure(ctx, false); err == nil {
		t.Fatal("injected reload failure accepted")
	}
	restored, _, _ := readTorrc()
	if restored != original {
		t.Fatal("reload rollback did not preserve torrc")
	}
	t.Log("reload failure restored original torrc")
	first, err := manager.Ensure(ctx, false)
	if err != nil || first.Status != "configured" || !first.Managed || !validOnionHostname(first.Hostname) {
		t.Fatalf("create: %+v %v", first, err)
	}
	once, _, _ := readTorrc()
	second, err := manager.Ensure(ctx, false)
	if err != nil || first.Hostname != second.Hostname {
		t.Fatal("idempotence", err)
	}
	twice, _, _ := readTorrc()
	if once != twice || !strings.HasPrefix(twice, original) {
		t.Fatal("duplicate block or changed original")
	}
	t.Log("create/retry retained onion and unrelated configuration")
	if os.Getenv("LOS_TOR_REST_NETWORK") == "1" {
		// The fixture explicitly trusts Alice's public cert and its loopback SAN.
		// No insecure TLS bypass or macaroon is used to test the Tor transport.
		cert, err := os.ReadFile("/var/lib/los-regtest/alice/tls.cert")
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cert) {
			t.Fatal("invalid fixture public certificate")
		}
		dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:9050", nil, &net.Dialer{Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{DialTLSContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			secure := tls.Client(conn, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
			if err := secure.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return secure, nil
		}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
		var reachable bool
		for attempt := 0; attempt < 6; attempt++ {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+first.Hostname+":8080/v1/getinfo", nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Log("Tor attempt:", strings.ReplaceAll(err.Error(), first.Hostname, "[test-onion]"))
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode >= 400 && strings.Contains(strings.ToLower(string(body)), "macaroon") {
				reachable = true
				break
			}
		}
		if !reachable {
			t.Fatal("onion REST did not return authenticated LND boundary through Tor")
		}
		t.Log("onion reached LND REST via SOCKS5 with verified TLS; request without macaroon rejected")
	}
	// Reuse the same directory as an operator-managed service, rather than copy keys.
	manualDir := "/var/lib/tor/los-rest-manual-test-" + time.Now().UTC().Format("20060102150405")
	if _, err := os.Lstat(manualDir); !os.IsNotExist(err) {
		t.Fatal("manual fixture directory already exists")
	}
	manual := original + "\nHiddenServiceDir " + manualDir + "/\nHiddenServicePort 8080 127.0.0.1:8080\n"
	if err := writeTorrc(manual, info); err != nil {
		t.Fatal(err)
	}
	state, err = manager.Ensure(ctx, false)
	if err != nil || state.Status != "configured" || state.Managed {
		t.Fatalf("manual: %+v %v", state, err)
	}
	after, _, _ := readTorrc()
	if after != manual {
		t.Fatal("manual service rewritten")
	}
	t.Log("manual onion reused without adding managed block")
	// Real Tor must reject a bad candidate before replacing the live config.
	if err := writeValidatedTorrc(manual+"\nNotATorOption 1\n", info, func(p string) error { return manager.verifyTorConfigAt(ctx, p) }); err == nil {
		t.Fatal("Tor accepted invalid candidate")
	}
	after, _, _ = readTorrc()
	if after != manual {
		t.Fatal("invalid candidate changed live file")
	}
	t.Log("invalid candidate preserved live configuration")
}
