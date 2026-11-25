// breez-bridge-probe checks route discovery and optional unfunded quotes.
package main

import (
	"encoding/hex"
	"flag"
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

func matchingRoutes(routes []sdk.BridgeRoutePair, toBase bool) []sdk.BridgeRoutePair {
	var matches []sdk.BridgeRoutePair
	for _, route := range routes {
		if toBase && isLightningBTC(route.Source) && isBaseUSDC(route.Destination) ||
			!toBase && isBaseUSDC(route.Source) && isLightningBTC(route.Destination) {
			matches = append(matches, route)
		}
	}
	return matches
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

	var quote quoteOptions
	flags := flag.NewFlagSet("breez-bridge-probe", flag.ContinueOnError)
	flags.Uint64Var(&quote.amount, "quote-usdc-micros", 0, "create unfunded quotes using this amount in USDC micros")
	flags.StringVar(&quote.baseRefund, "base-refund", "", "Base refund destination for the reverse quote")
	flags.StringVar(&quote.lightningRefund, "lightning-refund", "", "Lightning refund destination for the forward quote")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := quote.validate(); err != nil {
		return err
	}
	args = flags.Args()
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
		routes := matchingRoutes(response.Routes, check.toBase)
		fmt.Printf("%s: %d matching route(s)\n", check.label, len(routes))
		missing = missing || len(routes) == 0
		if quote.amount == 0 || len(routes) == 0 {
			continue
		}
		if len(routes) != 1 {
			return fmt.Errorf("%s: quote mode requires one unambiguous route", check.label)
		}
		refund := quote.baseRefund
		if check.toBase {
			refund = quote.lightningRefund
		}
		if err := quoteUnfunded(client, routes[0], check.recipient, refund, check.toBase, quote.amount); err != nil {
			return fmt.Errorf("%s: %w", check.label, err)
		}
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
