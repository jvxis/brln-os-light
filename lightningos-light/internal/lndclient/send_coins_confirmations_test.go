package lndclient

import (
	"context"
	"testing"

	"lightningos-light/lnrpc"
)

type sendCoinsConfirmationFixture struct {
	lnrpc.UnimplementedLightningServer
	requests chan *lnrpc.SendCoinsRequest
}

func (f *sendCoinsConfirmationFixture) SendCoins(_ context.Context, request *lnrpc.SendCoinsRequest) (*lnrpc.SendCoinsResponse, error) {
	f.requests <- request
	return &lnrpc.SendCoinsResponse{Txid: "fixture-txid"}, nil
}

func TestSendCoinsConfirmationPolicyPreservesExistingCallers(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing wallet send", true: "succession sweep"}[explicit], func(t *testing.T) {
			f := &sendCoinsConfirmationFixture{requests: make(chan *lnrpc.SendCoinsRequest, 1)}
			client := closeTestClient(t, f)
			var txid string
			var err error
			if explicit {
				txid, err = client.SendCoinsWithMinConfirmations(context.Background(), "fixture-destination", 0, 2, true, 6)
			} else {
				txid, err = client.SendCoins(context.Background(), "fixture-destination", 1234, 2, false)
			}
			if err != nil || txid != "fixture-txid" {
				t.Fatalf("send failed: %q %v", txid, err)
			}
			request := <-f.requests
			if request.Addr != "fixture-destination" || request.SatPerVbyte != 2 || request.SpendUnconfirmed {
				t.Fatalf("changed payment policy: %+v", request)
			}
			if explicit {
				if request.MinConfs != 6 || !request.SendAll || request.Amount != 0 {
					t.Fatalf("succession confirmation policy lost: %+v", request)
				}
			} else if request.MinConfs != 0 || request.SendAll || request.Amount != 1234 {
				t.Fatalf("existing send defaults changed: %+v", request)
			}
		})
	}
}

func TestSendCoinsExplicitConfirmationsCannotSilentlyUseDefault(t *testing.T) {
	f := &sendCoinsConfirmationFixture{requests: make(chan *lnrpc.SendCoinsRequest, 1)}
	client := closeTestClient(t, f)
	for _, minimum := range []int32{0, -1} {
		if _, err := client.SendCoinsWithMinConfirmations(context.Background(), "fixture-destination", 0, 2, true, minimum); err == nil {
			t.Fatal("invalid explicit confirmation policy accepted")
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("invalid policy submitted a native transaction")
	}
}
