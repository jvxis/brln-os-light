package server

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// handleAutofeePriceExperimentsGet lists price experiments, active first.
// Shadow rows carry the fee the engine would have tested and what the
// channel did at its unchanged fee, so the operator can audit the trigger
// before switching to enforce.
func (s *Server) handleAutofeePriceExperimentsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.autofeeService()
	if svc == nil {
		if errMsg == "" {
			errMsg = "autofee unavailable"
		}
		writeError(w, http.StatusServiceUnavailable, errMsg)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = n
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	items, err := svc.ListPriceExperiments(ctx, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	type item struct {
		autofeePriceExperiment
		StockedFrac          float64 `json:"stocked_frac"`
		SaleSatPerStockedDay float64 `json:"sale_sat_per_stocked_day"`
	}
	out := make([]item, 0, len(items))
	for _, x := range items {
		out = append(out, item{autofeePriceExperiment: x, StockedFrac: x.StockedFrac(), SaleSatPerStockedDay: x.SaleSatPerStockedDay(now)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "measured_at": now})
}
