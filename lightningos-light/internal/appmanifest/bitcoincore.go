package appmanifest

import "errors"

const (
	BitcoinCoreID = "bitcoincore"

	// BitcoinCoreImage is deliberately pinned to an explicit Bitcoin Core
	// release. The upstream `latest` tag tracks major releases and is therefore
	// not a stable privileged execution contract.
	BitcoinCoreImage = "bitcoin/bitcoin:31.1"

	BitcoinCoreImageNode AppImageVariant = "node"
)

func BitcoinCoreImageForVariant(variant AppImageVariant) (string, error) {
	if variant != BitcoinCoreImageNode {
		return "", errors.New("bitcoin core image variant is not allowed")
	}
	return BitcoinCoreImage, nil
}
