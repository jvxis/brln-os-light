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
