package server

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestKernelTCPStatusDoesNotTreatOldOrInvalidProofAsProtection(t *testing.T) {
	now := time.Unix(1800000000, 0)
	for _, item := range []struct {
		name, raw string
		valid     bool
	}{
		{"fresh", fmt.Sprintf(`{"version":1,"checked_at":%d,"filter_verified":true,"repair_count":1}`, now.Unix()), true},
		{"stale", fmt.Sprintf(`{"version":1,"checked_at":%d,"filter_verified":true}`, now.Unix()-91), false},
		{"future", fmt.Sprintf(`{"version":1,"checked_at":%d,"filter_verified":true}`, now.Unix()+6), false},
		{"trailing", fmt.Sprintf(`{"version":1,"checked_at":%d} {}`, now.Unix()), false},
		{"unknown version", fmt.Sprintf(`{"version":2,"checked_at":%d}`, now.Unix()), false},
		{"malformed", `{"filter_verified":true`, false},
		{"timestamp overflow", `{"version":1,"checked_at":-9223372036854775808,"filter_verified":true}`, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			status, valid := parseKernelTCPStatus(strings.NewReader(item.raw), now)
			if valid != item.valid {
				t.Fatalf("valid=%v want=%v", valid, item.valid)
			}
			if !valid && kernelTCPSystemCheckItem(status, valid).Status == systemCheckOK {
				t.Fatal("invalid proof shown as protected")
			}
		})
	}
	if kernelTCPSystemCheckItem(kernelTCPStatus{Disabled: true, FilterVerified: true}, true).Status == systemCheckOK {
		t.Fatal("operator-disabled mitigation shown as protected")
	}
	if kernelTCPSystemCheckItem(kernelTCPStatus{FilterVerified: true}, true).Status != systemCheckOK {
		t.Fatal("verified filter not shown")
	}
	if kernelTCPSystemCheckItem(kernelTCPStatus{FilterVerified: true}, true).Value != true {
		t.Fatal("system check value must preserve the existing scalar frontend contract")
	}
}
