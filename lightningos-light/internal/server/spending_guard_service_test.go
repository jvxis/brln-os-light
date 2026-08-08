package server

import (
	"errors"
	"math"
	"testing"
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
