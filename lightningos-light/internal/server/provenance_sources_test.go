package server

import (
	"context"
	"testing"

	"lightningos-light/internal/electrs"
)

func TestParseProvenancePrimary(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		invalid string
	}{
		{raw: "", want: provenancePrimaryChain},
		{raw: "chain", want: provenancePrimaryChain},
		{raw: "bitcoind", want: provenancePrimaryBitcoind},
		{raw: "bitcoin-core", want: provenancePrimaryBitcoind},
		{raw: "electrs", want: provenancePrimaryElectrs},
		{raw: "electrum", want: provenancePrimaryElectrs},
		{raw: "public", want: provenancePrimaryChain, invalid: "public"},
	}

	for _, tc := range tests {
		got, invalid := parseProvenancePrimary(tc.raw)
		if got != tc.want || invalid != tc.invalid {
			t.Fatalf("parseProvenancePrimary(%q) = (%q, %q), want (%q, %q)", tc.raw, got, invalid, tc.want, tc.invalid)
		}
	}
}

func TestBuildProvenanceSourceChainDefaultKeepsFallbackOrder(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "disabled")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 2 || names[0] != "bitcoind" || names[1] != "local electrs" {
		t.Fatalf("unexpected source order: %+v", names)
	}
}

func TestBuildProvenanceSourceChainForcedBitcoind(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "bitcoind")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 1 || names[0] != "bitcoind" {
		t.Fatalf("expected only bitcoind, got %+v", names)
	}
}

func TestBuildProvenanceSourceChainForcedElectrs(t *testing.T) {
	t.Setenv("PROVENANCE_PRIMARY", "electrs")
	t.Setenv("PROVENANCE_PUBLIC_ELECTRUM", "")
	bitcoind := NewBitcoinCoreSource(func(context.Context) (bool, string) { return true, "" })

	chain, _ := buildProvenanceSourceChain(true, bitcoind, nil)
	names := sourceNames(chain.Sources())
	if len(names) != 1 || names[0] != "local electrs" {
		t.Fatalf("expected only local electrs, got %+v", names)
	}
}

func sourceNames(sources []electrs.TxSource) []string {
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, source.Name())
	}
	return names
}
