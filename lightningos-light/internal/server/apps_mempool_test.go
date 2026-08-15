package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestMempoolExternalBitcoinUsesFixedConsumerBoundary(t *testing.T) {
	runtime := appmanifest.MempoolRuntime{
		BitcoinMode: appmanifest.MempoolBitcoinModeNative, Network: "bitcoin", BitcoinRPCUser: "rpc-user",
		BitcoinRPCPass: "rpc-pass", DBPassword: "db-pass", DBRootPassword: "db-root-pass",
	}
	compose := mempoolComposeContents(mempoolPaths{}, runtime)
	for _, want := range []string{
		`CORE_RPC_HOST: "172.31.253.1"`, "name: bitcoincore_default", "name: electrs_default",
		appmanifest.MempoolFrontendImage, appmanifest.MempoolBackendImage, appmanifest.MempoolDatabaseImage,
		`user: "1000:1000"`, `user: "999:999"`, "cap_drop:\n      - ALL", "no-new-privileges:true", `MARIADB_AUTO_UPGRADE: "1"`,
	} {
		if !strings.Contains(compose, want) {
			t.Fatalf("compose missing %q\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "rpc-pass") || strings.Contains(compose, "db-pass") {
		t.Fatal("Mempool compose contains runtime credentials")
	}
}

func TestMempoolLegacyDatabaseCredentialsArePreserved(t *testing.T) {
	dir := t.TempDir()
	paths := mempoolPaths{EnvPath: filepath.Join(dir, ".env")}
	legacy := "MEMPOOL_BITCOIN_RPC_USER=old\nMEMPOOL_BITCOIN_RPC_PASS=old\nMEMPOOL_BITCOIN_RPC_HOST=bitcoind\nMEMPOOL_BITCOIN_RPC_PORT=8332\nMEMPOOL_DB_PASSWORD=preserved-db\nMEMPOOL_DB_ROOT_PASSWORD=preserved-root\n"
	if err := os.WriteFile(paths.EnvPath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readEnvValue(paths.EnvPath, "MEMPOOL_DB_PASSWORD"); got != "preserved-db" {
		t.Fatalf("legacy database credential = %q", got)
	}
	if got := readEnvValue(paths.EnvPath, "MEMPOOL_DB_ROOT_PASSWORD"); got != "preserved-root" {
		t.Fatalf("legacy root credential = %q", got)
	}
}
