package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/reports"
)

const reportsTimezoneEnv = "REPORTS_TIMEZONE"

func (s *Server) handleReportsRange(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	key := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if key == "" {
		key = reports.RangeD1
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	loc := s.reportsLocation()
	items, _, err := svc.Range(ctx, key, time.Now(), loc)
	if err != nil {
		if strings.Contains(err.Error(), "invalid range") {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load reports")
		}
		return
	}

	writeJSON(w, http.StatusOK, reportSeriesResponse{
		Range:    key,
		Timezone: reportsTimezoneLabel(loc),
		Series:   mapSeries(items),
	})
}

func (s *Server) handleReportsCustom(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	loc := s.reportsLocation()
	startDate, endDate, ok := parseCustomRange(w, r, loc)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	items, err := svc.CustomRange(ctx, startDate, endDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load reports")
		return
	}

	writeJSON(w, http.StatusOK, reportSeriesResponse{
		Range:    "custom",
		Timezone: reportsTimezoneLabel(loc),
		Series:   mapSeries(items),
	})
}

func (s *Server) handleReportsSummaryCustom(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	loc := s.reportsLocation()
	startDate, endDate, ok := parseCustomRange(w, r, loc)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	summary, err := svc.CustomSummary(ctx, startDate, endDate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load report summary")
		return
	}

	writeJSON(w, http.StatusOK, reportSummaryResponse{
		Range:             "custom",
		Timezone:          reportsTimezoneLabel(loc),
		Days:              summary.Days,
		Totals:            metricsPayload(summary.Totals),
		Averages:          metricsPayload(summary.Averages),
		MovementTargetSat: summary.MovementTargetSat,
		MovementPct:       summary.MovementPct,
	})
}

func (s *Server) handleReportsSummary(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	key := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if key == "" {
		key = reports.RangeD1
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	loc := s.reportsLocation()
	summary, _, err := svc.Summary(ctx, key, time.Now(), loc)
	if err != nil {
		if strings.Contains(err.Error(), "invalid range") {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load report summary")
		}
		return
	}

	writeJSON(w, http.StatusOK, reportSummaryResponse{
		Range:             key,
		Timezone:          reportsTimezoneLabel(loc),
		Days:              summary.Days,
		Totals:            metricsPayload(summary.Totals),
		Averages:          metricsPayload(summary.Averages),
		MovementTargetSat: summary.MovementTargetSat,
		MovementPct:       summary.MovementPct,
	})
}

func (s *Server) handleReportsLive(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), reportsLiveTimeout())
	defer cancel()

	loc := s.reportsLocation()
	tr, metrics, err := svc.Live(ctx, time.Now(), loc, reportsLiveLookbackHours())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "live report unavailable")
		return
	}

	payload := metricsPayload(metrics)
	payload.Start = tr.StartLocal.Format(time.RFC3339)
	payload.End = tr.EndLocal.Format(time.RFC3339)
	payload.Timezone = reportsTimezoneLabel(loc)

	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleReportsMovementLive(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), reportsLiveTimeout())
	defer cancel()

	loc := s.reportsLocation()
	movement, err := svc.MovementLive(ctx, time.Now(), loc)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "daily movement unavailable")
		return
	}

	writeJSON(w, http.StatusOK, reportMovementLiveResponse{
		Date:              movement.Date.Format("2006-01-02"),
		Start:             movement.Start.Format(time.RFC3339),
		End:               movement.End.Format(time.RFC3339),
		Timezone:          reportsTimezoneLabel(loc),
		OutboundTargetSat: movement.TargetSat,
		RoutedVolumeSat:   movement.RoutedVolumeSat,
		MovementPct:       movement.MovementPct,
	})
}

func reportsLiveTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("REPORTS_LIVE_TIMEOUT_SEC"))
	if raw == "" {
		return 20 * time.Second
	}
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		return time.Duration(parsed) * time.Second
	}
	return 20 * time.Second
}

func reportsLiveLookbackHours() int {
	raw := strings.TrimSpace(os.Getenv("REPORTS_LIVE_LOOKBACK_HOURS"))
	if raw == "" {
		return 0
	}
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		return parsed
	}
	return 0
}

func (s *Server) reportsLocation() *time.Location {
	raw := strings.TrimSpace(os.Getenv(reportsTimezoneEnv))
	loc, err := reports.ResolveLocation(raw, time.Local)
	if err != nil && s != nil && s.logger != nil {
		s.logger.Printf("reports: invalid %s %q, using %s: %v", reportsTimezoneEnv, raw, loc.String(), err)
	}
	return loc
}

func reportsTimezoneLabel(loc *time.Location) string {
	if loc == nil {
		return "Local"
	}
	return loc.String()
}

type reportSeriesResponse struct {
	Range    string             `json:"range"`
	Timezone string             `json:"timezone"`
	Series   []reportSeriesItem `json:"series"`
}

type reportSeriesItem struct {
	Date                       string     `json:"date"`
	ForwardFeeRevenueSat       float64    `json:"forward_fee_revenue_sats"`
	RebalanceFeeCostSat        float64    `json:"rebalance_fee_cost_sats"`
	PaymentFeeCostSat          float64    `json:"payment_fee_cost_sats"`
	OnchainFeeCostSat          float64    `json:"onchain_fee_cost_sats"`
	OnchainCoopCloseCostSat    float64    `json:"onchain_coop_close_cost_sats"`
	OnchainLocalForceCostSat   float64    `json:"onchain_local_force_cost_sats"`
	OnchainRemoteForceCostSat  float64    `json:"onchain_remote_force_cost_sats"`
	OffchainFeeCostSat         float64    `json:"offchain_fee_cost_sats"`
	KeysendReceivedSat         float64    `json:"keysend_received_sats"`
	KeysendReceivedCount       int64      `json:"keysend_received_count"`
	TotalFeeCostSat            float64    `json:"total_fee_cost_sats"`
	TotalFeeCostWithOnchainSat float64    `json:"total_fee_cost_with_onchain_sats"`
	NetRoutingProfitSat        float64    `json:"net_routing_profit_sats"`
	NetWithKeysendSat          float64    `json:"net_with_keysend_sats"`
	ForwardCount               int64      `json:"forward_count"`
	RebalanceCount             int64      `json:"rebalance_count"`
	RebalanceVolumeSat         float64    `json:"rebalance_volume_sats"`
	PaymentCount               int64      `json:"payment_count"`
	RoutedVolumeSat            float64    `json:"routed_volume_sats"`
	OnchainBalanceSat          *int64     `json:"onchain_balance_sats"`
	LightningBalanceSat        *int64     `json:"lightning_balance_sats"`
	TotalBalanceSat            *int64     `json:"total_balance_sats"`
	ProvenanceLastSyncAt       *time.Time `json:"provenance_last_sync_at,omitempty"`
	ProvenanceLastSyncAgeHours *float64   `json:"provenance_last_sync_age_hours,omitempty"`
	ProvenanceHealthAlert      *bool      `json:"provenance_health_alert,omitempty"`
	ProvenanceLastError        *string    `json:"provenance_last_error,omitempty"`
}

type reportSummaryResponse struct {
	Range             string               `json:"range"`
	Timezone          string               `json:"timezone"`
	Days              int64                `json:"days"`
	Totals            reportMetricsPayload `json:"totals"`
	Averages          reportMetricsPayload `json:"averages"`
	MovementTargetSat int64                `json:"movement_target_sats"`
	MovementPct       float64              `json:"movement_pct"`
}

type reportMovementLiveResponse struct {
	Date              string  `json:"date"`
	Start             string  `json:"start"`
	End               string  `json:"end"`
	Timezone          string  `json:"timezone"`
	OutboundTargetSat int64   `json:"outbound_target_sats"`
	RoutedVolumeSat   float64 `json:"routed_volume_sats"`
	MovementPct       float64 `json:"movement_pct"`
}

type reportMetricsPayload struct {
	Start                      string  `json:"start,omitempty"`
	End                        string  `json:"end,omitempty"`
	Timezone                   string  `json:"timezone,omitempty"`
	ForwardFeeRevenueSat       float64 `json:"forward_fee_revenue_sats"`
	RebalanceFeeCostSat        float64 `json:"rebalance_fee_cost_sats"`
	PaymentFeeCostSat          float64 `json:"payment_fee_cost_sats"`
	OnchainFeeCostSat          float64 `json:"onchain_fee_cost_sats"`
	OnchainCoopCloseCostSat    float64 `json:"onchain_coop_close_cost_sats"`
	OnchainLocalForceCostSat   float64 `json:"onchain_local_force_cost_sats"`
	OnchainRemoteForceCostSat  float64 `json:"onchain_remote_force_cost_sats"`
	OffchainFeeCostSat         float64 `json:"offchain_fee_cost_sats"`
	KeysendReceivedSat         float64 `json:"keysend_received_sats"`
	KeysendReceivedCount       int64   `json:"keysend_received_count"`
	TotalFeeCostSat            float64 `json:"total_fee_cost_sats"`
	TotalFeeCostWithOnchainSat float64 `json:"total_fee_cost_with_onchain_sats"`
	NetRoutingProfitSat        float64 `json:"net_routing_profit_sats"`
	NetWithKeysendSat          float64 `json:"net_with_keysend_sats"`
	ForwardCount               int64   `json:"forward_count"`
	RebalanceCount             int64   `json:"rebalance_count"`
	RebalanceVolumeSat         float64 `json:"rebalance_volume_sats"`
	PaymentCount               int64   `json:"payment_count"`
	RoutedVolumeSat            float64 `json:"routed_volume_sats"`
	OnchainBalanceSat          *int64  `json:"onchain_balance_sats,omitempty"`
	LightningBalanceSat        *int64  `json:"lightning_balance_sats,omitempty"`
	TotalBalanceSat            *int64  `json:"total_balance_sats,omitempty"`
}

func mapSeries(items []reports.Row) []reportSeriesItem {
	if len(items) == 0 {
		return []reportSeriesItem{}
	}
	series := make([]reportSeriesItem, 0, len(items))
	for _, item := range items {
		series = append(series, reportSeriesItem{
			Date:                       item.ReportDate.Format("2006-01-02"),
			ForwardFeeRevenueSat:       metricSats(item.Metrics.ForwardFeeRevenueMsat, item.Metrics.ForwardFeeRevenueSat),
			RebalanceFeeCostSat:        metricSats(item.Metrics.RebalanceFeeCostMsat, item.Metrics.RebalanceFeeCostSat),
			PaymentFeeCostSat:          metricSats(item.Metrics.PaymentFeeCostMsat, item.Metrics.PaymentFeeCostSat),
			OnchainFeeCostSat:          metricSats(item.Metrics.OnchainFeeCostMsat, item.Metrics.OnchainFeeCostSat),
			OnchainCoopCloseCostSat:    metricSats(item.Metrics.OnchainCoopCloseCostMsat, item.Metrics.OnchainCoopCloseCostSat),
			OnchainLocalForceCostSat:   metricSats(item.Metrics.OnchainLocalForceCostMsat, item.Metrics.OnchainLocalForceCostSat),
			OnchainRemoteForceCostSat:  metricSats(item.Metrics.OnchainRemoteForceCostMsat, item.Metrics.OnchainRemoteForceCostSat),
			OffchainFeeCostSat:         offchainFeeCostSats(item.Metrics),
			KeysendReceivedSat:         metricSats(item.Metrics.KeysendReceivedMsat, item.Metrics.KeysendReceivedSat),
			KeysendReceivedCount:       item.Metrics.KeysendReceivedCount,
			TotalFeeCostSat:            totalFeeCostSats(item.Metrics),
			TotalFeeCostWithOnchainSat: totalFeeCostWithOnchainSats(item.Metrics),
			NetRoutingProfitSat:        metricSats(item.Metrics.NetRoutingProfitMsat, item.Metrics.NetRoutingProfitSat),
			NetWithKeysendSat:          metricSats(item.Metrics.NetWithKeysendMsat, item.Metrics.NetWithKeysendSat),
			ForwardCount:               item.Metrics.ForwardCount,
			RebalanceCount:             item.Metrics.RebalanceCount,
			RebalanceVolumeSat:         metricSats(item.Metrics.RebalanceVolumeMsat, item.Metrics.RebalanceVolumeSat),
			PaymentCount:               item.Metrics.PaymentCount,
			RoutedVolumeSat:            metricSats(item.Metrics.RoutedVolumeMsat, item.Metrics.RoutedVolumeSat),
			OnchainBalanceSat:          item.Metrics.OnchainBalanceSat,
			LightningBalanceSat:        item.Metrics.LightningBalanceSat,
			TotalBalanceSat:            item.Metrics.TotalBalanceSat,
			ProvenanceLastSyncAt:       item.Metrics.ProvenanceLastSyncAt,
			ProvenanceLastSyncAgeHours: item.Metrics.ProvenanceLastSyncAgeHours,
			ProvenanceHealthAlert:      item.Metrics.ProvenanceHealthAlert,
			ProvenanceLastError:        item.Metrics.ProvenanceLastError,
		})
	}
	return series
}

func metricsPayload(metrics reports.Metrics) reportMetricsPayload {
	return reportMetricsPayload{
		ForwardFeeRevenueSat:       metricSats(metrics.ForwardFeeRevenueMsat, metrics.ForwardFeeRevenueSat),
		RebalanceFeeCostSat:        metricSats(metrics.RebalanceFeeCostMsat, metrics.RebalanceFeeCostSat),
		PaymentFeeCostSat:          metricSats(metrics.PaymentFeeCostMsat, metrics.PaymentFeeCostSat),
		OnchainFeeCostSat:          metricSats(metrics.OnchainFeeCostMsat, metrics.OnchainFeeCostSat),
		OnchainCoopCloseCostSat:    metricSats(metrics.OnchainCoopCloseCostMsat, metrics.OnchainCoopCloseCostSat),
		OnchainLocalForceCostSat:   metricSats(metrics.OnchainLocalForceCostMsat, metrics.OnchainLocalForceCostSat),
		OnchainRemoteForceCostSat:  metricSats(metrics.OnchainRemoteForceCostMsat, metrics.OnchainRemoteForceCostSat),
		OffchainFeeCostSat:         offchainFeeCostSats(metrics),
		KeysendReceivedSat:         metricSats(metrics.KeysendReceivedMsat, metrics.KeysendReceivedSat),
		KeysendReceivedCount:       metrics.KeysendReceivedCount,
		TotalFeeCostSat:            totalFeeCostSats(metrics),
		TotalFeeCostWithOnchainSat: totalFeeCostWithOnchainSats(metrics),
		NetRoutingProfitSat:        metricSats(metrics.NetRoutingProfitMsat, metrics.NetRoutingProfitSat),
		NetWithKeysendSat:          metricSats(metrics.NetWithKeysendMsat, metrics.NetWithKeysendSat),
		ForwardCount:               metrics.ForwardCount,
		RebalanceCount:             metrics.RebalanceCount,
		RebalanceVolumeSat:         metricSats(metrics.RebalanceVolumeMsat, metrics.RebalanceVolumeSat),
		PaymentCount:               metrics.PaymentCount,
		RoutedVolumeSat:            metricSats(metrics.RoutedVolumeMsat, metrics.RoutedVolumeSat),
		OnchainBalanceSat:          metrics.OnchainBalanceSat,
		LightningBalanceSat:        metrics.LightningBalanceSat,
		TotalBalanceSat:            metrics.TotalBalanceSat,
	}
}

func totalFeeCostSats(metrics reports.Metrics) float64 {
	return offchainFeeCostSats(metrics)
}

func offchainFeeCostSats(metrics reports.Metrics) float64 {
	totalMsat := metrics.OffchainFeeCostMsat()
	if totalMsat != 0 {
		return float64(totalMsat) / 1000
	}
	return float64(metrics.OffchainFeeCostSat())
}

func totalFeeCostWithOnchainSats(metrics reports.Metrics) float64 {
	totalMsat := metrics.TotalFeeCostWithOnchainMsat()
	if totalMsat != 0 {
		return float64(totalMsat) / 1000
	}
	return float64(metrics.TotalFeeCostWithOnchainSat())
}

func metricSats(msat int64, sat int64) float64 {
	if msat != 0 {
		return float64(msat) / 1000
	}
	return float64(sat)
}

func parseCustomRange(w http.ResponseWriter, r *http.Request, loc *time.Location) (time.Time, time.Time, bool) {
	fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
	toStr := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromStr == "" || toStr == "" {
		writeError(w, http.StatusBadRequest, "from and to are required")
		return time.Time{}, time.Time{}, false
	}

	startDate, err := reports.ParseDate(fromStr, loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
		return time.Time{}, time.Time{}, false
	}
	endDate, err := reports.ParseDate(toStr, loc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "to must be YYYY-MM-DD")
		return time.Time{}, time.Time{}, false
	}
	if err := reports.ValidateCustomRange(startDate, endDate); err != nil {
		if strings.Contains(err.Error(), "large") {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("range too large (max %d days)", reports.CustomRangeDaysLimit()))
		} else {
			writeError(w, http.StatusBadRequest, "invalid range")
		}
		return time.Time{}, time.Time{}, false
	}
	return startDate, endDate, true
}
