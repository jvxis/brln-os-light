package main

import (
	"math/big"
	"strings"

	sdk "github.com/breez/breez-sdk-spark-go/breez_sdk_spark"
)

const solanaUSDCMint = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v"
const solanaBase58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

func isSolanaUSDC(side sdk.BridgeRoutePairSide) bool {
	return side.Chain == "solana" && side.Asset == "USDC" && side.Decimals == 6 &&
		side.ContractAddress != nil && *side.ContractAddress == solanaUSDCMint
}

func matchingSolanaRoutes(routes []sdk.BridgeRoutePair, toSolana bool) []sdk.BridgeRoutePair {
	var matches []sdk.BridgeRoutePair
	for _, route := range routes {
		if toSolana && isLightningBTC(route.Source) && isSolanaUSDC(route.Destination) ||
			!toSolana && isSolanaUSDC(route.Source) && isLightningBTC(route.Destination) {
			matches = append(matches, route)
		}
	}
	return matches
}

func validSolanaAddress(address string) bool {
	if len(address) < 32 || len(address) > 44 {
		return false
	}
	value, radix := new(big.Int), big.NewInt(58)
	for i := 0; i < len(address); i++ {
		digit := strings.IndexByte(solanaBase58, address[i])
		if digit < 0 {
			return false
		}
		value.Mul(value, radix).Add(value, big.NewInt(int64(digit)))
	}
	zeros := len(address) - len(strings.TrimLeft(address, "1"))
	return value.Sign() > 0 && zeros+len(value.Bytes()) == 32
}
