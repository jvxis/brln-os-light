package privileged

import (
	"strings"
	"testing"

	"lightningos-light/internal/appmanifest"
)

func TestValidateBitcoinCoreConfigContent(t *testing.T) {
	for _, content := range []string{
		"server=1\n",
		"rpcuser=lightningos\nrpcpassword=secret\n",
	} {
		if err := validateBitcoinCoreConfigContent(content); err != nil {
			t.Fatalf("valid content rejected: %v", err)
		}
	}
	for _, content := range []string{
		"",
		"server=1",
		"server=1\r\n",
		"server=1\x00\n",
		strings.Repeat("x", maxBitcoinCoreConfigBytes) + "\n",
	} {
		if err := validateBitcoinCoreConfigContent(content); err == nil {
			t.Fatalf("invalid content accepted (length %d)", len(content))
		}
	}
}

func TestBitcoinCoreConfigWithRPCAuthRejectsPlaintextAndInsertsBeforeSections(t *testing.T) {
	const auth = "lightningos:0123456789abcdef0123456789abcdef$0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	configured, err := bitcoinCoreConfigWithRPCAuth("server=1\n[regtest]\nrpcport=18443\n", auth)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configured, "server=1\nrpcauth="+auth+"\n[regtest]\n") {
		t.Fatalf("rpcauth was not inserted in the global section:\n%s", configured)
	}
	for _, content := range []string{
		"server=1\nrpcuser=lightningos\n",
		"server=1\nrpcpassword=secret\n",
		"server=1\nrpcauth=existing\n",
	} {
		if _, err := bitcoinCoreConfigWithRPCAuth(content, auth); err == nil {
			t.Fatalf("credential-bearing template accepted: %q", content)
		}
	}
}

func TestGenerateBitcoinCoreCredentialsProducesValidRPCAuth(t *testing.T) {
	credentials, err := generateBitcoinCoreCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBitcoinCoreCredentials(credentials); err != nil {
		t.Fatal(err)
	}
	if credentials.User != "lightningos" || len(credentials.Password) != 64 || !strings.HasPrefix(credentials.RPCAuth, "lightningos:") {
		t.Fatalf("unexpected generated credentials metadata: user=%q password_length=%d", credentials.User, len(credentials.Password))
	}
}

func TestBitcoinCoreConfigWithElectrsRPCAuthReplacesLegacyCredentialAndIsIdempotent(t *testing.T) {
	const auth = "electrs:0123456789abcdef0123456789abcdef$0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const oldElectrs = "electrs:ffffffffffffffffffffffffffffffff$eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	legacy := "server=1\nrpcauth=legacy:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa$bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\nrpcauth=" + oldElectrs + "\n[main]\nrpcport=8332\n"
	configured, changed, err := bitcoinCoreConfigWithElectrsRPCAuth(legacy, auth)
	if err != nil || !changed {
		t.Fatalf("configured/changed/error=%q/%v/%v", configured, changed, err)
	}
	if !strings.Contains(configured, "rpcauth=legacy:") || strings.Contains(configured, oldElectrs) || !strings.Contains(configured, "rpcauth="+auth+"\n[main]") {
		t.Fatalf("unrelated credential changed or Electrs credential was not replaced:\n%s", configured)
	}
	again, changed, err := bitcoinCoreConfigWithElectrsRPCAuth(configured, auth)
	if err != nil || changed || again != configured {
		t.Fatalf("Electrs credential merge is not idempotent: changed=%v err=%v", changed, err)
	}
	for _, variant := range []string{
		"server=1\nRPCAUTH = electrs:ffffffffffffffffffffffffffffffff$eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n",
		"server=1\n  rpcauth=electrs:ffffffffffffffffffffffffffffffff$eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n",
	} {
		updated, changed, err := bitcoinCoreConfigWithElectrsRPCAuth(variant, auth)
		if err != nil || !changed || strings.Contains(updated, oldElectrs) || !strings.Contains(updated, "rpcauth="+auth) {
			t.Fatalf("legacy Electrs credential variant was not replaced: changed=%v err=%v content=%q", changed, err, updated)
		}
	}
}

func TestBitcoinCoreManagedRPCAuthPreservesIrreversibleLegacyHash(t *testing.T) {
	legacy := "server=1\nrpcauth=lightningos:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa$bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\n[main]\nrpcport=8332\n"
	managed := "lightningos:cccccccccccccccccccccccccccccccc$dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	updated, changed, err := bitcoinCoreConfigWithManagedRPCAuth(legacy, managed, appmanifest.BitcoinCoreRPCUser, true)
	if err != nil || !changed {
		t.Fatalf("managed migration changed/error=%v/%v", changed, err)
	}
	if !strings.Contains(updated, "rpcauth=lightningos:aaaaaaaa") || !strings.Contains(updated, "rpcauth="+managed) {
		t.Fatalf("managed migration did not preserve both hashes:\n%s", updated)
	}
	again, changed, err := bitcoinCoreConfigWithManagedRPCAuth(updated, managed, appmanifest.BitcoinCoreRPCUser, true)
	if err != nil || changed || again != updated {
		t.Fatalf("managed migration is not idempotent: changed/error=%v/%v", changed, err)
	}
}

func TestGenerateBitcoinCoreElectrsCredentialsUsesFixedUser(t *testing.T) {
	credentials, err := generateBitcoinCoreCredentialsForUser(appmanifest.ElectrsBitcoinRPCUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBitcoinCoreCredentialsForUser(credentials, appmanifest.ElectrsBitcoinRPCUser); err != nil {
		t.Fatal(err)
	}
	if credentials.User != appmanifest.ElectrsBitcoinRPCUser || !strings.HasPrefix(credentials.RPCAuth, appmanifest.ElectrsBitcoinRPCUser+":") {
		t.Fatalf("unexpected Electrs credentials: user=%q", credentials.User)
	}
}
