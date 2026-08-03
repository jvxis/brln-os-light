package server

import (
	"errors"
	"testing"
)

func TestClassifyLNDHealthError(t *testing.T) {
	tests := []struct {
		name              string
		err               error
		warmup            bool
		serviceActive     bool
		endpointReachable bool
		wantLevel         string
		wantMessage       string
	}{
		{
			name:              "busy rpc with reachable endpoint is warning",
			err:               errors.New("rpc error: code = Unavailable desc = transport is closing"),
			serviceActive:     true,
			endpointReachable: true,
			wantLevel:         "WARN",
			wantMessage:       "LND RPC temporarily busy (gRPC endpoint reachable)",
		},
		{
			name:              "getinfo timeout with reachable endpoint is warning",
			err:               errors.New("context deadline exceeded"),
			serviceActive:     true,
			endpointReachable: true,
			wantLevel:         "WARN",
			wantMessage:       "LND GetInfo timeout (gRPC endpoint reachable)",
		},
		{
			name:        "restart warmup timeout is warning",
			err:         errors.New("context deadline exceeded"),
			warmup:      true,
			wantLevel:   "WARN",
			wantMessage: "LND warming up after restart (GetInfo timeout)",
		},
		{
			name:              "macaroon error remains actionable",
			err:               errors.New("admin macaroon: permission denied"),
			serviceActive:     true,
			endpointReachable: true,
			wantLevel:         "ERR",
			wantMessage:       "LND macaroon unreadable (check permissions)",
		},
		{
			name:              "locked wallet remains actionable",
			err:               errors.New("wallet locked"),
			serviceActive:     true,
			endpointReachable: true,
			wantLevel:         "ERR",
			wantMessage:       "LND wallet locked",
		},
		{
			name:        "inactive service remains error",
			err:         errors.New("rpc error: code = Unavailable desc = connection refused"),
			wantLevel:   "ERR",
			wantMessage: "LND gRPC connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyLNDHealthError(tt.err, tt.warmup, tt.serviceActive, tt.endpointReachable)
			if got.Level != tt.wantLevel {
				t.Fatalf("level = %q, want %q", got.Level, tt.wantLevel)
			}
			if got.Message != tt.wantMessage {
				t.Fatalf("message = %q, want %q", got.Message, tt.wantMessage)
			}
		})
	}
}
