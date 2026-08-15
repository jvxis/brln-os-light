package server

import (
	"os"
	"strings"

	"lightningos-light/internal/config"
)

// resolveBitcoinRemoteRPCConfig returns the remote Bitcoin endpoint that is
// already authoritative for LND. Existing-node installs commonly keep these
// credentials only in lnd.conf, while managed installs also persist them in
// secrets.env. Reading either source avoids forcing an operator to re-enter or
// duplicate credentials during an in-place LightningOS upgrade.
func resolveBitcoinRemoteRPCConfig(cfg *config.Config) bitcoinRPCConfig {
	fallback := bitcoinRPCConfig{}
	if cfg != nil {
		fallback.Host = cfg.BitcoinRemote.RPCHost
		fallback.ZMQBlock = cfg.BitcoinRemote.ZMQRawBlock
		fallback.ZMQTx = cfg.BitcoinRemote.ZMQRawTx
	}
	fallback.User = strings.TrimSpace(os.Getenv("BITCOIN_RPC_USER"))
	fallback.Pass = strings.TrimSpace(os.Getenv("BITCOIN_RPC_PASS"))
	if fallback.User == "" || fallback.Pass == "" {
		fileUser, filePass := readBitcoinSecrets()
		if fallback.User == "" {
			fallback.User = fileUser
		}
		if fallback.Pass == "" {
			fallback.Pass = filePass
		}
	}

	tagged, taggedOK := readBitcoinTaggedRPCConfigFromLNDConf("remote")
	active, activeOK := readBitcoindRPCConfigFromLNDConf()
	return selectBitcoinRemoteRPCConfig(fallback, tagged, taggedOK, active, activeOK)
}

func selectBitcoinRemoteRPCConfig(fallback, tagged bitcoinRPCConfig, taggedOK bool, active bitcoinRPCConfig, activeOK bool) bitcoinRPCConfig {
	if taggedOK && !isLocalRPCHost(tagged.Host) {
		return tagged
	}
	if activeOK && !isLocalRPCHost(active.Host) {
		return active
	}
	return fallback
}
