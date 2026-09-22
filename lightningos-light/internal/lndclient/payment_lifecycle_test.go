package lndclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"lightningos-light/lnrpc"
)

func TestPaymentLifecycle(t *testing.T) {
	cases := []struct {
		name             string
		pay              lnrpc.Payment
		started, settled time.Time
	}{
		{name: "missing timestamps remain unknown"},
		{name: "legacy creation", pay: lnrpc.Payment{CreationDate: 100}, started: time.Unix(100, 0).UTC()},
		{name: "nanoseconds preferred", pay: lnrpc.Payment{CreationDate: 100, CreationTimeNs: 100_000_000_123}, started: time.Unix(0, 100_000_000_123).UTC()},
		{name: "MPP settles at last successful shard", pay: lnrpc.Payment{Status: lnrpc.Payment_SUCCEEDED, Htlcs: []*lnrpc.HTLCAttempt{
			nil, {Status: lnrpc.HTLCAttempt_SUCCEEDED, ResolveTimeNs: 200},
			{Status: lnrpc.HTLCAttempt_FAILED, ResolveTimeNs: 900},
			{Status: lnrpc.HTLCAttempt_SUCCEEDED, ResolveTimeNs: 300},
		}}, settled: time.Unix(0, 300).UTC()},
		{name: "in flight is not settled", pay: lnrpc.Payment{Status: lnrpc.Payment_IN_FLIGHT, Htlcs: []*lnrpc.HTLCAttempt{{Status: lnrpc.HTLCAttempt_SUCCEEDED, ResolveTimeNs: 300}}}},
		{name: "failed is not settled", pay: lnrpc.Payment{Status: lnrpc.Payment_FAILED, Htlcs: []*lnrpc.HTLCAttempt{{Status: lnrpc.HTLCAttempt_FAILED, ResolveTimeNs: 300}}}},
		{name: "missing successful resolution", pay: lnrpc.Payment{Status: lnrpc.Payment_SUCCEEDED, Htlcs: []*lnrpc.HTLCAttempt{{Status: lnrpc.HTLCAttempt_SUCCEEDED, ResolveTimeNs: 300}, {Status: lnrpc.HTLCAttempt_SUCCEEDED}}}},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			var details PaymentDetails
			populatePaymentLifecycle(&details, &tc.pay)
			if !details.StartedAt.Equal(tc.started) || !details.SettledAt.Equal(tc.settled) {
				t.Fatalf("got started=%v settled=%v, want %v %v", details.StartedAt, details.SettledAt, tc.started, tc.settled)
			}
		})
	}
}

type invoiceTimestampClient struct {
	lnrpc.LightningClient
	response *lnrpc.PayReq
	err      error
	calls    int
}

func (c *invoiceTimestampClient) DecodePayReq(_ context.Context, req *lnrpc.PayReqString, _ ...grpc.CallOption) (*lnrpc.PayReq, error) {
	c.calls++
	if req.PayReq != "invoice" {
		return nil, errors.New("unexpected payment request")
	}
	return c.response, c.err
}

func TestInvoiceCreationTimestamp(t *testing.T) {
	for _, tc := range []struct {
		name, request string
		response      *lnrpc.PayReq
		err           error
		want          time.Time
		calls         int
	}{
		{name: "Keysend has no invoice"},
		{name: "invoice creation differs from payment start", request: "invoice", response: &lnrpc.PayReq{Timestamp: 100}, want: time.Unix(100, 0).UTC(), calls: 1},
		{name: "decode failure is optional", request: "invoice", err: errors.New("unavailable"), calls: 1},
		{name: "missing response", request: "invoice", calls: 1},
		{name: "missing timestamp", request: "invoice", response: &lnrpc.PayReq{}, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &invoiceTimestampClient{response: tc.response, err: tc.err}
			started := time.Unix(200, 0).UTC()
			details := PaymentDetails{PaymentRequest: tc.request, StartedAt: started}
			populateInvoiceCreation(context.Background(), client, &details)
			if !details.InvoiceCreatedAt.Equal(tc.want) || !details.StartedAt.Equal(started) || client.calls != tc.calls {
				t.Fatalf("unexpected invoice timestamp or changed payment start: %+v; calls=%d", details, client.calls)
			}
		})
	}
}
