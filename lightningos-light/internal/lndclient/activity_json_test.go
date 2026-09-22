package lndclient

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRecentActivityJSONOptionalTimestamps(t *testing.T) {
	occurred := time.Date(2026, 9, 20, 16, 7, 0, 0, time.UTC)
	created := occurred.Add(-time.Minute)
	for _, tc := range []struct {
		name             string
		created, settled time.Time
	}{
		{name: "payment without lifecycle timestamps"},
		{name: "received invoice with settlement only", settled: occurred},
		{name: "invoice with both timestamps", created: created, settled: occurred},
		{name: "creation without settlement", created: created},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := RecentActivity{Type: "payment", Status: "SUCCEEDED", AmountSat: 3_000_000,
				Timestamp: occurred, CreatedAt: tc.created, SettledAt: tc.settled}
			payload, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]time.Time{"timestamp": occurred, "created_at": tc.created, "settled_at": tc.settled} {
				raw, present := fields[key]
				if want.IsZero() {
					if present {
						t.Fatalf("unknown %s must be omitted, got %s", key, raw)
					}
					continue
				}
				var got time.Time
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("%s: %v", key, err)
				}
				if !got.Equal(want) {
					t.Fatalf("%s = %v, want %v", key, got, want)
				}
			}
			var roundTrip RecentActivity
			if err := json.Unmarshal(payload, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, item) {
				t.Fatal("activity changed during JSON round trip")
			}
		})
	}
}
