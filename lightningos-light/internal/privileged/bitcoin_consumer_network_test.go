package privileged

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"lightningos-light/internal/appmanifest"
)

const validBitcoinConsumerNetworkInspection = `bridge|local|false|bitcoincore|default|[{"Subnet":"172.31.253.0/24","Gateway":"172.31.253.1"}]`

func TestEnsureBitcoinConsumerNetworkCreatesOnlyTheFixedNetwork(t *testing.T) {
	inspectCalls := 0
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
			inspectCalls++
			if inspectCalls == 1 {
				return "", errors.New("not found"), true
			}
			return validBitcoinConsumerNetworkInspection, nil, true
		}
		if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "ls" {
			return "", nil, true
		}
		return "", nil, false
	}}
	manager := &ComposeAppManager{Runner: runner}
	state, err := manager.EnsureBitcoinConsumerNetwork(context.Background(), false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	wantCreate := recordedCommand{path: dockerPath, args: []string{
		"network", "create", "--driver", "bridge",
		"--subnet", appmanifest.BitcoinConsumerRPCSubnet,
		"--gateway", appmanifest.BitcoinConsumerHostGateway,
		"--label", "com.docker.compose.project=bitcoincore",
		"--label", "com.docker.compose.network=default",
		appmanifest.BitcoinConsumerNetwork,
	}}
	found := false
	for _, command := range runner.commands {
		if reflect.DeepEqual(command, wantCreate) {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixed network create command not found: %#v", runner.commands)
	}
}

func TestEnsureBitcoinConsumerNetworkPreservesValidExistingNetwork(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
			return validBitcoinConsumerNetworkInspection, nil, true
		}
		return "", nil, false
	}}
	state, err := (&ComposeAppManager{Runner: runner}).EnsureBitcoinConsumerNetwork(context.Background(), false)
	if err != nil || state.Status != "ready" || len(runner.commands) != 2 || runner.commands[1].path != ufwPath {
		t.Fatalf("state/error/commands=%#v/%v/%#v", state, err, runner.commands)
	}
}

func TestEnsureBitcoinConsumerNetworkRejectsExistingIncompatibleNetwork(t *testing.T) {
	for _, inspection := range []string{
		`bridge|local|false|other|default|[{"Subnet":"172.31.253.0/24","Gateway":"172.31.253.1"}]`,
		`bridge|local|false|bitcoincore|default|[{"Subnet":"172.31.254.0/24","Gateway":"172.31.254.1"}]`,
		`host|local|false|bitcoincore|default|[]`,
	} {
		runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
			if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
				return inspection, nil, true
			}
			return "", nil, false
		}}
		if _, err := (&ComposeAppManager{Runner: runner}).EnsureBitcoinConsumerNetwork(context.Background(), false); err == nil {
			t.Fatalf("incompatible inspection accepted: %s", inspection)
		}
		if len(runner.commands) != 1 {
			t.Fatalf("unexpected mutation for incompatible network: %#v", runner.commands)
		}
	}
}

func TestEnsureBitcoinConsumerNetworkDoesNotCreateWhenExistingNetworkCannotBeInspected(t *testing.T) {
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
			return "", errors.New("permission denied"), true
		}
		if path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "ls" {
			return appmanifest.BitcoinConsumerNetwork, nil, true
		}
		return "", nil, false
	}}
	if _, err := (&ComposeAppManager{Runner: runner}).EnsureBitcoinConsumerNetwork(context.Background(), false); err == nil {
		t.Fatal("expected inspection failure")
	}
	if len(runner.commands) != 2 {
		t.Fatalf("unexpected mutation after inspection failure: %#v", runner.commands)
	}
}

func TestEnsureBitcoinConsumerNetworkDryRunHasNoCommands(t *testing.T) {
	runner := &composeRecordingRunner{}
	state, err := (&ComposeAppManager{Runner: runner}).EnsureBitcoinConsumerNetwork(context.Background(), true)
	if err != nil || state.Status != "validated" || len(runner.commands) != 0 {
		t.Fatalf("state/error/commands=%#v/%v/%#v", state, err, runner.commands)
	}
}

func TestEnsureBitcoinConsumerNetworkUsesOnlyFixedPrivateBridgeFirewallRules(t *testing.T) {
	const networkID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	runner := &composeRecordingRunner{hook: func(path string, args []string) (string, error, bool) {
		switch {
		case path == dockerPath && len(args) >= 2 && args[0] == "network" && args[1] == "inspect" && args[len(args)-1] == bitcoinConsumerNetworkInspectFormat:
			return validBitcoinConsumerNetworkInspection, nil, true
		case path == ufwPath && reflect.DeepEqual(args, []string{"status"}):
			return "Status: active\n", nil, true
		case path == dockerPath && reflect.DeepEqual(args, []string{"network", "inspect", appmanifest.BitcoinConsumerNetwork, "--format", "{{.Id}}"}):
			return networkID, nil, true
		}
		return "", nil, false
	}}
	state, err := (&ComposeAppManager{Runner: runner}).EnsureBitcoinConsumerNetwork(context.Background(), false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("state/error=%#v/%v", state, err)
	}
	for _, port := range bitcoinConsumerTCPPorts {
		want := recordedCommand{path: ufwPath, args: []string{"allow", "in", "on", "br-0123456789ab", "to", "any", "port", strconv.Itoa(port), "proto", "tcp"}}
		found := false
		for _, command := range runner.commands {
			if reflect.DeepEqual(command, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixed firewall rule missing: %#v", want)
		}
	}
}
