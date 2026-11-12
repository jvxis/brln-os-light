package system

import (
	"context"
	"errors"

	"lightningos-light/internal/privileged"
)

type torLNDRESTPrivilegedClient interface {
	TorLNDREST(ctx context.Context, ensure bool) (privileged.TorLNDRESTState, error)
}

// TorLNDRESTWithBroker reads, or with ensure creates, the onion service for
// the local LND REST listener. The torrc write and the Tor reload need root,
// so this only runs through an enforcing broker; without one it is unhandled.
func TorLNDRESTWithBroker(ctx context.Context, ensure bool) (privileged.TorLNDRESTState, bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return privileged.TorLNDRESTState{}, false, nil
	}
	torClient, ok := client.(torLNDRESTPrivilegedClient)
	if !ok {
		return privileged.TorLNDRESTState{}, true, errors.New("privileged broker does not support the LND REST onion")
	}
	state, err := torClient.TorLNDREST(ctx, ensure)
	return state, true, err
}
