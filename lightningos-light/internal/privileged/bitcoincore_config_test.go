package privileged

import (
	"strings"
	"testing"
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
