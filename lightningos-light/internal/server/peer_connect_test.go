package server

import (
	"errors"
	"testing"
)

func TestIsAlreadyConnected(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "already connected", err: errors.New("already connected to peer\n02abc@example.onion:9735"), want: true},
		{name: "existing connection", err: errors.New("already have a connection to peer"), want: true},
		{name: "unreachable", err: errors.New("connection refused"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isAlreadyConnected(test.err); got != test.want {
				t.Fatalf("isAlreadyConnected() = %v, want %v", got, test.want)
			}
		})
	}
}
