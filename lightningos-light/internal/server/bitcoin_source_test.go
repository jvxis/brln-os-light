package server

import (
	"errors"
	"net/http"
	"testing"
)

func TestBitcoinRPCAuthenticationErrorRecognizesCoreResponses(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		if !isBitcoinRPCAuthenticationError(rpcStatusError{statusCode: status}) {
			t.Fatalf("status %d was not recognized as an authentication error", status)
		}
	}
	if isBitcoinRPCAuthenticationError(rpcStatusError{statusCode: http.StatusInternalServerError}) {
		t.Fatal("server error was misclassified as authentication failure")
	}
	if isBitcoinRPCAuthenticationError(errors.New("dial failed")) {
		t.Fatal("transport error was misclassified as authentication failure")
	}
}

func TestResolveBitcoinSourcePrefersLNDConf(t *testing.T) {
	t.Run("lnd conf local overrides env remote", func(t *testing.T) {
		got := resolveBitcoinSource(
			"remote",
			"BITCOIN_SOURCE=remote\n",
			"[Bitcoind]\nbitcoind.rpchost=127.0.0.1:8332\n",
		)
		if got != "local" {
			t.Fatalf("expected local, got %q", got)
		}
	})

	t.Run("lnd conf remote overrides env local", func(t *testing.T) {
		got := resolveBitcoinSource(
			"local",
			"BITCOIN_SOURCE=local\n",
			"[Bitcoind]\nbitcoind.rpchost=bitcoin.br-ln.com:8085\n",
		)
		if got != "remote" {
			t.Fatalf("expected remote, got %q", got)
		}
	})
}

func TestResolveBitcoinSourceFallbacks(t *testing.T) {
	t.Run("falls back to env when lnd conf is absent", func(t *testing.T) {
		got := resolveBitcoinSource("local", "", "")
		if got != "local" {
			t.Fatalf("expected local, got %q", got)
		}
	})

	t.Run("falls back to secrets when lnd conf/env are absent", func(t *testing.T) {
		got := resolveBitcoinSource("", "BITCOIN_SOURCE=local\n", "")
		if got != "local" {
			t.Fatalf("expected local, got %q", got)
		}
	})

	t.Run("defaults to remote when nothing valid is available", func(t *testing.T) {
		got := resolveBitcoinSource("invalid", "BITCOIN_SOURCE=invalid\n", "")
		if got != "remote" {
			t.Fatalf("expected remote, got %q", got)
		}
	})
}

func TestParseBitcoinSourceFromLNDConf(t *testing.T) {
	t.Run("detects local from active localhost rpchost", func(t *testing.T) {
		raw := "[Bitcoind]\nbitcoind.rpchost=localhost:8332\n"
		got := parseBitcoinSourceFromLNDConf(raw)
		if got != "local" {
			t.Fatalf("expected local, got %q", got)
		}
	})

	t.Run("detects remote from active remote rpchost", func(t *testing.T) {
		raw := "[Bitcoind]\nbitcoind.rpchost=bitcoin.br-ln.com:8085\n"
		got := parseBitcoinSourceFromLNDConf(raw)
		if got != "remote" {
			t.Fatalf("expected remote, got %q", got)
		}
	})

	t.Run("ignores commented rpchost lines", func(t *testing.T) {
		raw := "[Bitcoind]\n# bitcoind.rpchost=bitcoin.br-ln.com:8085\nbitcoind.rpchost=127.0.0.1:8332\n"
		got := parseBitcoinSourceFromLNDConf(raw)
		if got != "local" {
			t.Fatalf("expected local, got %q", got)
		}
	})
}
