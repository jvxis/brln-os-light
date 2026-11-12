package server

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestValidateSpendingGuardSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings SpendingGuardSettings
		wantErr  bool
	}{
		{name: "disabled default", settings: SpendingGuardSettings{}},
		{name: "enabled per payment", settings: SpendingGuardSettings{Enabled: true, MaxPaymentSat: 100_000}},
		{name: "enabled rolling", settings: SpendingGuardSettings{Enabled: true, Rolling24hLimitSat: 500_000}},
		{name: "enabled without limit", settings: SpendingGuardSettings{Enabled: true}, wantErr: true},
		{name: "negative", settings: SpendingGuardSettings{MaxPaymentSat: -1}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validateSpendingGuardSettings(test.settings); (got != nil) != test.wantErr {
				t.Fatalf("validateSpendingGuardSettings() error = %v, wantErr %v", got, test.wantErr)
			}
		})
	}
}

func TestSafeSpendingDebit(t *testing.T) {
	got, err := safeSpendingDebit(100_000, 500)
	if err != nil || got != 100_500 {
		t.Fatalf("safeSpendingDebit() = %d, %v", got, err)
	}
	if _, err := safeSpendingDebit(math.MaxInt64, 1); err == nil {
		t.Fatal("expected overflow to be rejected")
	}
}

func TestSpendingGuardNeedsReauth(t *testing.T) {
	base := SpendingGuardSettings{Enabled: true, MaxPaymentSat: 100_000, Rolling24hLimitSat: 500_000}
	if !spendingGuardNeedsReauth(base, SpendingGuardSettings{MaxPaymentSat: 100_000, Rolling24hLimitSat: 500_000}) {
		t.Fatal("disabling an active guard must require reauthentication")
	}
	if !spendingGuardNeedsReauth(base, SpendingGuardSettings{Enabled: true, MaxPaymentSat: 200_000, Rolling24hLimitSat: 500_000}) {
		t.Fatal("raising a limit must require reauthentication")
	}
	if spendingGuardNeedsReauth(base, SpendingGuardSettings{Enabled: true, MaxPaymentSat: 50_000, Rolling24hLimitSat: 400_000}) {
		t.Fatal("tightening limits should not require reauthentication")
	}
	if spendingGuardNeedsReauth(SpendingGuardSettings{}, SpendingGuardSettings{Enabled: true, MaxPaymentSat: 50_000}) {
		t.Fatal("enabling a disabled guard should not require reauthentication")
	}
}

func TestSpendingGuardFiniteAndUnlimitedTransitions(t *testing.T) {
	for _, tt := range []struct {
		name          string
		current, next SpendingGuardSettings
		want          bool
	}{
		{"remove per payment", SpendingGuardSettings{true, 100, 1000, time.Time{}}, SpendingGuardSettings{true, 0, 1000, time.Time{}}, true},
		{"remove rolling", SpendingGuardSettings{true, 100, 1000, time.Time{}}, SpendingGuardSettings{true, 100, 0, time.Time{}}, true},
		{"introduce per payment", SpendingGuardSettings{true, 0, 1000, time.Time{}}, SpendingGuardSettings{true, 100, 1000, time.Time{}}, false},
		{"introduce rolling", SpendingGuardSettings{true, 100, 0, time.Time{}}, SpendingGuardSettings{true, 100, 1000, time.Time{}}, false},
		{"unchanged", SpendingGuardSettings{true, 100, 0, time.Time{}}, SpendingGuardSettings{true, 100, 0, time.Time{}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := spendingGuardNeedsReauth(tt.current, tt.next); got != tt.want {
				t.Fatalf("needs reauth = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSuggestedSpendingGuardFeeSatMatchesRouterDefaults(t *testing.T) {
	if got := suggestedSpendingGuardFeeSat(500, 0); got != 500 {
		t.Fatalf("small payment fee = %d", got)
	}
	if got := suggestedSpendingGuardFeeSat(100_001, 0); got != 5_001 {
		t.Fatalf("percentage fee = %d", got)
	}
	if got := suggestedSpendingGuardFeeSat(100_000, 321); got != 321 {
		t.Fatalf("explicit fee = %d", got)
	}
}

func TestSpendingLimitErrorWrapsSentinel(t *testing.T) {
	err := &SpendingLimitError{Reason: "rolling_24h", RequestedSat: 1_000, LimitSat: 10_000, RemainingSat: 500}
	if !errors.Is(err, ErrSpendingGuardLimit) {
		t.Fatal("expected limit error to wrap ErrSpendingGuardLimit")
	}
}

func TestSpendingGuardReconcileBackoffGrowsAndCaps(t *testing.T) {
	cases := map[int]time.Duration{
		-1: time.Minute,
		0:  time.Minute,
		1:  2 * time.Minute,
		2:  4 * time.Minute,
		5:  32 * time.Minute,
		6:  time.Hour, // 64 min capped
		10: time.Hour,
		40: time.Hour, // no shift overflow
	}
	for attempts, want := range cases {
		if got := spendingGuardReconcileBackoff(attempts); got != want {
			t.Fatalf("attempts=%d: backoff %s want %s", attempts, got, want)
		}
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	if spendingGuardReconcileDue(now.Add(-30*time.Second), 0, now) {
		t.Fatal("a reservation touched 30s ago must wait for the first minute")
	}
	if !spendingGuardReconcileDue(now.Add(-time.Minute), 0, now) {
		t.Fatal("a reservation touched 1 min ago is due for its first lookup")
	}
	if spendingGuardReconcileDue(now.Add(-3*time.Minute), 2, now) {
		t.Fatal("after two lookups the entry waits 4 minutes")
	}
	if !spendingGuardReconcileDue(now.Add(-time.Hour), 30, now) {
		t.Fatal("the backoff never exceeds one hour")
	}
	if !spendingGuardReconcileDue(time.Time{}, 3, now) {
		t.Fatal("an entry without updated_at is always due")
	}
}

func TestSpendingGuardReconcileDecision(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-time.Minute)
	old := now.Add(-spendingGuardUnknownPaymentGrace)
	cases := []struct {
		name     string
		found    bool
		err      error
		status   string
		created  time.Time
		want     spendingGuardReconcileAction
		wantText string
	}{
		{"lookup error keeps", false, errors.New("lnd unavailable"), "", old, spendingGuardReconcileKeep, ""},
		{"lookup error keeps even when found", true, errors.New("lnd unavailable"), "FAILED", old, spendingGuardReconcileKeep, ""},
		{"succeeded settles", true, nil, "SUCCEEDED", fresh, spendingGuardReconcileSettle, ""},
		{"failed releases", true, nil, "failed", fresh, spendingGuardReconcileRelease, "LND reported payment failed"},
		{"in flight keeps", true, nil, "IN_FLIGHT", old, spendingGuardReconcileKeep, ""},
		{"unknown and recent keeps", false, nil, "", fresh, spendingGuardReconcileKeep, ""},
		{"unknown and old releases", false, nil, "", old, spendingGuardReconcileRelease, "LND has no record of this payment hash; the payment was never initiated"},
		{"unknown without created_at keeps", false, nil, "", time.Time{}, spendingGuardReconcileKeep, ""},
	}
	for _, tc := range cases {
		got, reason := spendingGuardReconcileDecision(tc.found, tc.err, tc.status, tc.created, now)
		if got != tc.want || reason != tc.wantText {
			t.Fatalf("%s: got (%d, %q) want (%d, %q)", tc.name, got, reason, tc.want, tc.wantText)
		}
	}
}
