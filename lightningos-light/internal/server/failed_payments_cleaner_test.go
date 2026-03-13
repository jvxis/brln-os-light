package server

import "testing"

func TestValidateFailedPaymentsCleanerConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     FailedPaymentsCleanerConfig
		wantErr bool
	}{
		{
			name: "minimum valid",
			cfg:  FailedPaymentsCleanerConfig{Enabled: true, IntervalHours: failedPaymentsCleanerMinIntervalHours},
		},
		{
			name: "maximum valid",
			cfg:  FailedPaymentsCleanerConfig{Enabled: true, IntervalHours: failedPaymentsCleanerMaxIntervalHours},
		},
		{
			name:    "below minimum",
			cfg:     FailedPaymentsCleanerConfig{Enabled: true, IntervalHours: failedPaymentsCleanerMinIntervalHours - 1},
			wantErr: true,
		},
		{
			name:    "above maximum",
			cfg:     FailedPaymentsCleanerConfig{Enabled: true, IntervalHours: failedPaymentsCleanerMaxIntervalHours + 1},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFailedPaymentsCleanerConfig(tc.cfg)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

func TestNormalizeFailedPaymentsCleanerConfig(t *testing.T) {
	cfg := normalizeFailedPaymentsCleanerConfig(FailedPaymentsCleanerConfig{
		Enabled:       true,
		IntervalHours: 0,
	})
	if cfg.IntervalHours != failedPaymentsCleanerMinIntervalHours {
		t.Fatalf("expected normalized interval %d, got %d", failedPaymentsCleanerMinIntervalHours, cfg.IntervalHours)
	}

	cfg = normalizeFailedPaymentsCleanerConfig(FailedPaymentsCleanerConfig{
		Enabled:       true,
		IntervalHours: failedPaymentsCleanerMaxIntervalHours + 10,
	})
	if cfg.IntervalHours != failedPaymentsCleanerMaxIntervalHours {
		t.Fatalf("expected normalized interval %d, got %d", failedPaymentsCleanerMaxIntervalHours, cfg.IntervalHours)
	}
}
