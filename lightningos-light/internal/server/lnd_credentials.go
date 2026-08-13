package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"time"

	"lightningos-light/internal/privileged"
)

type ensureLNDManagerCredentialFunc func(context.Context) (privileged.LNDManagerCredentialState, bool, error)

func convergeLNDManagerCredential(ctx context.Context, attempts int, delay time.Duration, ensure ensureLNDManagerCredentialFunc) (privileged.LNDManagerCredentialState, bool, error) {
	if attempts < 1 || delay < 0 || ensure == nil {
		return privileged.LNDManagerCredentialState{}, false, errors.New("LND manager credential convergence is unavailable")
	}
	var state privileged.LNDManagerCredentialState
	var handled bool
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return state, handled, ctxErr
		}
		state, handled, err = ensure(ctx)
		if !handled || (err == nil && state.Status != "pending") {
			return state, handled, err
		}
		if attempt+1 == attempts {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return state, handled, ctx.Err()
		case <-timer.C:
		}
	}
	return state, handled, err
}

func lndCredentialEqualsNativeAdmin(credential []byte) (bool, error) {
	admin, err := os.ReadFile(lndAdminMacaroonPath)
	if err != nil {
		// After manager credential migration the native admin macaroon is 0600
		// and intentionally unreadable to the manager. Every privileged app
		// placement independently repeats the equality check as root.
		if errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, errors.New("native LND admin credential is unavailable")
	}
	return bytes.Equal(credential, admin), nil
}
