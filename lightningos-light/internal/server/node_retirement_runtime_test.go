package server

import "testing"

func TestClassifySuccessionTransferWalletFunds(t *testing.T) {
	tests := []struct {
		name            string
		confirmedAmount int64
		totalAmount     int64
		wantDone        bool
		wantStatus      string
		wantError       string
	}{
		{
			name:            "waits when wallet has only unconfirmed funds",
			confirmedAmount: 0,
			totalAmount:     12500,
			wantDone:        false,
			wantStatus:      "waiting_funds",
			wantError:       "no confirmed UTXOs available",
		},
		{
			name:            "finishes when wallet has no funds",
			confirmedAmount: 0,
			totalAmount:     0,
			wantDone:        true,
			wantStatus:      nodeRetirementTransferSkippedNoWalletFunds,
			wantError:       "no wallet funds available for succession transfer",
		},
		{
			name:            "does nothing special when confirmed funds exist",
			confirmedAmount: 21000,
			totalAmount:     21000,
			wantDone:        false,
			wantStatus:      "",
			wantError:       "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			done, status, lastError := classifySuccessionTransferWalletFunds(tc.confirmedAmount, tc.totalAmount)
			if done != tc.wantDone {
				t.Fatalf("done mismatch: got %v want %v", done, tc.wantDone)
			}
			if status != tc.wantStatus {
				t.Fatalf("status mismatch: got %q want %q", status, tc.wantStatus)
			}
			if lastError != tc.wantError {
				t.Fatalf("lastError mismatch: got %q want %q", lastError, tc.wantError)
			}
		})
	}
}
