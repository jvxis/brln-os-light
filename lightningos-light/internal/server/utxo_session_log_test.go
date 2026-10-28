package server

import (
	"bytes"
	"context"
	"log"
	"net/http/httptest"
	"strings"
	"testing"

	"lightningos-light/internal/logsafe"
)

func TestUtxoLockDiagnosticsNeverExposeSessionCookie(t *testing.T) {
	const cookie = "fictional-private-session-cookie"
	var output bytes.Buffer
	s := &Server{logger: log.New(logsafe.NewWriter(&output), "", 0)}
	r := httptest.NewRequest("POST", "/api/onchain/utxos/lock", nil)
	r = r.WithContext(context.WithValue(r.Context(), authSessionContextKey, authSessionSnapshot{ID: cookie}))
	for _, action := range []string{"lock", "unlock"} {
		s.auditUtxoLockAction(r, action, []string{"fictional-outpoint:0"}, 60)
		s.auditUtxoLockAction(r, action, []string{"fictional-outpoint:0"}, 0)
	}
	text := output.String()
	if strings.Contains(text, cookie) {
		t.Fatal("UTXO diagnostic exposes a bearer session cookie")
	}
	if strings.Count(text, auditCorrelationID(cookie)) != 4 || !strings.Contains(text, "utxo lock") || !strings.Contains(text, "utxo unlock") || !strings.Contains(text, "fictional-outpoint:0") || !strings.Contains(text, "expiry_sec=60") {
		t.Fatal("safe session correlation or diagnostic context lost")
	}
	output.Reset()
	s.auditUtxoLockAction(httptest.NewRequest("POST", "/", nil), "lock", nil, 0)
	if !strings.Contains(output.String(), "session=anonymous") {
		t.Fatal("anonymous diagnostics changed")
	}
}
