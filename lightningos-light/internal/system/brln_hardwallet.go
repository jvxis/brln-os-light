package system

import (
	"context"
	"errors"
)

type hardwalletPrivilegedClient interface {
	HardwalletStatus(context.Context) (bool, string, string, string, error)
	HardwalletControl(context.Context, string, string, bool) error
}
type HardwalletBrokerState struct {
	Installed      bool
	Status, Source string
	BindIP         string
}

func HardwalletStatusWithBroker(ctx context.Context) (HardwalletBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return HardwalletBrokerState{}, errors.New("Hardwallet requires broker enforce mode")
	}
	c, ok := client.(hardwalletPrivilegedClient)
	if !ok {
		return HardwalletBrokerState{}, errors.New("broker does not support Hardwallet")
	}
	installed, status, source, bindIP, err := c.HardwalletStatus(ctx)
	return HardwalletBrokerState{Installed: installed, Status: status, Source: source, BindIP: bindIP}, err
}
func HardwalletControlWithBroker(ctx context.Context, action, source string) error {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return errors.New("Hardwallet requires broker enforce mode")
	}
	c, ok := client.(hardwalletPrivilegedClient)
	if !ok {
		return errors.New("broker does not support Hardwallet")
	}
	return c.HardwalletControl(ctx, action, source, false)
}
