package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const redactionConfigFixture = "[Application Options]\nalias=Config Fixture\nwallet-unlock-password-file=/srv/custom/password\n[Bitcoind]\nbitcoind.rpcuser=fixture-rpc-user\nbitcoind.rpcpass=fixture-rpc-password\n# old token=fixture-comment-token\nbitcoind.rpchost=127.0.0.1:8332\n"

func TestLNDSecretKeepReplaceClearAndRestore(t *testing.T) {
	public, options, _ := redactLNDConfig(redactionConfigFixture)
	for _, secret := range []string{"fixture-rpc-user", "fixture-rpc-password", "fixture-comment-token"} {
		if strings.Contains(public, secret) {
			t.Fatal("public config leaked a secret")
		}
	}
	if len(options) < 2 {
		t.Fatal("sensitive options were not described")
	}
	changed := strings.Replace(public, "alias=Config Fixture", "alias=Edited", 1)
	kept, err := restoreLNDConfigSecrets(redactionConfigFixture, changed, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"bitcoind.rpcuser=fixture-rpc-user", "bitcoind.rpcpass=fixture-rpc-password", "# old token=fixture-comment-token", "wallet-unlock-password-file=/srv/custom/password", "alias=Edited"} {
		if !strings.Contains(kept, line) {
			t.Fatal("keep lost a stored value or an unrelated option")
		}
	}
	if strings.Contains(kept, lndSecretPlaceholder) || strings.Contains(kept, "[LOS protected") {
		t.Fatal("placeholder reached stored config")
	}
	withoutRows := strings.Join(strings.FieldsFunc(changed, func(r rune) bool { return r == '\n' }), "\n")
	filtered := []string{}
	for _, line := range strings.Split(withoutRows, "\n") {
		if !strings.Contains(line, lndSecretPlaceholder) {
			filtered = append(filtered, line)
		}
	}
	omitted, err := restoreLNDConfigSecrets(redactionConfigFixture, strings.Join(filtered, "\n"), nil)
	if err != nil || !strings.Contains(omitted, "bitcoind.rpcpass=fixture-rpc-password") {
		t.Fatal("omitting a secret row erased its stored value")
	}
	replacement := "replacement with spaces and a \"quote\""
	replaced, err := restoreLNDConfigSecrets(redactionConfigFixture, changed, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "replace", Value: &replacement}})
	if err != nil {
		t.Fatal(err)
	}
	values := lndPolicyValues(replaced)
	if len(values["bitcoind.rpcpass"]) != 1 || values["bitcoind.rpcpass"][0] != replacement {
		t.Fatal("replacement was not encoded as one INI string")
	}
	cleared, err := restoreLNDConfigSecrets(redactionConfigFixture, changed, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "clear"}})
	if err != nil || len(lndPolicyValues(cleared)["bitcoind.rpcpass"]) != 0 {
		t.Fatal("explicit clear did not remove the option")
	}
	clearedPublic, _, _ := redactLNDConfig(cleared)
	restored, err := restoreLNDConfigSecrets(cleared, clearedPublic, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "replace", Value: &replacement}})
	if err != nil || len(lndPolicyValues(restored)["bitcoind.rpcpass"]) != 1 {
		t.Fatal("a cleared common credential could not be restored")
	}
}

func TestLNDSecretMutationRejectsAmbiguity(t *testing.T) {
	public, _, _ := redactLNDConfig(redactionConfigFixture)
	newline := "bad\nno-macaroons=true"
	plain := "must-not-be-used"
	for _, tc := range []struct {
		submitted string
		updates   map[string]lndSecretUpdate
	}{
		{strings.Replace(public, lndSecretPlaceholder, "typed-plaintext", 1), nil},
		{public, map[string]lndSecretUpdate{"unknown/password": {Action: "clear"}}},
		{public, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "replace", Value: &newline}}},
		{public, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "keep", Value: &plain}}},
		{public, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "clear", Value: &plain}}},
		{public, map[string]lndSecretUpdate{"bitcoind/bitcoind.rpcpass": {Action: "replace"}}},
	} {
		if _, err := restoreLNDConfigSecrets(redactionConfigFixture, tc.submitted, tc.updates); err == nil {
			t.Fatal("ambiguous or injectable secret mutation was accepted")
		}
	}
}

func TestLNDConfigRevisionIsOpaqueAndGuardsRawWrites(t *testing.T) {
	auth := &AuthService{enabled: true, now: time.Now, sessions: make(map[string]*authSession)}
	snapshot, _ := auth.newSession()
	auth.sessions[snapshot.ID].ReauthScopes[authScopeLNDConfig] = time.Now().Add(time.Minute)
	stored := []byte(redactionConfigFixture)
	writes := 0
	server := &Server{auth: auth, logger: log.New(io.Discard, "", 0), lndConfigOps: &lndConfigOperations{read: func() ([]byte, error) { return bytes.Clone(stored), nil }, write: func(value []byte) error { stored = bytes.Clone(value); writes++; return nil }}}
	first, err := server.readLNDConfigSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	again, _ := server.lndConfigRevision(stored)
	sum := sha256.Sum256(stored)
	if first.revision != again || first.revision == hex.EncodeToString(sum[:]) || len(first.revision) != 64 {
		t.Fatal("config revision is not a stable opaque server token")
	}
	other, _ := (&Server{}).lndConfigRevision(stored)
	if first.revision == other {
		t.Fatal("different Manager instances reused a config revision key")
	}
	call := func(revision string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"raw_user_conf": strings.Replace(first.public, "alias=Config Fixture", "alias=After", 1), "config_revision": revision, "apply_now": false})
		r := httptest.NewRequest("POST", "/api/lnd/config/raw", bytes.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, snapshot))
		w := httptest.NewRecorder()
		server.handleLNDConfigRaw(w, r)
		return w
	}
	if call("").Code != 409 || writes != 0 {
		t.Fatal("missing revision changed config")
	}
	if call(first.revision).Code != 200 || writes != 1 {
		t.Fatal("current revision could not save safely")
	}
	if call(first.revision).Code != 409 || writes != 1 {
		t.Fatal("stale advanced editor overwrote a newer config")
	}
	if strings.Contains(string(stored), lndSecretPlaceholder) || !strings.Contains(string(stored), "bitcoind.rpcpass=fixture-rpc-password") {
		t.Fatal("raw handler wrote a placeholder or erased a credential")
	}
}
