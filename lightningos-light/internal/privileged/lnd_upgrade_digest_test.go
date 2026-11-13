package privileged

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// This must run on every platform. Native Linux execution tests alone do not
// catch a changed embedded helper when the release is assembled on Windows.
func TestLNDUpgradeHelperDigestMatchesEmbeddedAsset(t *testing.T) {
	helper, err := os.ReadFile("../server/assets/upgrade-lnd.sh")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(helper)
	if got := hex.EncodeToString(digest[:]); got != lndUpgradeHelperSHA256 {
		t.Fatalf("broker/helper digest mismatch: got %s, want %s", got, lndUpgradeHelperSHA256)
	}
	params, err := MarshalParams(LNDUpgradeStartParams{
		Version: "0.21.8-beta", HelperContent: string(helper), VerifyOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateRequest(Request{Version: ProtocolVersion, RequestID: "lnd_helper_digest", Operation: OperationLNDUpgradeStart, DryRun: true, Params: params}); err != nil {
		t.Fatalf("actual helper cannot pass the broker request contract: %v", err)
	}
}
