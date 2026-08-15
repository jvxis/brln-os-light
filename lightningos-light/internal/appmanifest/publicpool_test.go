package appmanifest

import (
	"strings"
	"testing"
)

func validPublicPoolRemoteRuntime() PublicPoolRuntime {
	return PublicPoolRuntime{BitcoinMode: PublicPoolBitcoinRemote, BitcoinRPCURL: "http://bitcoin.example", BitcoinRPCPort: 8332, BitcoinRPCUser: "rpcuser", BitcoinRPCPass: "rpcpass", BitcoinZMQHost: "tcp://bitcoin.example:28332"}
}

func TestPublicPoolManifestIsImmutableAndHardened(t *testing.T) {
	runtime := validPublicPoolRemoteRuntime()
	env, err := PublicPoolEnv(runtime)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePublicPoolEnv([]byte(env))
	if err != nil || parsed != runtime {
		t.Fatalf("parse=%#v/%v", parsed, err)
	}
	compose, err := PublicPoolCompose(PublicPoolComposePaths{DataDir: "/var/lib/lightningos/apps-data/publicpool/db", CaddyfilePath: "/var/lib/lightningos-privileged/apps/publicpool/Caddyfile"}, runtime.BitcoinMode)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{PublicPoolBackendImage, PublicPoolUIImage, `user: "65532:65532"`, "read_only: true", "cap_drop:\n      - ALL", "no-new-privileges:true", ":/public-pool/DB:rw", ":/etc/caddy/Caddyfile:ro"} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q", required)
		}
	}
	if strings.Contains(compose, ":latest") || strings.Contains(compose, "docker.sock") || strings.Contains(compose, "privileged: true") {
		t.Fatal("compose contains mutable or privileged runtime")
	}
}

func TestPublicPoolEnvRejectsTamperingAndDuplicateEmptyValue(t *testing.T) {
	env, _ := PublicPoolEnv(validPublicPoolRemoteRuntime())
	for _, raw := range []string{
		strings.Replace(env, "NETWORK=mainnet", "NETWORK=testnet", 1),
		env + "EVIL=value\n",
		strings.Replace(env, "BITCOIN_ZMQ_HOST=tcp://bitcoin.example:28332\n", "BITCOIN_ZMQ_HOST=\nBITCOIN_ZMQ_HOST=\n", 1),
	} {
		if _, err := ParsePublicPoolEnv([]byte(raw)); err == nil {
			t.Fatal("tampered environment accepted")
		}
	}
}

func TestPublicPoolRuntimeRejectsUnsafeEndpointsAndCredentials(t *testing.T) {
	for _, mutate := range []func(*PublicPoolRuntime){
		func(v *PublicPoolRuntime) { v.BitcoinRPCURL = "https://bitcoin.example" },
		func(v *PublicPoolRuntime) { v.BitcoinRPCURL = "http://127.0.0.1" },
		func(v *PublicPoolRuntime) { v.BitcoinRPCPass = "bad$password" },
		func(v *PublicPoolRuntime) { v.BitcoinZMQHost = "tcp://127.0.0.1:28332" },
	} {
		value := validPublicPoolRemoteRuntime()
		mutate(&value)
		if err := ValidatePublicPoolRuntime(value); err == nil {
			t.Fatalf("unsafe runtime accepted: %#v", value)
		}
	}
}
