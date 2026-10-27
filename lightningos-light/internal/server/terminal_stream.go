package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const terminalStreamCheckInterval = time.Second

type terminalRuntimeSnapshot struct {
	values map[string]string
	digest [sha256.Size]byte
	path   string
}

func readTerminalRuntimeSnapshot(path string) (terminalRuntimeSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return terminalRuntimeSnapshot{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return terminalRuntimeSnapshot{}, errors.New("terminal runtime unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return terminalRuntimeSnapshot{}, errors.New("terminal runtime unavailable")
	}
	snapshot := terminalRuntimeSnapshot{values: make(map[string]string), digest: sha256.Sum256(raw), path: path}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			snapshot.values[key] = strings.TrimSpace(value)
		}
	}
	return snapshot, nil
}

// Inspect the live session without refreshing LastSeenAt or ExpiresAt. A
// background terminal connection must not keep a login alive indefinitely.
func (a *AuthService) terminalSessionValid(id string, loginRequired bool) bool {
	if a.Enabled() != loginRequired {
		return false
	}
	if !loginRequired {
		return true
	}
	now := a.currentTime()
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[id]
	return ok && now.Before(session.ExpiresAt)
}

// ReverseProxy closes both ends of an upgraded connection when this context is
// cancelled. Runtime changes (including operator, credential and write mode)
// invalidate the old connection; reconnecting runs auth/origin checks again.
func (s *Server) terminalStreamContext(r *http.Request, runtime terminalRuntimeSnapshot) (context.Context, context.CancelFunc, bool) {
	required := s.auth.Enabled()
	session, _ := authSessionFromContext(r.Context())
	if !s.auth.terminalSessionValid(session.ID, required) {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(r.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(terminalStreamCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := readTerminalRuntimeSnapshot(runtime.path)
				if err != nil || current.digest != runtime.digest || !s.auth.terminalSessionValid(session.ID, required) {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); <-done }, true
}
