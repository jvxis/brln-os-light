package server

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

func TestTerminalEstablishedWebsocketRevocation(t *testing.T) {
	for _, reason := range []string{"logout", "expiry", "all_session_reset", "credential_rotation", "write_removed", "disabled", "missing_runtime"} {
		t.Run(reason, func(t *testing.T) {
			oldPath := terminalRuntimeEnvPath
			terminalRuntimeEnvPath = filepath.Join(t.TempDir(), "terminal.env")
			defer func() { terminalRuntimeEnvPath = oldPath }()
			credential := "losop:AbCdEfGhIjKlMnOpQrStUvWxYz012345"
			upstream := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				if ws.Request().Header.Get("Authorization") != basicAuthHeader(credential) || ws.Request().Header.Get("Cookie") != "" {
					return
				}
				for {
					var value string
					if websocket.Message.Receive(ws, &value) != nil {
						return
					}
					if websocket.Message.Send(ws, value) != nil {
						return
					}
				}
			}))
			defer upstream.Close()
			u, _ := url.Parse(upstream.URL)
			writeRuntime := func(enabled, write string, secret string) {
				t.Helper()
				content := fmt.Sprintf("TERMINAL_ENABLED=%s\nTERMINAL_ALLOW_WRITE=%s\nTERMINAL_PORT=%s\nTERMINAL_CREDENTIAL=%s\n", enabled, write, u.Port(), secret)
				if err := os.WriteFile(terminalRuntimeEnvPath, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeRuntime("1", "1", credential)
			auth := &AuthService{enabled: true, now: time.Now, sessions: make(map[string]*authSession)}
			snapshot, err := auth.newSession()
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{auth: auth, logger: log.New(io.Discard, "", 0)}
			front := httptest.NewTLSServer(auth.Middleware()(http.HandlerFunc(server.handleTerminalProxy)))
			defer front.Close()
			dial := func(session, origin string) (*websocket.Conn, error) {
				cfg, _ := websocket.NewConfig("wss"+strings.TrimPrefix(front.URL, "https")+"/terminal/ws", origin)
				cfg.TlsConfig = front.Client().Transport.(*http.Transport).TLSClientConfig
				cfg.Header.Set("Cookie", authSessionCookieName+"="+session)
				return websocket.DialConfig(cfg)
			}
			ws, err := dial(snapshot.ID, front.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			if err := websocket.Message.Send(ws, "before"); err != nil {
				t.Fatal(err)
			}
			var value string
			if err := websocket.Message.Receive(ws, &value); err != nil || value != "before" {
				t.Fatalf("authorized connection not functional: %v", err)
			}
			switch reason {
			case "logout":
				auth.logout(snapshot.ID)
			case "expiry":
				auth.mu.Lock()
				auth.sessions[snapshot.ID].ExpiresAt = time.Now().Add(-time.Second)
				auth.mu.Unlock()
			case "all_session_reset":
				if _, err := auth.resetSessionsAndCreateNew(); err != nil {
					t.Fatal(err)
				}
			case "credential_rotation":
				writeRuntime("1", "1", "losop:0123456789AbCdEfGhIjKlMnOpQrStUv")
			case "write_removed":
				writeRuntime("1", "0", credential)
			case "disabled":
				writeRuntime("0", "0", credential)
			case "missing_runtime":
				if err := os.Remove(terminalRuntimeEnvPath); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(1250 * time.Millisecond)
			ws.SetDeadline(time.Now().Add(2 * time.Second))
			err = websocket.Message.Send(ws, "after")
			if err == nil {
				err = websocket.Message.Receive(ws, &value)
			}
			var timeout net.Error
			if err == nil {
				t.Fatalf("terminal remained usable after %s", reason)
			}
			if errors.As(err, &timeout) && timeout.Timeout() {
				t.Fatalf("terminal was not actively closed after %s", reason)
			}
			if reason == "logout" || reason == "expiry" || reason == "all_session_reset" {
				stale, err := dial(snapshot.ID, front.URL)
				if err == nil {
					stale.Close()
					t.Fatal("revoked session reconnected")
				}
				fresh, _ := auth.newSession()
				next, err := dial(fresh.ID, front.URL)
				if err != nil {
					t.Fatal("new session could not reconnect")
				}
				next.Close()
				cross, err := dial(fresh.ID, "https://attacker.invalid")
				if err == nil {
					cross.Close()
					t.Fatal("cross-origin reconnect accepted")
				}
			}
		})
	}
}

func TestTerminalMonitoringDoesNotExtendSession(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	auth := &AuthService{enabled: true, now: func() time.Time { return now }, sessions: make(map[string]*authSession)}
	session, err := auth.newSession()
	if err != nil {
		t.Fatal(err)
	}
	expiry := auth.sessions[session.ID].ExpiresAt
	now = now.Add(time.Hour)
	if !auth.terminalSessionValid(session.ID, true) {
		t.Fatal("live session rejected")
	}
	if !auth.sessions[session.ID].ExpiresAt.Equal(expiry) || !auth.sessions[session.ID].LastSeenAt.Equal(session.ExpiresAt.Add(-authSessionTTL)) {
		t.Fatal("background monitor refreshed session lifetime")
	}
	now = expiry
	if auth.terminalSessionValid(session.ID, true) {
		t.Fatal("session accepted at expiration")
	}
}
