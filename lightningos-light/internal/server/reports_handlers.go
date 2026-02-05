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
    Range: key,
    Timezone: reportsTimezoneLabel(loc),
    Series: mapSeries(items),
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

  fromStr := strings.TrimSpace(r.URL.Query().Get("from"))
  toStr := strings.TrimSpace(r.URL.Query().Get("to"))
  if fromStr == "" || toStr == "" {
    writeError(w, http.StatusBadRequest, "from and to are required")
    return
  }

  loc := s.reportsLocation()
  startDate, err := reports.ParseDate(fromStr, loc)
  if err != nil {
    writeError(w, http.StatusBadRequest, "from must be YYYY-MM-DD")
    return
  }
  endDate, err := reports.ParseDate(toStr, loc)
  if err != nil {
    writeError(w, http.StatusBadRequest, "to must be YYYY-MM-DD")
    return
  }
  if err := reports.ValidateCustomRange(startDate, endDate); err != nil {
    if strings.Contains(err.Error(), "large") {
      writeError(w, http.StatusBadRequest, fmt.Sprintf("range too large (max %d days)", reports.CustomRangeDaysLimit()))
    } else {
      writeError(w, http.StatusBadRequest, "invalid range")
    }
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
    Range: "custom",
    Timezone: reportsTimezoneLabel(loc),
    Series: mapSeries(items),
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
    Range: key,
    Timezone: reportsTimezoneLabel(loc),
    Days: summary.Days,
    Totals: metricsPayload(summary.Totals),
    Averages: metricsPayload(summary.Averages),
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
  Range string `json:"range"`
  Timezone string `json:"timezone"`
  Series []reportSeriesItem `json:"series"`
}

type reportSeriesItem struct {
  Date string `json:"date"`
  ForwardFeeRevenueSat float64 `json:"forward_fee_revenue_sats"`
  RebalanceFeeCostSat float64 `json:"rebalance_fee_cost_sats"`
  NetRoutingProfitSat float64 `json:"net_routing_profit_sats"`
  ForwardCount int64 `json:"forward_count"`
  RebalanceCount int64 `json:"rebalance_count"`
  RoutedVolumeSat float64 `json:"routed_volume_sats"`
  OnchainBalanceSat *int64 `json:"onchain_balance_sats"`
  LightningBalanceSat *int64 `json:"lightning_balance_sats"`
  TotalBalanceSat *int64 `json:"total_balance_sats"`
}

type reportSummaryResponse struct {
  Range string `json:"range"`
  Timezone string `json:"timezone"`
  Days int64 `json:"days"`
  Totals reportMetricsPayload `json:"totals"`
  Averages reportMetricsPayload `json:"averages"`
}

type reportMetricsPayload struct {
  Start string `json:"start,omitempty"`
  End string `json:"end,omitempty"`
  Timezone string `json:"timezone,omitempty"`
  ForwardFeeRevenueSat float64 `json:"forward_fee_revenue_sats"`
  RebalanceFeeCostSat float64 `json:"rebalance_fee_cost_sats"`
  NetRoutingProfitSat float64 `json:"net_routing_profit_sats"`
  ForwardCount int64 `json:"forward_count"`
  RebalanceCount int64 `json:"rebalance_count"`
  RoutedVolumeSat float64 `json:"routed_volume_sats"`
  OnchainBalanceSat *int64 `json:"onchain_balance_sats,omitempty"`
  LightningBalanceSat *int64 `json:"lightning_balance_sats,omitempty"`
  TotalBalanceSat *int64 `json:"total_balance_sats,omitempty"`
}

func mapSeries(items []reports.Row) []reportSeriesItem {
  if len(items) == 0 {
    return []reportSeriesItem{}
  }
  series := make([]reportSeriesItem, 0, len(items))
  for _, item := range items {
    series = append(series, reportSeriesItem{
      Date: item.ReportDate.Format("2006-01-02"),
      ForwardFeeRevenueSat: metricSats(item.Metrics.ForwardFeeRevenueMsat, item.Metrics.ForwardFeeRevenueSat),
      RebalanceFeeCostSat: metricSats(item.Metrics.RebalanceFeeCostMsat, item.Metrics.RebalanceFeeCostSat),
      NetRoutingProfitSat: metricSats(item.Metrics.NetRoutingProfitMsat, item.Metrics.NetRoutingProfitSat),
      ForwardCount: item.Metrics.ForwardCount,
      RebalanceCount: item.Metrics.RebalanceCount,
      RoutedVolumeSat: metricSats(item.Metrics.RoutedVolumeMsat, item.Metrics.RoutedVolumeSat),
      OnchainBalanceSat: item.Metrics.OnchainBalanceSat,
      LightningBalanceSat: item.Metrics.LightningBalanceSat,
      TotalBalanceSat: item.Metrics.TotalBalanceSat,
    })
  }
  return series
}

func metricsPayload(metrics reports.Metrics) reportMetricsPayload {
  return reportMetricsPayload{
    ForwardFeeRevenueSat: metricSats(metrics.ForwardFeeRevenueMsat, metrics.ForwardFeeRevenueSat),
    RebalanceFeeCostSat: metricSats(metrics.RebalanceFeeCostMsat, metrics.RebalanceFeeCostSat),
    NetRoutingProfitSat: metricSats(metrics.NetRoutingProfitMsat, metrics.NetRoutingProfitSat),
    ForwardCount: metrics.ForwardCount,
    RebalanceCount: metrics.RebalanceCount,
    RoutedVolumeSat: metricSats(metrics.RoutedVolumeMsat, metrics.RoutedVolumeSat),
    OnchainBalanceSat: metrics.OnchainBalanceSat,
    LightningBalanceSat: metrics.LightningBalanceSat,
    TotalBalanceSat: metrics.TotalBalanceSat,
  }
}

func metricSats(msat int64, sat int64) float64 {
  if msat != 0 {
    return float64(msat) / 1000
  }
  return float64(sat)
}
