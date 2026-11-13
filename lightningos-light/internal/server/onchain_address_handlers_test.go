package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
)

type fakeAddressOwnership struct {
	visible, controlled bool
	err                 error
}

func (f fakeAddressOwnership) IsWalletAddress(context.Context, string) (bool, error) {
	return f.visible, f.err
}

func (f fakeAddressOwnership) IsControlledDefaultWalletAddress(context.Context, string) (bool, error) {
	return f.controlled, f.err
}

func TestClassifyOnchainAddressOwnership(t *testing.T) {
	cases := []struct {
		name string
		lnd  fakeAddressOwnership
		want string
	}{
		{"external", fakeAddressOwnership{}, onchainAddressExternal},
		{"owned", fakeAddressOwnership{visible: true, controlled: true}, onchainAddressOwned},
		{"imported or watch-only", fakeAddressOwnership{visible: true}, onchainAddressKnown},
	}
	for _, tc := range cases {
		got, err := classifyOnchainAddressOwnership(context.Background(), tc.lnd, "bc1qaddress")
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q err %v, want %q", tc.name, got, err, tc.want)
		}
	}
	if _, err := classifyOnchainAddressOwnership(context.Background(), fakeAddressOwnership{err: errors.New("lnd down")}, "bc1qaddress"); err == nil {
		t.Fatal("wallet lookup errors must surface, never default to external")
	}
}

func TestWalletAddressNetwork(t *testing.T) {
	cases := map[string]string{
		"bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4":                     "mainnet",
		"bc1pmfr3p9j00pfxjh0zmgp99y8zftmd3s5pmedqhyptwy6lm87hf5sspknck9": "mainnet",
		"3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy":                             "mainnet",
		"tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kxpjzsx":                     "testnet",
	}
	for address, want := range cases {
		got, ok := lndclient.WalletAddressNetwork(address)
		if !ok || got != want {
			t.Fatalf("%s: got %q ok=%v want %q", address, got, ok, want)
		}
	}
	for _, bad := range []string{"", "hello", "bc1qinvalidchecksum0000000000000000000000000", "lnbc1..."} {
		if _, ok := lndclient.WalletAddressNetwork(bad); ok {
			t.Fatalf("%q must not parse as an address", bad)
		}
	}
}

func TestHandleOnchainAddressOwnershipRejectsBadInputBeforeLND(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	for _, query := range []string{"", "address=", "address=not-an-address", "address=" + strings.Repeat("a", 129)} {
		req := httptest.NewRequest(http.MethodGet, "/api/onchain/address/ownership?"+query, nil)
		rec := httptest.NewRecorder()
		s.handleOnchainAddressOwnership(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("query %q: expected 400, got %d %s", query, rec.Code, rec.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/onchain/address/ownership?address=bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4", nil)
	rec := httptest.NewRecorder()
	s.handleOnchainAddressOwnership(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("valid address without LND must answer 503, got %d", rec.Code)
	}
}
