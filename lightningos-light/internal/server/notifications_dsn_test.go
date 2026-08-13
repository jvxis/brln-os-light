package server

import (
	"errors"
	"testing"
)

func TestResolveNotificationsDSNUsesOnlyAuthenticatedCredentials(t *testing.T) {
	const valid = "postgres://losapp:valid@127.0.0.1:5432/lightningos?sslmode=disable"
	const stale = "postgres://losapp:stale@127.0.0.1:5432/lightningos?sslmode=disable"

	dsn, err := resolveNotificationsDSN(valid, func(candidate string) bool { return candidate == valid }, func() (string, error) {
		t.Fatal("valid current DSN unexpectedly triggered bootstrap")
		return "", nil
	})
	if err != nil || dsn != valid {
		t.Fatalf("valid current DSN rejected: dsn=%q err=%v", dsn, err)
	}

	dsn, err = resolveNotificationsDSN(stale, func(candidate string) bool { return candidate == valid }, func() (string, error) {
		return valid, nil
	})
	if err != nil || dsn != valid {
		t.Fatalf("stale DSN was not replaced: dsn=%q err=%v", dsn, err)
	}
}

func TestResolveNotificationsDSNFailsClosedWhenRecoveryIsUnavailable(t *testing.T) {
	const stale = "postgres://losapp:stale@127.0.0.1:5432/lightningos?sslmode=disable"
	if _, err := resolveNotificationsDSN(stale, func(string) bool { return false }, func() (string, error) {
		return stale, nil
	}); err == nil {
		t.Fatal("stale recovered DSN was accepted")
	}
	want := errors.New("admin recovery failed")
	if _, err := resolveNotificationsDSN(stale, func(string) bool { return false }, func() (string, error) {
		return "", want
	}); !errors.Is(err, want) {
		t.Fatalf("bootstrap error was not preserved: %v", err)
	}
}
