package server

import (
	"testing"
	"time"
)

func TestReportsReconciliationResponseIncludesGapsAndProgress(t *testing.T) {
	server := &Server{reportsReconcileJob: reportsReconciliationJob{
		Running:     true,
		Total:       2,
		Completed:   1,
		CurrentDate: "2026-08-14",
		StartedAt:   time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
	}}
	missing := []time.Time{
		time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	}
	response := server.reportsReconciliationResponse(missing)
	if response.MissingCount != 2 || len(response.MissingDates) != 2 || !response.Running {
		t.Fatalf("unexpected reconciliation response: %+v", response)
	}
	if response.Completed != 1 || response.Total != 2 || response.CurrentDate != "2026-08-14" || response.StartedAt == nil {
		t.Fatalf("missing progress data: %+v", response)
	}
}

func TestReportsReconciliationResponseSuppressesResolvedError(t *testing.T) {
	server := &Server{reportsReconcileJob: reportsReconciliationJob{LastError: "internal detail"}}
	response := server.reportsReconciliationResponse(nil)
	if response.MissingCount != 0 || response.LastError != "" {
		t.Fatalf("resolved reconciliation leaked stale error: %+v", response)
	}
}
