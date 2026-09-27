package lndclient

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lightningos-light/lnrpc"
)

func TestPolicyUpdateResult(t *testing.T) {
	transport := errors.New("transport error")
	for _, tc := range []struct {
		name        string
		resp        *lnrpc.PolicyUpdateResponse
		err         error
		wantFailure bool
		wantCount   int
	}{
		{"success", &lnrpc.PolicyUpdateResponse{}, nil, false, 0},
		{"missing response", nil, nil, true, 0},
		{"transport", nil, transport, true, 0},
		{"transport takes precedence", &lnrpc.PolicyUpdateResponse{FailedUpdates: []*lnrpc.FailedUpdate{{}}}, transport, true, 0},
		{"partial rejection", &lnrpc.PolicyUpdateResponse{FailedUpdates: []*lnrpc.FailedUpdate{{Reason: lnrpc.UpdateFailure_UPDATE_FAILURE_INVALID_PARAMETER, UpdateError: "secret"}, nil}}, nil, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := policyUpdateResult(tc.resp, tc.err)
			if (got != nil) != tc.wantFailure {
				t.Fatalf("unexpected result: %v", got)
			}
			if tc.err != nil && got != tc.err {
				t.Fatal("transport error identity changed")
			}
			var rejected *PolicyUpdateError
			if tc.wantCount > 0 {
				if !errors.As(got, &rejected) || rejected.FailedUpdates != tc.wantCount {
					t.Fatalf("missing rejection: %v", got)
				}
				if strings.Contains(got.Error(), "secret") {
					t.Fatal("raw daemon error leaked")
				}
			}
		})
	}
}

func TestPolicyFailureReasonsAreBoundedAndSanitized(t *testing.T) {
	resp := &lnrpc.PolicyUpdateResponse{}
	for _, reason := range []lnrpc.UpdateFailure{0, 1, 2, 3, 4, 999} {
		resp.FailedUpdates = append(resp.FailedUpdates, &lnrpc.FailedUpdate{Reason: reason, UpdateError: "secret"})
	}
	resp.FailedUpdates = append(resp.FailedUpdates, nil)
	reasons := policyUpdateFailureReasons(resp)
	if len(reasons) != 5 || reasons["unknown"] != 3 || reasons["pending"] != 1 || reasons["not_found"] != 1 || reasons["internal"] != 1 || reasons["invalid_parameter"] != 1 {
		t.Fatalf("unexpected reasons: %v", reasons)
	}
	raw, err := json.Marshal(reasons)
	if err != nil || strings.Contains(string(raw), "secret") {
		t.Fatalf("unsafe JSON: %s %v", raw, err)
	}
}
