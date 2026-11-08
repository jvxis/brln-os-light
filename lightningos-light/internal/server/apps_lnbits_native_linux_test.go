package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/privileged"
)

type nativeLNbitsMigrationRPC struct {
	*lndclient.Client
	broker   *privileged.ComposeAppManager
	failBake bool
}

func (r *nativeLNbitsMigrationRPC) DeleteMacaroonID(ctx context.Context, _ uint64) error {
	return r.broker.RevokeLNbitsLegacyCredential(ctx, false)
}
func (r *nativeLNbitsMigrationRPC) ValidateRevocation(ctx context.Context) error {
	return r.broker.RevokeLNbitsLegacyCredential(ctx, true)
}
func (r *nativeLNbitsMigrationRPC) BakeCustomMacaroon(ctx context.Context, req lndclient.BakeCustomMacaroonRequest) (lndclient.BakeCustomMacaroonResult, error) {
	if r.failBake {
		return lndclient.BakeCustomMacaroonResult{}, errors.New("injected interruption after native revocation")
	}
	return r.Client.BakeCustomMacaroon(ctx, req)
}

// Runs actual container lifecycle and broker implementation against regtest.
// It does not replace the separately required old-Manager upgrade-path test.
func TestLNbitsNativeDockerMigration(t *testing.T) {
	if os.Getenv("LOS_LNBITS_DOCKER") != "1" {
		t.Skip("explicit disposable Docker/regtest opt-in required")
	}
	host, _ := os.Hostname()
	marker, _ := os.ReadFile("/etc/los-regtest/LAB_ONLY")
	if os.Geteuid() != 0 || host != "los-regtest" || string(marker) != "LOS_REGTEST_ONLY\n" {
		t.Fatal("not the isolated root regtest fixture")
	}
	const native = "/var/lib/los-regtest/alice"
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	client := lndclient.New(&config.Config{LND: config.LNDConfig{GRPCHost: "127.0.0.1:10009", TLSCertPath: native + "/tls.cert", AdminMacaroonPath: native + "/data/chain/bitcoin/regtest/admin.macaroon"}}, log.New(io.Discard, "", 0))
	defer client.Close()
	state, err := client.FinancialIntentNodeState(ctx)
	if err != nil || state == nil || len(state.Chains) != 1 || state.Chains[0].Network != "regtest" {
		t.Fatal("regtest RPC guard failed")
	}
	cmd := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "name=^lnbits$")
	out, err := cmd.Output()
	if err != nil || len(bytes.TrimSpace(out)) != 0 {
		t.Fatal("requires no existing LNbits container")
	}
	root, err := os.MkdirTemp("/var/lib/los-regtest", "lnbits-0555-docker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Retained isolated test data:", root)
	paths := lnbitsPaths{Root: filepath.Join(root, "apps", "lnbits"), DataDir: filepath.Join(root, "data", "lnbits", "data"), LndDir: filepath.Join(root, "data", "lnbits", "lnd")}
	paths.ComposePath = filepath.Join(paths.Root, appmanifest.LNbitsComposeFile)
	paths.EnvPath = filepath.Join(paths.Root, appmanifest.LNbitsEnvFile)
	paths.MacaroonPath = filepath.Join(paths.LndDir, appmanifest.LNbitsMacaroonFile)
	if err := ensureLnbitsPaths(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ComposePath, []byte(lnbitsComposeContents(paths)), 0640); err != nil {
		t.Fatal(err)
	}
	if err := ensureLnbitsEnv(paths); err != nil {
		t.Fatal(err)
	}
	lndRoot := filepath.Join(root, "native-material")
	if err := os.MkdirAll(filepath.Join(lndRoot, "data", "chain", "bitcoin", "mainnet"), 0700); err != nil {
		t.Fatal(err)
	}
	protected := map[string][]byte{}
	for _, pair := range [][2]string{{"tls.cert", "tls.cert"}, {"tls.key", "tls.key"}, {"lnd.conf", "lnd.conf"}, {"data/chain/bitcoin/regtest/admin.macaroon", "data/chain/bitcoin/mainnet/admin.macaroon"}} {
		raw, e := os.ReadFile(filepath.Join(native, pair[0]))
		if e != nil {
			t.Fatal(e)
		}
		protected[filepath.Join(native, pair[0])] = raw
		if e := os.WriteFile(filepath.Join(lndRoot, pair[1]), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	broker := &privileged.ComposeAppManager{Runner: &privileged.ExecCommandRunner{}, AppsRoot: filepath.Join(root, "apps"), AppsDataRoot: filepath.Join(root, "data"), PrivilegedAppsRoot: filepath.Join(root, "private"), LNDDataRoot: lndRoot, LNDConfigPath: filepath.Join(lndRoot, "lnd.conf")}
	rpc := &nativeLNbitsMigrationRPC{Client: client, broker: broker}
	failStop := false
	runtime := lnbitsCredentialRuntime{inspect: func(c context.Context) (string, error) {
		s, e := broker.Inspect(c, appmanifest.LNbitsID)
		return s.Status, e
	}, lifecycle: func(c context.Context, action string) error {
		if action == "stop" && failStop {
			return errors.New("injected stop failure")
		}
		return broker.Lifecycle(c, appmanifest.LNbitsID, privileged.AppLifecycleAction(action), false)
	}}
	// Only this test's app is stopped/removed; its data and native lab remain.
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if e := broker.Remove(c, appmanifest.LNbitsID, false); e != nil {
			t.Error("test container cleanup failed:", e)
		}
	})
	var testRoots []uint64
	defer func() {
		for _, id := range testRoots {
			c, done := context.WithTimeout(context.Background(), 10*time.Second)
			if e := client.DeleteMacaroonID(c, id); e != nil {
				t.Error("test root cleanup:", e)
			}
			done()
		}
	}()
	read := func() []byte {
		t.Helper()
		raw, e := os.ReadFile(paths.MacaroonPath)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	waitApp := func() {
		t.Helper()
		hc := http.Client{Timeout: 5 * time.Second}
		until := time.Now().Add(90 * time.Second)
		for time.Now().Before(until) {
			r, e := hc.Get("http://127.0.0.1:5000/")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			time.Sleep(time.Second)
		}
		t.Fatal("LNbits HTTP readiness timeout")
	}
	api := func(method, path, key string, payload any) map[string]any {
		t.Helper()
		var body io.Reader
		if payload != nil {
			raw, e := json.Marshal(payload)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(raw)
		}
		req, e := http.NewRequestWithContext(ctx, method, "http://127.0.0.1:5000"+path, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-Api-Key", key)
		}
		r, e := (&http.Client{Timeout: 45 * time.Second}).Do(req)
		if e != nil {
			t.Fatal("LNbits API transport failed")
		}
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			t.Fatalf("LNbits API %s status %d", path, r.StatusCode)
		}
		var result map[string]any
		if json.NewDecoder(r.Body).Decode(&result) != nil {
			t.Fatal("invalid API response")
		}
		return result
	}
	checkFunding := func() {
		t.Helper()
		code := `import os,httpx
with httpx.Client(verify=os.environ['LND_REST_CERT'],timeout=20) as c:
 h={'Grpc-Metadata-macaroon':open(os.environ['LND_REST_MACAROON'],'rb').read().hex()}
 for path in ['v1/getinfo','v1/balance/channels']:
  r=c.get(os.environ['LND_REST_ENDPOINT'].rstrip('/')+'/'+path,headers=h)
  assert r.status_code==200, 'funding read rejected'
print('funding TLS/auth reads passed')`
		if _, e := broker.Runner.Run(ctx, "/usr/bin/docker", "exec", "lnbits", "/app/.venv/bin/python", "-c", code); e != nil {
			t.Fatal("container funding validation:", e)
		}
	}
	if err := ensureLNbitsWalletCredential(ctx, paths, rpc, runtime); err != nil {
		t.Fatal("fresh credential:", err)
	}
	id, _, err := lndclient.InspectMacaroon(read())
	if err != nil {
		t.Fatal(err)
	}
	testRoots = append(testRoots, id)
	if err := runtime.lifecycle(ctx, "start"); err != nil {
		t.Fatal("fresh start:", err)
	}
	waitApp()
	checkFunding()
	t.Log("fresh clean-data LNbits start and funding reads passed")
	passwordBytes := make([]byte, 20)
	if _, e := rand.Read(passwordBytes); e != nil {
		t.Fatal(e)
	}
	password := hex.EncodeToString(passwordBytes)
	api("PUT", "/api/v1/auth/first_install", "", map[string]any{"username": "losregtest", "password": password, "password_repeat": password})
	account := api("POST", "/api/v1/account", "", map[string]any{"name": "LOS regtest migration"})
	key, ok := account["adminkey"].(string)
	if !ok || key == "" {
		t.Fatal("wallet key unavailable")
	}
	bobBase := "/var/lib/los-regtest/bob"
	bob := lndclient.New(&config.Config{LND: config.LNDConfig{GRPCHost: "127.0.0.1:11009", TLSCertPath: bobBase + "/tls.cert", AdminMacaroonPath: bobBase + "/data/chain/bitcoin/regtest/admin.macaroon"}}, log.New(io.Discard, "", 0))
	defer bob.Close()
	bs, e := bob.FinancialIntentNodeState(ctx)
	if e != nil || bs == nil || len(bs.Chains) != 1 || bs.Chains[0].Network != "regtest" {
		t.Fatal("Bob guard failed")
	}
	receive := func() {
		t.Helper()
		inv := api("POST", "/api/v1/payments", key, map[string]any{"out": false, "amount": 2000, "memo": "LOS regtest validation"})
		bolt, ok := inv["bolt11"].(string)
		if !ok {
			bolt, ok = inv["payment_request"].(string)
		}
		if !ok {
			t.Fatal("missing invoice")
		}
		hash, ok := inv["payment_hash"].(string)
		if !ok {
			t.Fatal("missing payment hash")
		}
		if e := bob.PayInvoice(ctx, bolt, nil, 10); e != nil {
			t.Fatal("regtest incoming payment failed:", e)
		}
		for i := 0; i < 30; i++ {
			p := api("GET", "/api/v1/payments/"+hash, key, nil)
			if p["paid"] == true {
				return
			}
			time.Sleep(time.Second)
		}
		t.Fatal("LNbits incoming tracking failed")
	}
	receive()
	invoice, e := bob.CreateInvoice(ctx, 1000, "LOS regtest outgoing", 600, nil)
	if e != nil {
		t.Fatal(e)
	}
	paid := api("POST", "/api/v1/payments", key, map[string]any{"out": true, "bolt11": invoice.PaymentRequest})
	hash, ok := paid["payment_hash"].(string)
	if !ok {
		t.Fatal("outgoing payment hash missing")
	}
	if api("GET", "/api/v1/payments/"+hash, key, nil)["paid"] != true {
		t.Fatal("LNbits outgoing tracking failed")
	}
	beforeWallet := api("GET", "/api/v1/wallet", key, nil)
	for _, scenario := range []string{"running", "stopped", "stop-failure", "interrupted-bake"} {
		t.Run(scenario, func(t *testing.T) {
			if err := runtime.lifecycle(ctx, "stop"); err != nil {
				t.Fatal(err)
			}
			ids, e := client.ListMacaroonIDs(ctx)
			if e != nil {
				t.Fatal(e)
			}
			id, e := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			testRoots = append(testRoots, id)
			r, e := client.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{RootKeyID: id, Permissions: lnbitsLegacyPermissions()})
			if e != nil {
				t.Fatal(e)
			}
			old, e := hex.DecodeString(r.MacaroonHex)
			if e != nil {
				t.Fatal(e)
			}
			if e := writeLNbitsPrivateFile(paths.MacaroonPath, old); e != nil {
				t.Fatal(e)
			}
			if e := runtime.lifecycle(ctx, "start"); e != nil {
				t.Fatal(e)
			}
			waitApp()
			if scenario == "stopped" {
				if e := runtime.lifecycle(ctx, "stop"); e != nil {
					t.Fatal(e)
				}
			}
			failStop = scenario == "stop-failure"
			rpc.failBake = scenario == "interrupted-bake"
			e = ensureLNbitsWalletCredential(ctx, paths, rpc, runtime)
			if failStop || rpc.failBake {
				if e == nil {
					t.Fatal("injected failure was ignored")
				}
				valid := client.AuthenticateMacaroon(ctx, old, lnbitsLegacyPermissions()) == nil
				if failStop && !valid {
					t.Fatal("credential revoked before successful stop")
				}
				if rpc.failBake && valid {
					t.Fatal("legacy credential restored after interruption")
				}
				failStop = false
				rpc.failBake = false
				e = ensureLNbitsWalletCredential(ctx, paths, rpc, runtime)
			}
			if e != nil {
				t.Fatal("migration/retry:", e)
			}
			if client.AuthenticateMacaroon(ctx, old, lnbitsLegacyPermissions()) == nil {
				t.Fatal("old copy remains valid")
			}
			if e := client.AuthenticateMacaroon(ctx, read(), lnbitsMacaroonPermissions()); e != nil {
				t.Fatal("new credential invalid")
			}
			s, e := runtime.inspect(ctx)
			if e != nil {
				t.Fatal(e)
			}
			want := "running"
			if scenario == "stopped" {
				want = "stopped"
			}
			if s != want {
				t.Fatalf("status %s want %s", s, want)
			}
			if want == "running" {
				waitApp()
				checkFunding()
			}
			if _, e := os.Stat(filepath.Join(paths.DataDir, "database.sqlite3")); e != nil {
				t.Fatal("database missing")
			}
			if want == "running" {
				afterWallet := api("GET", "/api/v1/wallet", key, nil)
				if afterWallet["id"] != beforeWallet["id"] || afterWallet["balance"] != beforeWallet["balance"] {
					t.Fatal("wallet identity or balance changed during migration")
				}
			}
			if e := ensureLNbitsWalletCredential(ctx, paths, rpc, runtime); e != nil {
				t.Fatal("repeat:", e)
			}
		})
	}
	receive()
	t.Log("Real LNbits invoice creation, incoming subscription, outgoing payment and tracking passed; wallet/balance retained through migrations")
	for p, before := range protected {
		after, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(before, after) {
			t.Fatal("native material changed:", filepath.Base(p))
		}
	}
}
