package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/system"
)

func TestLogQueryRequiresAuthentication(t *testing.T) {
	auth := &AuthService{enabled: true}
	recorder := httptest.NewRecorder()
	auth.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("unauthenticated query reached handler") })).ServeHTTP(recorder, httptest.NewRequest("GET", "/api/logs/query?service=lnd", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", recorder.Code)
	}
}

func TestLogQueryValidation(t *testing.T) {
	now := time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC)
	q, err := parseLogQuery(url.Values{"service": {"lnd"}, "since": {"2026-10-08T14:00:00-03:00"}, "until": {"2026-10-08T15:00:00-03:00"}}, now)
	if err != nil || q.Since.Hour() != 17 || q.Until.Hour() != 18 || q.Limit != 200 {
		t.Fatalf("timezone conversion/defaults: %+v %v", q, err)
	}
	for _, query := range []string{
		"service=unknown", "service=lnd&limit=-1", "service=lnd&limit=1001", "service=lnd&limit=abc",
		"service=lnd&since=yesterday", "service=lnd&until=2026-10-08T18:00:00Z",
		"service=lnd&since=2026-10-08T18:00:00&until=2026-10-08T19:00:00Z",
		"service=lnd&since=2026-10-08T18:00:00Z&until=2026-10-08T17:00:00Z",
		"service=lnd&since=2026-10-01T00:00:00Z&until=2026-10-09T00:00:00Z",
		"service=lnd&level=panic", "service=lnd&event=closed", "service=autofee&event=connection", "service=lnd&q=" + strings.Repeat("a", 257),
	} {
		t.Run(query, func(t *testing.T) {
			r := httptest.NewRecorder()
			(&Server{}).handleLogsQuery(r, httptest.NewRequest("GET", "/api/logs/query?"+query, nil))
			if r.Code != 400 || r.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("validation: %d %s", r.Code, r.Body.String())
			}
		})
	}
}

func TestLogQueryFiltersBeforeLimitAndKeepsContext(t *testing.T) {
	start := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	batch := system.JournalQueryResult{}
	for i := range 1500 {
		message := fmt.Sprintf("[INF] background %d", i)
		if i == 100 || i == 300 {
			message = fmt.Sprintf("[ERR] PEER: Unable to connect peer=fixture-%d", i)
		}
		batch.Entries = append(batch.Entries, system.JournalEntry{Time: start.Add(time.Duration(i) * time.Second), Message: message, Priority: "6"})
	}
	q := logQuery{Service: "lnd", Until: start.Add(time.Hour), Limit: 1, Level: "error", Event: "connection", Text: "FIXTURE"}
	result := selectLogEntries(q, "systemd:lnd", logCapabilities{Events: true, Context: true}, batch)
	if result.Matched != 2 || len(result.Entries) != 1 || !result.ResultLimited || result.Partial {
		t.Fatalf("wrong query accounting: %+v", result)
	}
	entry := result.Entries[0]
	if !strings.Contains(entry.Message, "fixture-300") || len(entry.Context) != 7 || !strings.Contains(entry.Context[0].Message, "297") || !strings.Contains(entry.Context[6].Message, "303") {
		t.Fatalf("missing older match/context: %+v", entry)
	}
	q.Since, q.Until, q.Limit = start.Add(300*time.Second), start.Add(302*time.Second), 100
	result = selectLogEntries(q, "systemd:lnd", logCapabilities{Events: true, Context: true}, batch)
	if len(result.Entries) != 1 || len(result.Entries[0].Context) != 3 {
		t.Fatalf("context escaped inclusive interval: %+v", result)
	}
}

func TestLogQueryClassification(t *testing.T) {
	// Representative messages verified against LND funding/manager.go,
	// peer/brontide.go and contractcourt/channel_arbitrator.go (v0.20.1-beta).
	for _, tc := range []struct{ message, level, event string }{
		{"[INF] CNCT: Broadcasting force close transaction abc, height=1", "info", "force_close"},
		{"[ERR] CNCT: Failed to handle remote force close: timeout", "error", "force_close"},
		{"[INF] CNCT: ChannelArbitrator(abc:0) marking channel cooperatively closed", "info", "cooperative_close"},
		{"[ERR] PEER: coop close error for channel abc: timeout", "error", "cooperative_close"},
		{"[INF] FNDG: Starting funding workflow with peer for pending_id(abc)", "info", "channel_open"},
		{"[INF] PEER: New channel active ChannelPoint(abc:0) with peer", "info", "channel_open"},
		{"[ERR] SRVR: Unable to connect to peer: dial tcp: connection refused", "error", "connection"},
		{"[WRN] PEER: pong timeout -- disconnecting", "warning", "connection"},
		{"[DBG] LTND: Version: 0.20.1-beta", "debug", "lifecycle"},
		{"[INF] LTND: Waiting for chain backend to finish sync", "info", "sync"},
		{"[INF] GRPH: close database handle", "info", "other"},
		{"[ERR] FNDG: channel type negotiation failed", "error", "other"},
		{"unstructured record", "unknown", "other"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			if got := classifyLogLevel(tc.message, ""); got != tc.level {
				t.Fatalf("level %q want %q", got, tc.level)
			}
			if got := classifyLNDEvent(tc.message); got != tc.event {
				t.Fatalf("event %q want %q", got, tc.event)
			}
		})
	}
	if classifyLogLevel("[ERR] app error sent to stdout", "6") != "error" || classifyLogLevel("plain journal error", "3") != "error" {
		t.Fatal("application severity must override transport priority")
	}
}

func TestLogQueryRedactsResultsAndContext(t *testing.T) {
	secrets := []string{
		"password=fixture-password", `{"token":"fixture-json-token"}`, "Authorization: Bearer fixture-bearer", "https://rpc:fixture-url-pass@host",
		"seed words: fixture-seed", "private_key=fixture-key", "bot123456789:ABCDEFGHIJKLMNOPQRSTUVWX", "rpcuser=fixture-rpc-user",
		"macaroon: fixture-macaroon", "-----BEGIN PRIVATE KEY-----\nfixture-pem\n-----END PRIVATE KEY-----",
	}
	for _, message := range secrets {
		got := safeQueryLogMessage(message)
		if strings.Contains(got, "fixture-") || strings.Contains(got, "ABCDEFGHIJKLMNOPQRSTUVWX") {
			t.Fatalf("sensitive value leaked for %q: %q", message, got)
		}
	}
	clean := safeQueryLogMessage("\x1b[31m[ERR] connection refused\x1b[0m\x00\nnext line")
	if strings.ContainsAny(clean, "\x1b\x00") || !strings.Contains(clean, "\nnext line") {
		t.Fatalf("bad sanitization: %q", clean)
	}
	batch := system.JournalQueryResult{Entries: []system.JournalEntry{{Message: "password=fixture-password"}, {Message: "[ERR] connection refused"}}}
	result := selectLogEntries(logQuery{Limit: 1, Level: "error"}, "systemd:lnd", logCapabilities{Context: true}, batch)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "fixture-password") || len(result.Entries[0].Context) != 2 {
		t.Fatalf("context sanitization: %s", encoded)
	}
}

func TestLogQueryLimitsAndSourceCapabilities(t *testing.T) {
	q := logQuery{Service: "fedimint-guardian", Since: time.Now(), Limit: 200}
	if _, err := (&Server{}).queryLogs(context.Background(), q); err != errLogQueryUnsupported {
		t.Fatalf("unsupported source filters silently accepted: %v", err)
	}
	batch := system.JournalQueryResult{Partial: true, Reason: "scan_limit"}
	result := selectLogEntries(logQuery{Limit: 200}, "systemd:lnd", logCapabilities{}, batch)
	if !result.Partial || result.Reason != "scan_limit" || result.Entries == nil {
		t.Fatalf("partial empty result must stay partial: %+v", result)
	}
	for range 1000 {
		batch.Entries = append(batch.Entries, system.JournalEntry{Message: strings.Repeat("x", 8000)})
	}
	result = selectLogEntries(logQuery{Limit: 1000}, "systemd:lnd", logCapabilities{Context: true}, batch)
	if !result.ResultLimited || len(result.Entries) >= 1000 {
		t.Fatal("response byte budget not applied")
	}
}
