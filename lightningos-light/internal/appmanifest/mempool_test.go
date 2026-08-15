package appmanifest

import (
	"strings"
	"testing"
)

func testMempoolRuntime() MempoolRuntime {
	return MempoolRuntime{
		BitcoinMode: MempoolBitcoinModeApp, Network: "bitcoin", BitcoinRPCUser: "rpc-user", BitcoinRPCPass: "rpc-pass",
		DBPassword: "db-password", DBRootPassword: "root-password",
	}
}

func TestMempoolRuntimeEnvironmentRoundTrip(t *testing.T) {
	runtime := testMempoolRuntime()
	raw, err := MempoolRuntimeEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMempoolRuntimeEnv([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed != runtime {
		t.Fatalf("parsed runtime = %#v, want %#v", parsed, runtime)
	}
	for _, mutation := range []string{
		strings.Replace(raw, "MEMPOOL_BITCOIN_MODE=app", "MEMPOOL_BITCOIN_MODE=remote", 1),
		raw + "UNKNOWN=value\n",
		strings.Replace(raw, "MEMPOOL_DB_PASSWORD=db-password", "MEMPOOL_DB_PASSWORD=bad value", 1),
	} {
		if _, err := ParseMempoolRuntimeEnv([]byte(mutation)); err == nil {
			t.Fatalf("tampered environment accepted: %q", mutation)
		}
	}
}

func TestMempoolComposeIsClosedAndHardened(t *testing.T) {
	compose, err := MempoolCompose(testMempoolRuntime())
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		MempoolFrontendImage, MempoolBackendImage, MempoolDatabaseImage,
		`user: "1000:1000"`, `user: "999:999"`, "cap_drop:\n      - ALL", "no-new-privileges:true",
		"/run/frontend:rw,exec,nosuid,nodev,size=512m,uid=1000,gid=1000,mode=0700",
		"name: bitcoincore_default", "name: electrs_default", "name: mempool_dbdata", "name: mempool_cache",
		`MARIADB_AUTO_UPGRADE: "1"`,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q", required)
		}
	}
	for _, secret := range []string{"rpc-user", "rpc-pass", "db-password", "root-password"} {
		if strings.Contains(compose, secret) {
			t.Fatalf("compose contains secret %q", secret)
		}
	}
}

func TestMempoolComposeUsesEnrolledDataDirectory(t *testing.T) {
	runtime := testMempoolRuntime()
	runtime.DataDir = "/mnt/chain/lightningos/mempool"
	compose, err := MempoolCompose(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, mount := range []string{
		"/mnt/chain/lightningos/mempool/db:/var/lib/mysql",
		"/mnt/chain/lightningos/mempool/cache:/run/backend/cache",
	} {
		if !strings.Contains(compose, mount) {
			t.Fatalf("Mempool compose missing external mount %q", mount)
		}
	}
	if strings.Contains(compose, "name: "+MempoolDBVolume) || strings.Contains(compose, "name: "+MempoolCacheVolume) {
		t.Fatal("Mempool external storage retained a Docker-root named volume")
	}
}
