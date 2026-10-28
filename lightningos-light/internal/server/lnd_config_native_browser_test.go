package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lightningos-light/internal/config"
)

// Called only after the native fixture's explicit opt-in, VM identity, root,
// regtest config and authenticated native daemon guards have passed. The
// launcher provides a private read-only /etc/lightningos bind for auth secrets.
func runLNDConfigNativeBrowser(t *testing.T, s *Server) {
	t.Helper()
	const root = "/home/admin/los-regtest-tools"
	const static = root + "/config309-native-ui"
	const ready = root + "/config309-browser-ready"
	const done = root + "/config309-browser-done"
	if _, err := os.Stat(filepath.Join(static, "index.html")); err != nil {
		t.Fatal("native browser UI missing")
	}
	s.cfg = &config.Config{UI: config.UIConfig{StaticDir: static}}
	listener, err := net.Listen("tcp", "127.0.0.1:18643")
	if err != nil {
		t.Fatal("native browser TLS port unavailable")
	}
	server := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	errors := make(chan error, 1)
	go func() {
		errors <- server.ServeTLS(listener, "/etc/los-regtest/alice/lightningos/server.crt", "/etc/los-regtest/alice/lightningos/server.key")
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	if err := os.WriteFile(ready, []byte("native config UI fixture ready\n"), 0600); err != nil {
		t.Fatal("could not signal browser readiness")
	}
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := os.Stat(done); err == nil {
				t.Log("PASS: native browser integration completed")
				return
			}
		case <-timer.C:
			t.Fatal("native browser integration did not complete")
		case <-errors:
			t.Fatal("native browser server stopped unexpectedly")
		}
	}
}
