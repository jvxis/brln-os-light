package logsafe

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestRedactCoreDiagnosticCredentials(t *testing.T) {
	for _, tc := range []struct {
		value   string
		secrets []string
	}{
		{`database connection failed postgres://fixture-user:fixture-password@db.local/lnd`, []string{"fixture-user", "fixture-password"}},
		{`rpc authentication failed bitcoind.rpcuser=private-user bitcoind.rpcpass="private value with spaces"`, []string{"private-user", "private value with spaces"}},
		{`remote failed {"password":"private\"value","macaroon":"private-macaroon"}`, []string{"private", "private-macaroon"}},
		{`authentication failed Authorization: Bearer private-token`, []string{"private-token"}},
		{`authentication failed Cookie: __Host-lightningos_session=private-session; csrf=private-csrf`, []string{"private-session", "private-csrf"}},
		{`notification failed https://api.telegram.org/bot123456789:abcdefghijklmnopqrstuv_xyz/getUpdates`, []string{"123456789:abcdefghijklmnopqrstuv_xyz"}},
	} {
		safe := Redact(tc.value)
		for _, secret := range tc.secrets {
			if strings.Contains(safe, secret) {
				t.Fatalf("credential not redacted for diagnostic type")
			}
		}
		if !strings.Contains(safe, "failed") {
			t.Fatal("diagnostic category lost")
		}
		if Redact(safe) != safe {
			t.Fatal("redaction must be idempotent")
		}
	}
	var output bytes.Buffer
	logger := log.New(NewWriter(&output), "service: ", 0)
	logger.Print("read failed: -----BEGIN PRIVATE KEY-----\nfictional-key-bytes\n-----END PRIVATE KEY-----")
	if strings.Contains(output.String(), "fictional-key-bytes") {
		t.Fatal("multiline private material reached logger output")
	}
}
