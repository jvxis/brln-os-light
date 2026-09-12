package server

import (
	"bytes"
	"context"
	"io"
	"lightningos-light/internal/mesh"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMeshDiagnosticCountersPersistWithoutClaimingDelivery(t *testing.T) {
	m, _ := meshIntegrationService(t)
	ctx := context.Background()
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_settings SET mode='send'")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(ctx, "INSERT INTO los_mesh_peers(node,name,secret,paired) VALUES(10,'test',$1,true)", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	m.client = &http.Client{Transport: meshRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := "{}"
		code := 202
		if r.URL.Path == "/status" {
			body = `{"state":"running","node":20,"protocol":1}`
			code = 200
		}
		if r.URL.Path == "/packets" && r.Method == "GET" {
			body = "[]"
			code = 200
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	id, err := m.queue(ctx, 10, mesh.Transaction, meshTestRaw())
	if err != nil {
		t.Fatal(err)
	}
	m.tick(ctx)
	var attempts, accepted, received int
	var updated *time.Time
	if err = m.db.QueryRow(ctx, "SELECT send_attempts,bridge_accepted,received,updated FROM los_mesh_sessions WHERE id=$1", id).Scan(&attempts, &accepted, &received, &updated); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || accepted != 1 || received != 0 || updated == nil {
		t.Fatal("bridge acceptance incorrectly reported as delivery", attempts, accepted, received)
	}
	o := m.outgoing[id]
	o.Attempts = 3
	o.Sent = time.Now().Add(-time.Minute)
	m.tick(ctx)
	var state, reason string
	if err = m.db.QueryRow(ctx, "SELECT state,last_error FROM los_mesh_sessions WHERE id=$1", id).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "incomplete" || reason != "no_los_confirmation" {
		t.Fatal(state, reason)
	}
}
