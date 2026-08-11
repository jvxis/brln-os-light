package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const bitcoinConsumerNetworkInspectFormat = `{{.Driver}}|{{.Scope}}|{{.Internal}}|{{index .Labels "com.docker.compose.project"}}|{{index .Labels "com.docker.compose.network"}}|{{json .IPAM.Config}}`

var bitcoinConsumerNetworkIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

var bitcoinConsumerTCPPorts = [...]int{8332, 8333, 28332, 28333}

type bitcoinConsumerIPAMConfig struct {
	Subnet  string `json:"Subnet"`
	Gateway string `json:"Gateway"`
}

func (manager *ComposeAppManager) EnsureBitcoinConsumerNetwork(ctx context.Context, dryRun bool) (BitcoinConsumerNetworkState, error) {
	if manager == nil || manager.Runner == nil {
		return BitcoinConsumerNetworkState{}, errors.New("compose app manager is unavailable")
	}
	if dryRun {
		return BitcoinConsumerNetworkState{Status: "validated"}, nil
	}

	output, err := manager.inspectBitcoinConsumerNetwork(ctx)
	if err != nil {
		listed, listErr := manager.Runner.Run(ctx, dockerPath,
			"network", "ls",
			"--filter", "name=^"+appmanifest.BitcoinConsumerNetwork+"$",
			"--format", "{{.Name}}",
		)
		if listErr != nil || strings.TrimSpace(listed) != "" {
			return BitcoinConsumerNetworkState{}, errors.New("bitcoin consumer network inspection failed")
		}
		if _, createErr := manager.Runner.Run(ctx, dockerPath,
			"network", "create",
			"--driver", "bridge",
			"--subnet", appmanifest.BitcoinConsumerRPCSubnet,
			"--gateway", appmanifest.BitcoinConsumerHostGateway,
			"--label", "com.docker.compose.project="+appmanifest.BitcoinCoreProject,
			"--label", "com.docker.compose.network=default",
			appmanifest.BitcoinConsumerNetwork,
		); createErr != nil {
			return BitcoinConsumerNetworkState{}, errors.New("bitcoin consumer network creation failed")
		}
		output, err = manager.inspectBitcoinConsumerNetwork(ctx)
		if err != nil {
			return BitcoinConsumerNetworkState{}, errors.New("bitcoin consumer network inspection failed after creation")
		}
	}
	if err := validateBitcoinConsumerNetworkInspection(output); err != nil {
		return BitcoinConsumerNetworkState{}, err
	}
	if err := manager.ensureBitcoinConsumerFirewall(ctx); err != nil {
		return BitcoinConsumerNetworkState{}, err
	}
	return BitcoinConsumerNetworkState{Status: "ready"}, nil
}

func (manager *ComposeAppManager) ensureBitcoinConsumerFirewall(ctx context.Context) error {
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath,
		"network", "inspect", appmanifest.BitcoinConsumerNetwork,
		"--format", "{{.Id}}",
	)
	if err != nil {
		return errors.New("bitcoin consumer firewall bridge inspection failed")
	}
	networkID = strings.TrimSpace(networkID)
	if !bitcoinConsumerNetworkIDPattern.MatchString(networkID) {
		return errors.New("bitcoin consumer firewall bridge ID is invalid")
	}
	bridge := "br-" + networkID[:12]
	for _, port := range bitcoinConsumerTCPPorts {
		if _, err := manager.Runner.Run(ctx, ufwPath,
			"allow", "in", "on", bridge, "to", "any", "port", strconv.Itoa(port), "proto", "tcp",
		); err != nil {
			return errors.New("bitcoin consumer firewall update failed")
		}
	}
	return nil
}

func (manager *ComposeAppManager) inspectBitcoinConsumerNetwork(ctx context.Context) (string, error) {
	return manager.Runner.Run(ctx, dockerPath,
		"network", "inspect", appmanifest.BitcoinConsumerNetwork,
		"--format", bitcoinConsumerNetworkInspectFormat,
	)
}

func validateBitcoinConsumerNetworkInspection(output string) error {
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 6 || parts[0] != "bridge" || parts[1] != "local" || parts[2] != "false" ||
		parts[3] != appmanifest.BitcoinCoreProject || parts[4] != "default" {
		return errors.New("existing bitcoin consumer network is not managed by LightningOS")
	}
	var configs []bitcoinConsumerIPAMConfig
	if err := json.Unmarshal([]byte(parts[5]), &configs); err != nil || len(configs) != 1 ||
		configs[0].Subnet != appmanifest.BitcoinConsumerRPCSubnet ||
		configs[0].Gateway != appmanifest.BitcoinConsumerHostGateway {
		return errors.New("existing bitcoin consumer network has an incompatible IPAM configuration")
	}
	return nil
}
