package privileged

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestTorUpgradeHelperDigestAndPayloadSize(t *testing.T) {
	helper, err := os.ReadFile("../server/assets/check-tor-update.sh")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(helper)
	if got := hex.EncodeToString(digest[:]); got != torUpgradeHelperSHA256 {
		t.Fatalf("broker/helper digest mismatch: got %s", got)
	}
	if len(helper) >= 48*1024 {
		t.Fatalf("helper exceeds broker payload limit: %d bytes", len(helper))
	}
}
