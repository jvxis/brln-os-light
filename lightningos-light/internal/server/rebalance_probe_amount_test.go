package server

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRouteAmountOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"build route policy refusal", status.Error(codes.Unknown, "no matching outgoing channel available for node index 1"), true},
		{"plain error with same text", errors.New("rpc error: code = Unknown desc = no matching outgoing channel available for node index 2"), true},
		{"no path is not an amount problem", status.Error(codes.Unknown, "unable to find a path to destination"), false},
		{"transport error", status.Error(codes.Unavailable, "connection refused"), false},
		{"deadline", context.DeadlineExceeded, false},
		{"cancelled", context.Canceled, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRouteAmountOutOfRange(tc.err); got != tc.want {
				t.Fatalf("isRouteAmountOutOfRange(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
