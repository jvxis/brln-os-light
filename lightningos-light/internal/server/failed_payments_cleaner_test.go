package server

import (
	"context"
	"testing"
	"time"
)

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

type failedPaymentsCleanerLNDStub struct {
	countResult    int
	countErr       error
	deleteErr      error
	countCalls     int
	deleteCalls    int
	countDeadline  time.Time
	deleteDeadline time.Time
}

func (s *failedPaymentsCleanerLNDStub) CountFailedPayments(ctx context.Context) (int, error) {
	s.countCalls++
	if deadline, ok := ctx.Deadline(); ok {
		s.countDeadline = deadline
	}
	return s.countResult, s.countErr
}

func (s *failedPaymentsCleanerLNDStub) DeleteFailedPayments(ctx context.Context) error {
	s.deleteCalls++
	if deadline, ok := ctx.Deadline(); ok {
		s.deleteDeadline = deadline
	}
	return s.deleteErr
}

func TestFailedPaymentsCleanerTickUsesSeparateTimeouts(t *testing.T) {
	stub := &failedPaymentsCleanerLNDStub{countResult: 7}
	cleaner := NewFailedPaymentsCleaner(nil, stub, nil)

	startedAt := time.Now()
	cleaner.tick(true)

	if stub.countCalls != 1 {
		t.Fatalf("expected CountFailedPayments to be called once, got %d", stub.countCalls)
	}
	if stub.deleteCalls != 1 {
		t.Fatalf("expected DeleteFailedPayments to be called once, got %d", stub.deleteCalls)
	}
	if cleaner.lastDeletedCount != 7 {
		t.Fatalf("expected 7 deleted payments, got %d", cleaner.lastDeletedCount)
	}
	if cleaner.lastError != "" {
		t.Fatalf("expected no last error, got %q", cleaner.lastError)
	}

	countBudget := stub.countDeadline.Sub(startedAt)
	if countBudget < failedPaymentsCleanerCountTimeout-2*time.Second || countBudget > failedPaymentsCleanerCountTimeout+2*time.Second {
		t.Fatalf("expected count timeout near %s, got %s", failedPaymentsCleanerCountTimeout, countBudget)
	}

	deleteBudget := stub.deleteDeadline.Sub(startedAt)
	if deleteBudget < failedPaymentsCleanerDeleteTimeout-2*time.Second || deleteBudget > failedPaymentsCleanerDeleteTimeout+2*time.Second {
		t.Fatalf("expected delete timeout near %s, got %s", failedPaymentsCleanerDeleteTimeout, deleteBudget)
	}
}

func TestFailedPaymentsCleanerTickRecordsFriendlyTimeout(t *testing.T) {
	stub := &failedPaymentsCleanerLNDStub{
		countResult: 1,
		deleteErr:   context.DeadlineExceeded,
	}
	cleaner := NewFailedPaymentsCleaner(nil, stub, nil)

	cleaner.tick(true)

	want := "failed payments cleanup timed out after 15m while deleting failed payments"
	if cleaner.lastError != want {
		t.Fatalf("expected %q, got %q", want, cleaner.lastError)
	}
	if stub.deleteCalls != 1 {
		t.Fatalf("expected DeleteFailedPayments to be called once, got %d", stub.deleteCalls)
	}
}
