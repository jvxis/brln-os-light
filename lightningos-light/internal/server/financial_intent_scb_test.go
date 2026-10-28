package server

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFinancialIntentSCBVerificationOrder(t *testing.T) {
	points := []string{"channel-b:1", "channel-a:0"}
	binding, err := json.Marshal(financialIntentBinding{Kind: "scb_restore", Snapshot: financialSnapshot(scbFinancialSnapshot(points))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), financialIntentContextKey{}, &financialIntentExecution{record: &financialIntentRecord{Binding: binding}})
	if points[0] != "channel-b:1" {
		t.Fatal("snapshot mutated the native response")
	}
	if err := validateFinancialSnapshot(ctx, "scb_restore", scbFinancialSnapshot([]string{"channel-a:0", "channel-b:1"})); err != nil {
		t.Fatal("same verified channels in different native order rejected:", err)
	}
	for _, changed := range [][]string{{"channel-a:0"}, {"channel-a:0", "channel-c:1"}, {"channel-a:0", "channel-b:1", "channel-b:1"}} {
		if validateFinancialSnapshot(ctx, "scb_restore", scbFinancialSnapshot(changed)) == nil {
			t.Fatal("changed recovery channel set inherited approval")
		}
	}
}
