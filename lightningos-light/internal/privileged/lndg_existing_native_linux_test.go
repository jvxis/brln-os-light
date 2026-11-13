package privileged

import (
	"os"
	"testing"
)

func TestLNDgExistingNativeDeclaration(t *testing.T) {
	if os.Getenv("LOS_LNDG_EXISTING") != "1" {
		t.Skip("explicit disposable opt-in required")
	}
	mac, _ := os.ReadFile("/sys/class/net/enp0s3/address")
	if string(mac) != "08:00:27:01:34:ae\n" || os.Geteuid() != 0 {
		t.Fatal("wrong disposable")
	}
	manager := &ComposeAppManager{Runner: &ExecCommandRunner{}}
	if _, err := manager.validatedLNDgFiles(); err != nil {
		t.Fatal(err)
	}
	t.Log("native declaration accepted without mutations")
}
