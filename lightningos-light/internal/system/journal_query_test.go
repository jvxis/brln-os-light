package system

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func journalFixture(t *testing.T, timestamp string, message any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"__REALTIME_TIMESTAMP": timestamp, "MESSAGE": message, "PRIORITY": "6"})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func TestJournalQueryDecodeAndBudgets(t *testing.T) {
	input := journalFixture(t, "2000000", "newest\ncontinued") + journalFixture(t, "1000000", []int{111, 108, 100})
	got := decodeJournalQuery(strings.NewReader(input))
	if got.Partial || len(got.Entries) != 2 || got.Entries[0].Message != "old" || got.Entries[1].Message != "newest\ncontinued" || !got.Entries[1].Time.Equal(time.Unix(2, 0)) {
		t.Fatalf("decode: %+v", got)
	}
	got = decodeJournalQuery(strings.NewReader(input + "bad json\n"))
	if !got.Partial || len(got.Entries) != 2 {
		t.Fatalf("malformed record lost valid records: %+v", got)
	}
	got = decodeJournalQuery(strings.NewReader(strings.Repeat(journalFixture(t, "1000000", "ok"), JournalQueryScanLimit+1)))
	if !got.Partial || got.Reason != "scan_limit" || len(got.Entries) != JournalQueryScanLimit {
		t.Fatalf("scan budget: %s %d", got.Reason, len(got.Entries))
	}
	got = decodeJournalQuery(strings.NewReader(journalFixture(t, "1000000", strings.Repeat("x", 9000))))
	if !got.Partial || !strings.HasSuffix(got.Entries[0].Message, " [truncated]") {
		t.Fatal("long record not disclosed")
	}
	got = decodeJournalQuery(strings.NewReader(strings.Repeat(journalFixture(t, "1000000", strings.Repeat("x", 4000)), 5000)))
	if !got.Partial || got.Reason != "byte_limit" {
		t.Fatalf("byte budget: %+v", got.Reason)
	}
	got = decodeJournalQuery(strings.NewReader(journalFixture(t, "1000000", strings.Repeat("x", 2*1024*1024))))
	if !got.Partial || got.Reason != "record_limit" {
		t.Fatal("scanner overflow not disclosed")
	}
}

func TestJournalQueryArgsPreserveExactUnitAndUTCInterval(t *testing.T) {
	since, _ := time.Parse(time.RFC3339Nano, "2026-10-08T10:00:00.123456-03:00")
	args := strings.Join(journalQueryArgs("lnd", since, since.Add(time.Hour)), "|")
	for _, want := range []string{"-u|lnd|", "--reverse", "--output=json", fmt.Sprintf("-n|%d", JournalQueryScanLimit+1), "--since|2026-10-08 13:00:00.123456 UTC", "--until|2026-10-08 14:00:00.123456 UTC"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %q", want, args)
		}
	}
}
