package lndclient

import (
	"testing"

	"lightningos-light/lnrpc"
)

func TestBuildPeerInfosUsesOnlyProvidedAliasMap(t *testing.T) {
	infos := buildPeerInfos([]*lnrpc.Peer{
		{
			PubKey:  "02ABC",
			Address: "203.0.113.10:9735",
			Errors: []*lnrpc.TimestampedError{
				{Timestamp: 123, Error: "last failure"},
			},
		},
		{
			PubKey:  "03DEF",
			Address: "198.51.100.20:9735",
		},
	}, map[string]string{
		"02abc": "Known Alias",
	})

	if len(infos) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(infos))
	}
	if infos[0].Alias != "Known Alias" {
		t.Fatalf("expected alias from alias map, got %q", infos[0].Alias)
	}
	if infos[0].LastError != "last failure" || infos[0].LastErrorTime != 123 {
		t.Fatalf("expected latest peer error to be preserved, got %q at %d", infos[0].LastError, infos[0].LastErrorTime)
	}
	if infos[1].Alias != "" {
		t.Fatalf("expected missing alias to stay empty without graph lookup, got %q", infos[1].Alias)
	}
}
