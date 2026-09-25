package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type autofeeExposureQuery struct {
	Point string
	Since time.Time
	Until time.Time
	Limit int
}

func parseAutofeeExposureQuery(q url.Values, now time.Time) (autofeeExposureQuery, error) {
	v := autofeeExposureQuery{Point: strings.TrimSpace(q.Get("channel_point")), Since: now.Add(-24 * time.Hour), Until: now, Limit: 100}
	if v.Point == "" || len(v.Point) > 100 {
		return v, fmt.Errorf("channel_point required (maximum 100 characters)")
	}
	for key, target := range map[string]*time.Time{"since": &v.Since, "until": &v.Until} {
		if raw := q.Get(key); raw != "" {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				return v, fmt.Errorf("%s must be RFC3339", key)
			}
			*target = t.UTC()
		}
	}
	if q.Get("since") == "" {
		v.Since = v.Until.Add(-24 * time.Hour)
	}
	if !v.Since.Before(v.Until) || v.Until.Sub(v.Since) > 24*time.Hour || v.Until.After(now) {
		return v, fmt.Errorf("require since < until <= now and a window of at most 24 hours")
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return v, fmt.Errorf("limit must be between 1 and 200")
		}
		v.Limit = n
	}
	return v, nil
}

func (s *Server) handleAutofeeExposuresGet(w http.ResponseWriter, r *http.Request) {
	query, err := parseAutofeeExposureQuery(r.URL.Query(), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !autofeeExposureEnabled() {
		writeError(w, http.StatusServiceUnavailable, "policy observation disabled")
		return
	}
	svc, serviceErr := s.autofeeService()
	if svc == nil || serviceErr != "" {
		writeError(w, http.StatusServiceUnavailable, "policy observation unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	items, err := readAutofeePolicyExposures(ctx, svc.db, query.Point, query.Since, query.Until, query.Limit)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("autofee: exposure query failed: %v", err)
		}
		writeError(w, http.StatusServiceUnavailable, "policy observations unavailable; retry or request a shorter window")
		return
	}
	var nextUntil *time.Time
	if len(items) == query.Limit {
		t := items[len(items)-1].Start
		nextUntil = &t
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel_point": query.Point, "since": query.Since, "until": query.Until,
		"measured_at": time.Now().UTC(), "items": items, "next_until": nextUntil,
		"sample_interval_seconds": int(autofeeExposurePeriod.Seconds()),
		"limitations":             []string{"sampled_local_policy_not_causal_attribution", "propagation_and_between_sample_changes_unobserved", "notifications_may_arrive_late", "no_historical_backfill", "no_extrapolation_past_last_observation"},
	})
}
