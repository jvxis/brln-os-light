// breez-bridge-probe checks route discovery without creating orders or paying.
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

const baseUSDCContract = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913"

func isBaseUSDC(side sdk.BridgeRoutePairSide) bool {
	return side.Chain == "base" && side.Asset == "USDC" && side.Decimals == 6 &&
		side.ChainId != nil && *side.ChainId == "8453" &&
		side.ContractAddress != nil && strings.EqualFold(*side.ContractAddress, baseUSDCContract)
}

func isLightningBTC(side sdk.BridgeRoutePairSide) bool {
	return side.Chain == "lightning" && side.Asset == "BTC"
}

func matchingRouteCount(routes []sdk.BridgeRoutePair, toBase bool) int {
	count := 0
	for _, route := range routes {
		if toBase && isLightningBTC(route.Source) && isBaseUSDC(route.Destination) ||
			!toBase && isBaseUSDC(route.Source) && isLightningBTC(route.Destination) {
			count++
		}
	}
	return count
}

func validBaseAddress(address string) bool {
	if len(address) != 42 || !strings.HasPrefix(address, "0x") {
		return false
	}
	_, err := hex.DecodeString(address[2:])
	return err == nil
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "--check-bindings" {
		// Importing the binding verifies its native ABI during package initialization.
		fmt.Println("Breez native bindings loaded")
		return nil
	}

	if len(args) != 2 || !validBaseAddress(args[0]) || strings.TrimSpace(args[1]) == "" {
		return fmt.Errorf("usage: breez-bridge-probe <Base recipient address> <Lightning recipient>")
	}
	apiKey := os.Getenv("BREEZ_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("BREEZ_API_KEY is required for route discovery")
	}
	client, err := sdk.NewBridgingClient(sdk.DefaultBridgingConfig(apiKey))
	if err != nil {
		return fmt.Errorf("Breez bridging client initialization failed")
	}
	defer client.Destroy()

	checks := []struct {
		label     string
		recipient string
		toBase    bool
	}{
		{"Lightning -> native USDC on Base", args[0], true},
		{"Native USDC on Base -> Lightning", args[1], false},
	}
	missing := false
	for _, check := range checks {
		response, err := client.GetRoutes(sdk.GetBridgeRoutesRequest{Recipient: check.recipient})
		if err != nil {
			return fmt.Errorf("%s: route lookup failed", check.label)
		}
		count := matchingRouteCount(response.Routes, check.toBase)
		fmt.Printf("%s: %d matching route(s)\n", check.label, count)
		missing = missing || count == 0
	}
	if missing {
		return fmt.Errorf("at least one required route is unavailable")
	}
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
