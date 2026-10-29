package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBitcoinCoreOperationAuditPersistsOutcomesWithoutSecrets(t *testing.T) {
	pool := auditSecurityPool(t)
	svc := NewAuditService(pool, nil)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := &Server{auditLog: svc}
	for _, test := range []struct {
		action string
		status int
	}{{"start", 200}, {"stop", 500}, {"install", 409}} {
		req := httptest.NewRequest("POST", "/api/apps/bitcoincore/"+test.action, strings.NewReader(`{"password":"fixture-private-password"}`))
		req.Header.Set("Cookie", "session=fixture-private-cookie")
		out := httptest.NewRecorder()
		wrapped, done := s.auditBitcoinCoreOperation(out, req, "bitcoincore", test.action)
		wrapped.WriteHeader(test.status)
		_, _ = wrapped.Write([]byte("fixture-private-error-detail"))
		done()
		if out.Code != test.status || out.Body.String() != "fixture-private-error-detail" {
			t.Fatal("Audit changed the HTTP response")
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var count int
		if err := pool.QueryRow(context.Background(), "select count(*) from audit_events").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("asynchronous lifecycle Audit events missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rows, err := pool.Query(context.Background(), "select action, target, metadata from audit_events")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var action, target string
		var raw []byte
		if err := rows.Scan(&action, &target, &raw); err != nil {
			t.Fatal(err)
		}
		if target != "bitcoincore" || strings.Contains(string(raw), "fixture-private") {
			t.Fatal("unsafe Audit target/metadata")
		}
		var metadata map[string]any
		if json.Unmarshal(raw, &metadata) != nil || len(metadata) != 3 {
			t.Fatal("unexpected Audit metadata contract")
		}
		want := "error"
		if action == "app.bitcoincore.start" {
			want = "success"
		}
		if metadata["status"] != want || metadata["catalog_version"] != "31.2" {
			t.Fatal("incorrect lifecycle outcome/version label")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
