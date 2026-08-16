package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/reports"
)

type reportsReconciliationJob struct {
	Running     bool
	Total       int
	Completed   int
	CurrentDate string
	LastError   string
	StartedAt   time.Time
	FinishedAt  time.Time
}

type reportsReconciliationResponse struct {
	MissingDates []string   `json:"missing_dates"`
	MissingCount int        `json:"missing_count"`
	Running      bool       `json:"running"`
	Total        int        `json:"total"`
	Completed    int        `json:"completed"`
	CurrentDate  string     `json:"current_date,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func (s *Server) handleReportsReconciliationGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		writeReportsUnavailable(w, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	missing, err := s.missingReportDates(ctx, svc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect report reconciliation")
		return
	}
	writeJSON(w, http.StatusOK, s.reportsReconciliationResponse(missing))
}

func (s *Server) handleReportsReconciliationPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		writeReportsUnavailable(w, errMsg)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	missing, err := s.missingReportDates(ctx, svc)
	cancel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect report reconciliation")
		return
	}

	s.reportsReconcileMu.Lock()
	if s.reportsReconcileJob.Running {
		s.reportsReconcileMu.Unlock()
		writeError(w, http.StatusConflict, "report reconciliation is already running")
		return
	}
	if len(missing) == 0 {
		s.reportsReconcileJob = reportsReconciliationJob{}
		s.reportsReconcileMu.Unlock()
		writeJSON(w, http.StatusOK, s.reportsReconciliationResponse(missing))
		return
	}
	s.reportsReconcileJob = reportsReconciliationJob{
		Running:   true,
		Total:     len(missing),
		StartedAt: time.Now(),
	}
	s.reportsReconcileMu.Unlock()

	dates := append([]time.Time(nil), missing...)
	go s.runReportsReconciliation(svc, dates)
	writeJSON(w, http.StatusAccepted, s.reportsReconciliationResponse(missing))
}

func (s *Server) runReportsReconciliation(svc *reports.Service, dates []time.Time) {
	ctx, cancel := context.WithTimeout(s.shutdownContext(), reportsReconciliationTimeout())
	defer cancel()
	err := svc.ReconcileDates(ctx, dates, s.reportsLocation(), func(completed, total int, reportDate time.Time) {
		s.reportsReconcileMu.Lock()
		s.reportsReconcileJob.Completed = completed
		s.reportsReconcileJob.Total = total
		s.reportsReconcileJob.CurrentDate = reportDate.Format("2006-01-02")
		s.reportsReconcileMu.Unlock()
	})

	s.reportsReconcileMu.Lock()
	s.reportsReconcileJob.Running = false
	s.reportsReconcileJob.FinishedAt = time.Now()
	if err != nil {
		s.reportsReconcileJob.LastError = "report reconciliation failed; check manager logs"
	} else {
		s.reportsReconcileJob.LastError = ""
	}
	s.reportsReconcileMu.Unlock()
	if err != nil && s.logger != nil {
		s.logger.Printf("reports reconciliation failed: %v", err)
	}
}

func reportsReconciliationTimeout() time.Duration {
	timeout := reportsLiveBuildTimeout()
	if timeout < 10*time.Minute {
		return 10 * time.Minute
	}
	return timeout
}

func (s *Server) missingReportDates(ctx context.Context, svc *reports.Service) ([]time.Time, error) {
	loc := s.reportsLocation()
	yesterday := time.Now().In(loc).AddDate(0, 0, -1)
	return svc.MissingDailyDates(ctx, yesterday)
}

func (s *Server) reportsReconciliationResponse(missing []time.Time) reportsReconciliationResponse {
	s.reportsReconcileMu.Lock()
	job := s.reportsReconcileJob
	s.reportsReconcileMu.Unlock()
	dates := make([]string, 0, len(missing))
	for _, value := range missing {
		dates = append(dates, value.Format("2006-01-02"))
	}
	response := reportsReconciliationResponse{
		MissingDates: dates,
		MissingCount: len(dates),
		Running:      job.Running,
		Total:        job.Total,
		Completed:    job.Completed,
		CurrentDate:  job.CurrentDate,
	}
	if len(dates) > 0 {
		response.LastError = job.LastError
	}
	if !job.StartedAt.IsZero() {
		startedAt := job.StartedAt
		response.StartedAt = &startedAt
	}
	if !job.FinishedAt.IsZero() {
		finishedAt := job.FinishedAt
		response.FinishedAt = &finishedAt
	}
	return response
}

func writeReportsUnavailable(w http.ResponseWriter, errMsg string) {
	message := strings.TrimSpace(errMsg)
	if message == "" {
		message = "reports unavailable"
	}
	writeError(w, http.StatusServiceUnavailable, message)
}
