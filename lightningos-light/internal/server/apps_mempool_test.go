package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestMempoolExternalBitcoinUsesFixedConsumerBoundary(t *testing.T) {
	dir := t.TempDir()
	paths := mempoolPaths{EnvPath: filepath.Join(dir, ".env")}
	values := mempoolRuntimeValues{
		BitcoinRPCUser: "rpc-user",
		BitcoinRPCPass: "rpc-pass",
		BitcoinRPCHost: appmanifest.BitcoinConsumerHostGateway,
		BitcoinRPCPort: 18443,
		DBPassword:     "db-pass",
		DBRootPassword: "db-root-pass",
	}
	if err := ensureMempoolEnv(paths, values); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "MEMPOOL_BITCOIN_RPC_HOST=172.31.253.1") {
		t.Fatalf("fixed gateway missing from env: %s", raw)
	}
	compose := mempoolComposeContents(paths)
	for _, want := range []string{"CORE_RPC_HOST: \"${MEMPOOL_BITCOIN_RPC_HOST}\"", "name: bitcoincore_default"} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q\n%s", want, compose)
		}
	}
}
