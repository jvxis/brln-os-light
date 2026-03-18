package lndclient

import (
	"errors"
	"testing"

	"lightningos-light/lnrpc"
)

func TestSendPaymentSyncError(t *testing.T) {
	baseErr := errors.New("rpc failed")

	tests := []struct {
		name string
		res  *lnrpc.SendResponse
		err  error
		want string
	}{
		{name: "rpc error", err: baseErr, want: "rpc failed"},
		{name: "payment error", res: &lnrpc.SendResponse{PaymentError: "temporary channel failure"}, want: "temporary channel failure"},
		{name: "payment error trimmed", res: &lnrpc.SendResponse{PaymentError: " insufficient balance "}, want: "insufficient balance"},
		{name: "success", res: &lnrpc.SendResponse{}, want: ""},
		{name: "nil response success", want: ""},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := sendPaymentSyncError(tc.res, tc.err)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.want)
			}
			if err.Error() != tc.want {
				t.Fatalf("expected error %q, got %q", tc.want, err.Error())
			}
		})
	}
}
