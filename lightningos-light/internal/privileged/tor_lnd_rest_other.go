//go:build !linux

package privileged

import (
	"context"
	"errors"
)

func (manager *NativeTorLNDRESTManager) status(context.Context) (TorLNDRESTState, error) {
	return TorLNDRESTState{}, errors.New("Tor onion services are supported only on Linux")
}

func (manager *NativeTorLNDRESTManager) ensure(context.Context) (TorLNDRESTState, error) {
	return TorLNDRESTState{}, errors.New("Tor onion services are supported only on Linux")
}
