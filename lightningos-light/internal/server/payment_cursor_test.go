package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"lightningos-light/lnrpc"
)

type paymentCursorClient struct {
	lnrpc.LightningClient
	responses []*lnrpc.ListPaymentsResponse
	errors    []error
	requests  []*lnrpc.ListPaymentsRequest
}

func (c *paymentCursorClient) ListPayments(_ context.Context, req *lnrpc.ListPaymentsRequest, _ ...grpc.CallOption) (*lnrpc.ListPaymentsResponse, error) {
	c.requests = append(c.requests, req)
	i := len(c.requests) - 1
	if i < len(c.errors) && c.errors[i] != nil {
		return nil, c.errors[i]
	}
	if i < len(c.responses) {
		return c.responses[i], nil
	}
	return &lnrpc.ListPaymentsResponse{}, nil
}

func paymentPage(indices ...uint64) *lnrpc.ListPaymentsResponse {
	page := &lnrpc.ListPaymentsResponse{}
	for _, index := range indices {
		page.Payments = append(page.Payments, &lnrpc.Payment{PaymentIndex: index})
	}
	return page
}

func TestPaymentCursorAheadRecoveryBoundedAndDurable(t *testing.T) {
	client := &paymentCursorClient{responses: []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(89134, 89135), paymentPage()}}
	var saved paymentCursorRecovery
	now := time.Unix(1000, 0)
	page, offset, recovery, probed, err := queryPaymentPage(context.Background(), client, 13689703, "13689703", paymentCursorRecovery{}, true, now,
		func(_ context.Context, expected string, value paymentCursorRecovery) error {
			if expected != "13689703" {
				t.Fatal("lost original CAS value")
			}
			saved = value
			return nil
		})
	if err != nil || !probed || offset != 89133 || len(page.Payments) != 2 || recovery != saved {
		t.Fatalf("recovery failed: offset=%d recovery=%+v err=%v", offset, recovery, err)
	}
	if saved.PreviousCursor != "13689703" || saved.ReplayThrough != 89135 || saved.RecoveredAtNS != now.UnixNano() {
		t.Fatal("lost durable recovery evidence")
	}
	for i, req := range client.requests {
		if !req.IncludeIncomplete || req.MaxPayments != paymentsPollPageSize || req.CountTotalPayments {
			t.Fatal("changed or unbounded payments query")
		}
		if req.Reversed != (i == 1) {
			t.Fatal("wrong pagination direction")
		}
	}
	if client.requests[1].IndexOffset != 0 || client.requests[2].IndexOffset != 13689703 {
		t.Fatal("recovery was not independently confirmed")
	}
	raw, _ := json.Marshal(saved)
	loaded, err := decodePaymentCursorRecovery(string(raw))
	if err != nil || loaded != saved {
		t.Fatal("recovery fence did not survive restart")
	}
}

func TestPaymentCursorOrdinaryPagesAndEmptyHistoryNeverRewind(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset uint64
		probe  bool
		pages  []*lnrpc.ListPaymentsResponse
		calls  int
	}{
		{"normal", 50, true, []*lnrpc.ListPaymentsResponse{paymentPage(51, 52)}, 1},
		{"pending", 50, true, []*lnrpc.ListPaymentsResponse{{Payments: []*lnrpc.Payment{{PaymentIndex: 51, Status: lnrpc.Payment_IN_FLIGHT}}}}, 1},
		{"caught up", 50, true, []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(49, 50)}, 2},
		{"deleted history", 50, true, []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage()}, 2},
		{"new cursor", 0, true, []*lnrpc.ListPaymentsResponse{paymentPage()}, 1},
		{"throttled", 50, false, []*lnrpc.ListPaymentsResponse{paymentPage()}, 1},
		{"new payment during probe", 50, true, []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(49), paymentPage(51)}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &paymentCursorClient{responses: tc.pages}
			_, offset, _, _, err := queryPaymentPage(context.Background(), client, tc.offset, "50", paymentCursorRecovery{}, tc.probe, time.Now(), func(context.Context, string, paymentCursorRecovery) error { t.Fatal("unexpected rewind"); return nil })
			if err != nil || offset != tc.offset || len(client.requests) != tc.calls {
				t.Fatalf("ordinary behavior changed: offset=%d calls=%d err=%v", offset, len(client.requests), err)
			}
		})
	}
}

func TestPaymentCursorErrorsPreserveCursorAndFence(t *testing.T) {
	prior := paymentCursorRecovery{PreviousCursor: "1000", ResumeAfter: 10, ReplayThrough: 20, RecoveredAtNS: 1}
	for _, tc := range []struct {
		name      string
		responses []*lnrpc.ListPaymentsResponse
		errs      []error
		saveErr   bool
	}{
		{"forward failure", nil, []error{errors.New("offline")}, false},
		{"nil response", []*lnrpc.ListPaymentsResponse{nil}, nil, false},
		{"probe failure", []*lnrpc.ListPaymentsResponse{paymentPage()}, []error{nil, errors.New("offline")}, false},
		{"nil probe", []*lnrpc.ListPaymentsResponse{paymentPage(), nil}, nil, false},
		{"invalid index", []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(0)}, nil, false},
		{"confirmation failure", []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(5)}, []error{nil, nil, errors.New("offline")}, false},
		{"CAS or database failure", []*lnrpc.ListPaymentsResponse{paymentPage(), paymentPage(5), paymentPage()}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &paymentCursorClient{responses: tc.responses, errors: tc.errs}
			_, offset, recovery, _, err := queryPaymentPage(context.Background(), client, 100, "100", prior, true, time.Now(), func(context.Context, string, paymentCursorRecovery) error {
				if !tc.saveErr {
					t.Fatal("unexpected persistence")
				}
				return errors.New("write rejected")
			})
			if err == nil || offset != 100 || recovery != prior {
				t.Fatal("failure changed cursor or historical fence")
			}
		})
	}
}

func TestPaymentRecoveryMirrorFencePreservesLivePendingCompletion(t *testing.T) {
	r := paymentCursorRecovery{PreviousCursor: "1000", ResumeAfter: 1, ReplayThrough: 100, RecoveredAtNS: 1000}
	for _, tc := range []struct {
		name       string
		pay        *lnrpc.Payment
		historical bool
	}{
		{"historical terminal", &lnrpc.Payment{PaymentIndex: 99, CreationTimeNs: 100}, true},
		{"historical with unknown date", &lnrpc.Payment{PaymentIndex: 99}, true},
		{"new payment", &lnrpc.Payment{PaymentIndex: 101, CreationTimeNs: 1001}, false},
		{"created during recovery", &lnrpc.Payment{PaymentIndex: 99, CreationTimeNs: 1001}, false},
		{"pending later settled", &lnrpc.Payment{PaymentIndex: 99, CreationTimeNs: 100, Htlcs: []*lnrpc.HTLCAttempt{{ResolveTimeNs: 1001}}}, false},
		{"old settled rebalance", &lnrpc.Payment{PaymentIndex: 99, CreationTimeNs: 100, Htlcs: []*lnrpc.HTLCAttempt{{ResolveTimeNs: 900}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if r.suppressMirror(tc.pay) != tc.historical {
				t.Fatal("wrong historical/live decision")
			}
		})
	}
	for _, raw := range []string{`{`, `{"previous_cursor":"10","resume_after":0,"replay_through":20,"recovered_at_ns":1}`, `{"previous_cursor":"100","resume_after":20,"replay_through":20,"recovered_at_ns":1}`} {
		if _, err := decodePaymentCursorRecovery(raw); err == nil {
			t.Fatal("accepted corrupt recovery checkpoint")
		}
	}
}
