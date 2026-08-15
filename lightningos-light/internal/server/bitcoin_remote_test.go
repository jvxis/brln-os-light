package server

import "testing"

func TestSelectBitcoinRemoteRPCConfig(t *testing.T) {
	fallback := bitcoinRPCConfig{Host: "configured.example:8332", User: "configured-user", Pass: "configured-pass"}
	tagged := bitcoinRPCConfig{Host: "tagged.example:8332", User: "tagged-user", Pass: "tagged-pass"}
	activeRemote := bitcoinRPCConfig{Host: "active.example:8332", User: "active-user", Pass: "active-pass"}
	activeLocal := bitcoinRPCConfig{Host: "127.0.0.1:8332", User: "local-user", Pass: "local-pass"}

	tests := []struct {
		name     string
		tagged   bitcoinRPCConfig
		taggedOK bool
		active   bitcoinRPCConfig
		activeOK bool
		want     bitcoinRPCConfig
	}{
		{name: "existing untagged remote lnd config", active: activeRemote, activeOK: true, want: activeRemote},
		{name: "managed tagged remote overrides active local", tagged: tagged, taggedOK: true, active: activeLocal, activeOK: true, want: tagged},
		{name: "local lnd config does not become remote", active: activeLocal, activeOK: true, want: fallback},
		{name: "configured fallback", want: fallback},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectBitcoinRemoteRPCConfig(fallback, tc.tagged, tc.taggedOK, tc.active, tc.activeOK)
			if got != tc.want {
				t.Fatalf("selected config = %#v, want %#v", got, tc.want)
			}
		})
	}
}
