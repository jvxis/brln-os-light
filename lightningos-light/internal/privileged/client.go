package privileged

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	BrokerExecutablePath = "/usr/local/libexec/lightningos-privileged"
	SudoExecutablePath   = "/usr/bin/sudo"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeShadow   Mode = "shadow"
	ModeEnforce  Mode = "enforce"
)

type Transport interface {
	Do(ctx context.Context, request Request) (Response, error)
}

type Client struct {
	mode      Mode
	timeout   time.Duration
	transport Transport
	logger    *log.Logger
}

type ClientError struct {
	Code    string
	Message string
}

func (err *ClientError) Error() string {
	if err == nil {
		return ""
	}
	if err.Code == "" {
		return err.Message
	}
	return err.Code + ": " + err.Message
}

func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeDisabled:
		return ModeDisabled, nil
	case ModeShadow:
		return ModeShadow, nil
	case ModeEnforce:
		return ModeEnforce, nil
	default:
		return "", fmt.Errorf("invalid privileged broker mode")
	}
}

func NewClient(modeValue string, timeout time.Duration, logger *log.Logger) (*Client, error) {
	mode, err := ParseMode(modeValue)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &Client{
		mode:      mode,
		timeout:   timeout,
		transport: &ExecTransport{},
		logger:    logger,
	}, nil
}

func NewClientWithTransport(mode Mode, timeout time.Duration, transport Transport, logger *log.Logger) *Client {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &Client{mode: mode, timeout: timeout, transport: transport, logger: logger}
}

func (client *Client) Mode() string {
	if client == nil {
		return string(ModeDisabled)
	}
	return string(client.mode)
}

func (client *Client) SelfTest(ctx context.Context) error {
	_, err := client.call(ctx, OperationSelfTest, struct{}{}, false)
	return err
}

func (client *Client) ServiceStatus(ctx context.Context, unit string) (string, error) {
	response, err := client.call(ctx, OperationServiceStatus, ServiceStatusParams{Unit: unit}, false)
	if err != nil {
		return "", err
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker service status response")
	}
	return result.Status, nil
}

func (client *Client) RestartService(ctx context.Context, unit string, noBlock bool, dryRun bool) error {
	_, err := client.call(ctx, OperationServiceRestart, ServiceRestartParams{
		Unit:    unit,
		NoBlock: noBlock,
	}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected service.restart: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted service.restart for %s", unit)
		}
	}
	return err
}

func (client *Client) EnableLogin(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationFilesEnableLogin, struct{}{}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected files.enable_login: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted files.enable_login")
		}
	}
	return err
}

func (client *Client) AppLifecycle(ctx context.Context, appID string, action string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppLifecycle, AppLifecycleParams{AppID: appID, Action: AppLifecycleAction(action)}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.compose.lifecycle: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.compose.lifecycle for %s/%s", appID, action)
		}
	}
	return err
}

func (client *Client) RemoveApp(ctx context.Context, appID string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppRemove, AppRemoveParams{AppID: appID}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.compose.remove: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.compose.remove for %s", appID)
		}
	}
	return err
}

func (client *Client) InspectApp(ctx context.Context, appID string) (string, float64, error) {
	response, err := client.call(ctx, OperationAppInspect, AppInspectParams{AppID: appID}, false)
	if err != nil {
		if client != nil && client.mode == ModeShadow && client.logger != nil {
			client.logger.Printf("privileged broker shadow inspection rejected app.compose.inspect: %v", err)
		}
		return "", 0, err
	}
	var result AppInspection
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", 0, errors.New("invalid broker app inspection response")
	}
	if result.Status != "running" && result.Status != "stopped" {
		return "", 0, errors.New("invalid broker app inspection status")
	}
	if result.CPUPercentRaw < 0 {
		return "", 0, errors.New("invalid broker app CPU percentage")
	}
	if client.mode == ModeShadow && client.logger != nil {
		client.logger.Printf("privileged broker shadow inspection accepted app.compose.inspect for %s", appID)
	}
	return result.Status, result.CPUPercentRaw, nil
}

func (client *Client) call(ctx context.Context, operation Operation, params any, dryRun bool) (Response, error) {
	var response Response
	if client == nil || client.transport == nil {
		return response, errors.New("privileged broker client unavailable")
	}
	if client.mode == ModeDisabled {
		return response, errors.New("privileged broker disabled")
	}
	rawParams, err := MarshalParams(params)
	if err != nil {
		return response, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return response, errors.New("failed to create broker request id")
	}
	request := Request{
		Version:   ProtocolVersion,
		RequestID: requestID,
		Operation: operation,
		DryRun:    dryRun,
		Params:    rawParams,
	}
	if err := ValidateRequest(request); err != nil {
		return response, err
	}
	callCtx, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	response, err = client.transport.Do(callCtx, request)
	if err != nil {
		return response, err
	}
	if response.RequestID != request.RequestID {
		return response, errors.New("broker response request_id mismatch")
	}
	if !response.OK {
		return response, &ClientError{Code: response.Error.Code, Message: response.Error.Message}
	}
	return response, nil
}

type ExecTransport struct{}

func (transport *ExecTransport) Do(ctx context.Context, request Request) (Response, error) {
	var response Response
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	if len(payload) > MaxMessageBytes {
		return response, errors.New("broker request too large")
	}

	command := exec.CommandContext(ctx, SudoExecutablePath, "-n", BrokerExecutablePath)
	command.Stdin = bytes.NewReader(payload)
	stdout := newBoundedOutput(MaxMessageBytes)
	command.Stdout = stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return response, fmt.Errorf("privileged broker execution failed: %w", err)
	}
	if stdout.Overflowed() {
		return response, errors.New("privileged broker response too large")
	}
	response, err = DecodeResponse(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return response, err
	}
	return response, nil
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
