package server

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoadPendingOpenFundingTxObservation(t *testing.T) {
	const txid = "1751749bbb418e25d12da6aba5ec5a6322fcb34383d14765f9a112b7829259e6"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tx/"+txid {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"fee": 423,
			"weight": 846,
			"vin": [{"sequence": 0}, {"sequence": 4294967295}],
			"status": {"confirmed": false}
		}`))
	}))
	defer server.Close()

	observation := loadPendingOpenFundingTxObservation(context.Background(), server.URL, txid)
	if observation.Status != "mempool" {
		t.Fatalf("status = %q, want mempool", observation.Status)
	}
	if observation.FeeSat != 423 {
		t.Fatalf("fee = %d, want 423", observation.FeeSat)
	}
	if observation.Vsize != 211.5 {
		t.Fatalf("vsize = %v, want 211.5", observation.Vsize)
	}
	if math.Abs(observation.EffectiveFeeRateSatVbyte-2) > 0.000001 {
		t.Fatalf("effective fee rate = %v, want 2", observation.EffectiveFeeRateSatVbyte)
	}
	if observation.RBF == nil || !*observation.RBF {
		t.Fatal("RBF = false or unavailable, want true")
	}
}

func TestLoadPendingOpenFundingTxObservationStatuses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "confirmed", statusCode: http.StatusOK, body: `{"fee":100,"weight":400,"vin":[{"sequence":4294967295}],"status":{"confirmed":true}}`, want: "confirmed"},
		{name: "not found", statusCode: http.StatusNotFound, want: "not_found"},
		{name: "upstream unavailable", statusCode: http.StatusBadGateway, want: "unavailable"},
		{name: "invalid response", statusCode: http.StatusOK, body: `{`, want: "unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			observation := loadPendingOpenFundingTxObservation(
				context.Background(), server.URL,
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			)
			if observation.Status != test.want {
				t.Fatalf("status = %q, want %q", observation.Status, test.want)
			}
		})
	}
}

func TestPendingOpenFundingTxID(t *testing.T) {
	const txid = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if got := pendingOpenFundingTxID(txid + ":1"); got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("txid = %q", got)
	}
	if got := pendingOpenFundingTxID("not-a-channel-point"); got != "" {
		t.Fatalf("invalid channel point returned %q", got)
	}
}
