package server

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The password confirmation belongs on the actions that move funds, and only on
// those. Route preview was gated too (issue #40): the dialog never opened, the
// preview failed, and with no routes the Pay buttons stayed disabled - so the
// gate blocked the whole screen without protecting anything.
//
// Previewing probes with SendToRouteV2 under a random payment hash. No preimage
// exists for it, every probe fails, and a failed HTLC settles nothing.
func TestOnlyPayingHandlersRequireLightningFundsReauth(t *testing.T) {
	mustGate := map[string]string{
		"handleWalletPay":               "sends the payment",
		"handleWalletPayValidatedRoute": "sends the payment over a chosen route",
		"handleWalletPayMPP":            "sends the payment as an MPP plan",
	}
	mustNotGate := map[string]string{
		"handleWalletPayPreview": "probes only; a random payment hash can never settle",
		"handleWalletDecode":     "reads the invoice, touches nothing",
	}

	raw, err := os.ReadFile("handlers.go")
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	funcDecl := regexp.MustCompile(`^func \(s \*Server\) ([A-Za-z0-9_]+)\(`)
	gated := make(map[string]bool)
	enclosing := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if match := funcDecl.FindStringSubmatch(line); match != nil {
			enclosing = match[1]
		}
		if strings.Contains(line, "requireLightningFundsReauth(") &&
			!strings.HasPrefix(strings.TrimSpace(line), "func ") {
			gated[enclosing] = true
		}
	}

	for name, why := range mustGate {
		if !gated[name] {
			t.Errorf("%s must ask for the password: it %s", name, why)
		}
	}
	for name, why := range mustNotGate {
		if gated[name] {
			t.Errorf("%s must not ask for the password: it %s.\n"+
				"Gating it breaks the screen - the dialog is only wired on the paying "+
				"handlers, so the request just fails and no route is ever shown.", name, why)
		}
	}
}
