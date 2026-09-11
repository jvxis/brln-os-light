package system

import (
	"context"
	"errors"
)

func MeshControlWithBroker(ctx context.Context, action, device string) (string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return "", errors.New("LOS Mesh requires privileged broker enforce mode")
	}
	m, ok := client.(interface {
		MeshControl(context.Context, string, string) (string, error)
	})
	if !ok {
		return "", errors.New("upgrade the privileged broker for LOS Mesh")
	}
	return m.MeshControl(ctx, action, device)
}
