package server

import (
	"context"
	"strings"
	"testing"

	"lightningos-light/internal/system"
)

func TestExternalBitcoinConsumerNetworkRequiresEnforcedTypedBroker(t *testing.T) {
	for _, test := range []struct {
		name      string
		client    *cpuMinerPrivilegedClient
		wantErr   string
		wantCalls int
	}{
		{name: "missing", wantErr: "enforce mode"},
		{name: "shadow", client: &cpuMinerPrivilegedClient{mode: "shadow"}, wantErr: "enforce mode", wantCalls: 1},
		{name: "enforced", client: &cpuMinerPrivilegedClient{mode: "enforce"}, wantCalls: 1},
		{name: "broker rejection", client: &cpuMinerPrivilegedClient{mode: "enforce", networkErr: context.DeadlineExceeded}, wantErr: "consumer network unavailable", wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.client == nil {
				system.ConfigurePrivilegedClient(nil)
			} else {
				system.ConfigurePrivilegedClient(test.client)
			}
			t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })
			err := ensureLocalExternalBitcoinConsumerNetwork(context.Background())
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error=%v, want substring %q", err, test.wantErr)
			}
			if test.client != nil && test.client.networkCalls != test.wantCalls {
				t.Fatalf("network calls=%d, want %d", test.client.networkCalls, test.wantCalls)
			}
		})
	}
}
