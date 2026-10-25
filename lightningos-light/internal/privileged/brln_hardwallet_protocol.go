package privileged

import (
	"context"
	"errors"
)

const (
	OperationHardwalletStatus  Operation = "app.brlnhardwallet.status"
	OperationHardwalletControl Operation = "app.brlnhardwallet.control"
)

type HardwalletControlParams struct {
	Action string `json:"action"`
	Source string `json:"source,omitempty"`
}
type HardwalletState struct {
	Installed bool     `json:"installed"`
	Status    string   `json:"status"`
	Source    string   `json:"source,omitempty"`
	BindIPs   []string `json:"bind_ips,omitempty"`
}
type HardwalletManager interface {
	Status(context.Context) (HardwalletState, error)
	Control(context.Context, HardwalletControlParams, bool) (HardwalletState, error)
}

func validateHardwalletControl(p HardwalletControlParams) error {
	switch p.Action {
	case "ensure", "start", "stop", "remove", "firewall":
	default:
		return errors.New("Hardwallet action not allowed")
	}
	if p.Source != "" && (p.Action != "ensure" || (p.Source != "local" && p.Source != "club")) {
		return errors.New("Hardwallet source not allowed")
	}
	return nil
}
func (c *Client) HardwalletStatus(ctx context.Context) (bool, string, string, []string, error) {
	response, err := c.call(ctx, OperationHardwalletStatus, struct{}{}, false)
	if err != nil {
		return false, "", "", nil, err
	}
	var state HardwalletState
	if err = decodeStrict(response.Result, &state); err != nil {
		return false, "", "", nil, err
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" {
		return false, "", "", nil, errors.New("invalid Hardwallet state")
	}
	return state.Installed, state.Status, state.Source, state.BindIPs, nil
}
func (c *Client) HardwalletControl(ctx context.Context, action, source string, dry bool) error {
	_, err := c.call(ctx, OperationHardwalletControl, HardwalletControlParams{Action: action, Source: source}, dry)
	return err
}
