package server

import (
	"context"
	"errors"
	"fmt"

	"lightningos-light/internal/system"
)

// ensureLocalExternalBitcoinConsumerNetwork creates or validates the one
// manager-owned Docker boundary used by containers that consume a native
// Bitcoin Core service. It deliberately does not read, write, start, stop, or
// restart the external service.
func ensureLocalExternalBitcoinConsumerNetwork(ctx context.Context) error {
	return ensureBitcoinConsumerNetwork(ctx)
}

func ensureBitcoinConsumerNetwork(ctx context.Context) error {
	handled, err := system.EnsureBitcoinConsumerNetworkWithBroker(ctx)
	if err != nil {
		return fmt.Errorf("bitcoin consumer network unavailable: %w", err)
	}
	if !handled {
		return errors.New("external Bitcoin Core consumers require the privileged broker in enforce mode")
	}
	return nil
}
