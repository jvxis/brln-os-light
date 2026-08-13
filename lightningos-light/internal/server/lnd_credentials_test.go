package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"lightningos-light/internal/privileged"
)

func TestConvergeLNDManagerCredentialRetriesTransientFailureAndPending(t *testing.T) {
	calls := 0
	state, handled, err := convergeLNDManagerCredential(context.Background(), 4, 0, func(context.Context) (privileged.LNDManagerCredentialState, bool, error) {
		calls++
		switch calls {
		case 1:
			return privileged.LNDManagerCredentialState{}, true, errors.New("transient config race")
		case 2:
			return privileged.LNDManagerCredentialState{Status: "pending"}, true, nil
		default:
			return privileged.LNDManagerCredentialState{Status: "ready", ConfiguredPath: privileged.DefaultLNDManagerMacaroonPath, AdminProtected: true}, true, nil
		}
	})
	if err != nil || !handled || state.Status != "ready" || calls != 3 {
		t.Fatalf("unexpected convergence: state=%#v handled=%v calls=%d err=%v", state, handled, calls, err)
	}
}

func TestConvergeLNDManagerCredentialStopsOnUnsupportedBroker(t *testing.T) {
	calls := 0
	_, handled, err := convergeLNDManagerCredential(context.Background(), 4, 0, func(context.Context) (privileged.LNDManagerCredentialState, bool, error) {
		calls++
		return privileged.LNDManagerCredentialState{}, false, nil
	})
	if err != nil || handled || calls != 1 {
		t.Fatalf("unexpected unsupported convergence: handled=%v calls=%d err=%v", handled, calls, err)
	}
}

func TestConvergeLNDManagerCredentialHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, handled, err := convergeLNDManagerCredential(ctx, 4, time.Hour, func(context.Context) (privileged.LNDManagerCredentialState, bool, error) {
		calls++
		cancel()
		return privileged.LNDManagerCredentialState{Status: "pending"}, true, nil
	})
	if !handled || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("unexpected canceled convergence: handled=%v calls=%d err=%v", handled, calls, err)
	}
}
