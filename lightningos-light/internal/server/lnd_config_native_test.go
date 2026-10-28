package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
)

// This deliberately names one isolated lab. It can never select a production
// service, arbitrary config path or mainnet node through an environment value.
// The restart adapter replaces only the privileged transport; the handlers,
// file operations, LND INI parser, daemon, TLS gRPC readiness and rollback run
// for real. Do not run concurrently with other native lab mutations.
func TestLNDConfigNativeDisposable(t *testing.T) {
	if os.Getenv("LOS_LND_CONFIG_NATIVE") != "alice-regtest-disposable" {
		t.Skip("requires the isolated Alice regtest disposable and explicit opt-in")
	}
	if runtime.GOOS != "linux" || os.Getuid() != 0 {
		t.Fatal("requires isolated Linux root fixture")
	}
	mac, err := os.ReadFile("/sys/class/net/enp0s3/address")
	if err != nil || strings.TrimSpace(string(mac)) != "08:00:27:13:de:b8" {
		t.Fatal("wrong disposable VM identity")
	}
	const dir = "/var/lib/los-regtest/alice"
	const path = dir + "/lnd.conf"
	original, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(original), "bitcoin.regtest=true") || strings.Contains(string(original), "bitcoin.mainnet=true") {
		t.Fatal("requires the known regtest configuration")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal("config metadata unavailable")
	}
	logger := log.New(io.Discard, "", 0)
	client := lndclient.New(&config.Config{LND: config.LNDConfig{
		GRPCHost: "127.0.0.1:10009", TLSCertPath: dir + "/tls.cert",
		AdminMacaroonPath: dir + "/data/chain/bitcoin/regtest/admin.macaroon",
	}}, logger)
	defer client.Close()
	ops := (&Server{lnd: client}).lndConfigOperations()
	ops.path = path
	ops.read = func() ([]byte, error) { return os.ReadFile(path) }
	ops.write = func(raw []byte) error { return writeLNDConfigBytes(path, raw) }
	ops.restart = func(ctx context.Context) error {
		return exec.CommandContext(ctx, "/usr/bin/systemctl", "restart", "los-regtest-alice").Run()
	}
	ops.readyTimeout = 35 * time.Second
	ctx := context.Background()
	if state, err := ops.probe(ctx); err != nil || state != "active" {
		t.Fatal("native baseline is not active")
	}
	t.Cleanup(func() {
		if err := ops.write(original); err != nil {
			t.Error("could not restore original native config")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := ops.restart(ctx); err != nil {
			t.Error("could not restart original native service")
			return
		}
		if state, err := waitLNDConfigReadiness(ctx, ops, string(original)); err != nil || state != "active" {
			t.Error("original native service was not verified active")
		}
	})
	auth := NewAuthService(nil, logger)
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{lnd: client, logger: logger, auth: auth, lndConfigOps: &ops}
	call := func(raw bool, payload any) *httptest.ResponseRecorder {
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal("could not encode fixture request")
		}
		r := httptest.NewRequest(http.MethodPost, "/api/lnd/config", bytes.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, session))
		w := httptest.NewRecorder()
		if raw {
			s.handleLNDConfigRaw(w, r)
		} else {
			s.handleLNDConfigPost(w, r)
		}
		return w
	}
	assertFile := func(expected []byte) {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, expected) {
			t.Fatal("native file unexpectedly changed")
		}
	}
	if call(false, map[string]any{"alias": "unauthorized", "apply_now": true}).Code != 428 {
		t.Fatal("missing approval accepted")
	}
	assertFile(original)
	auth.sessions[session.ID].ReauthScopes[authScopeLNDConfig] = time.Now().Add(10 * time.Minute)
	if call(false, map[string]any{"alias": "Config Native Fixture", "color": "#123456", "min_channel_size_sat": 10000, "max_channel_size_sat": 1000000, "apply_now": true}).Code != 200 {
		t.Fatal("native structured apply failed")
	}
	state, err := ops.probe(ctx)
	if err != nil || state != "active" {
		t.Fatal("auto-unlock was not verified")
	}
	t.Log("PASS: native alias/color/channel limits and automatic wallet unlock")
	snap, err := s.readLNDConfigSnapshot()
	if err != nil {
		t.Fatal("native snapshot failed")
	}
	for _, entry := range lndConfigEntries(string(snap.raw)) {
		if lndOptionIsSecret(entry) && len(entry.value) > 3 && strings.Contains(snap.public, entry.value) {
			t.Fatal("native public config exposed credential")
		}
	}
	for _, option := range []string{"no-macaroons=true", "rpclisten=0.0.0.0:10009", "datadir=/tmp/forbidden", "wallet-unlock-password-file=/tmp/forbidden"} {
		bad := updateLNDSectionOptions(snap.public, "Application Options", []lndSectionOption{{key: strings.SplitN(option, "=", 2)[0], value: strings.SplitN(option, "=", 2)[1]}})
		if call(true, map[string]any{"raw_user_conf": bad, "config_revision": snap.revision, "apply_now": true}).Code != 400 {
			t.Fatal("native unsafe option was not rejected")
		}
		assertFile(snap.raw)
	}
	if call(false, map[string]any{"alias": "bad\nno-macaroons=true", "apply_now": true}).Code != 400 {
		t.Fatal("structured newline injection accepted")
	}
	assertFile(snap.raw)
	// Unknown legitimate user options and protected custom paths survive the
	// advanced editor's redaction/merge, then pass LND's actual INI parser.
	public := updateLNDSectionOptions(snap.public, "Application Options", []lndSectionOption{{key: "maxpendingchannels", value: "5"}})
	if call(true, map[string]any{"raw_user_conf": public, "config_revision": snap.revision, "apply_now": true}).Code != 200 {
		t.Fatal("native redacted keep apply failed")
	}
	kept, _ := os.ReadFile(path)
	for _, entry := range lndConfigEntries(string(snap.raw)) {
		if (lndProtectedPath(lndPolicyKey(entry.key)) || lndOptionIsSecret(entry)) && !strings.Contains(string(kept), entry.line) {
			t.Fatal("native preserved setting changed")
		}
	}
	if call(true, map[string]any{"raw_user_conf": public, "config_revision": snap.revision, "apply_now": true}).Code != 409 {
		t.Fatal("stale native editor overwrote config")
	}
	t.Log("PASS: native redaction, preserved credentials/custom startup path/operator option, unsafe options and stale revision")
	// A systemd restart may return success before a malformed INI option kills
	// the daemon. Readiness must detect it and verify restoration independently.
	snap, _ = s.readLNDConfigSnapshot()
	bad := updateLNDSectionOptions(snap.public, "Application Options", []lndSectionOption{{key: "los-invalid-native-fixture-option", value: "true"}})
	w := call(true, map[string]any{"raw_user_conf": bad, "config_revision": snap.revision, "apply_now": true})
	var failure struct {
		Restored bool `json:"config_restored"`
		Verified bool `json:"rollback_verified"`
	}
	if json.Unmarshal(w.Body.Bytes(), &failure) != nil || w.Code != 500 || !failure.Restored || !failure.Verified {
		t.Fatal("native startup failure was not recovered and verified")
	}
	assertFile(snap.raw)
	t.Log("PASS: actual native startup failure rolls back with fresh authenticated GetInfo")
	// Simulate only the privileged transport refusing a restart, with real
	// bytes/readback and a real native rollback restart and readiness probe.
	normalRestart := ops.restart
	attempt := 0
	ops.restart = func(ctx context.Context) error {
		attempt++
		if attempt == 1 {
			return errors.New("fixture restart refusal")
		}
		return normalRestart(ctx)
	}
	_, err = applyLNDConfig(ctx, ops, snap.raw, []byte(strings.Replace(string(snap.raw), "Config Native Fixture", "Refused Fixture", 1)), true)
	var applyErr *lndConfigApplyError
	if !errors.As(err, &applyErr) || !applyErr.RollbackVerified {
		t.Fatal("native restart refusal not recovered")
	}
	ops.restart = normalRestart
	assertFile(snap.raw)
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		t.Fatal("native config inode or permissions changed")
	}
	t.Log("PASS: native restart refusal recovery and preserved inode/permissions")
}
