package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"lightningos-light/internal/system"
)

type insightSource struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	AgeSec int64  `json:"age_sec,omitempty"`
	Error  string `json:"error,omitempty"`
}

type insightsSummary struct {
	ValueSatDay    int64 `json:"value_sat_day"`
	Open           int   `json:"open"`
	OpenHigh       int   `json:"open_high"`
	Followed30d    int   `json:"followed_30d"`
	IdleCapitalSat int64 `json:"idle_capital_sat"`
	// Phase 2: measured outcomes, node-wide.
	Measured int `json:"measured"`
	Hits     int `json:"hits"`
	Pending  int `json:"pending"`
}

type insightsPayload struct {
	ComputedAt time.Time          `json:"computed_at"`
	Sources    []insightSource    `json:"sources"`
	Node       insightNodeInfo    `json:"node"`
	Summary    insightsSummary    `json:"summary"`
	Scorecard  []InsightRuleScore `json:"scorecard"`
	Items      []Insight          `json:"items"`
}

func (s *Server) handleInsightOutcomesGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.insightsService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, insightsUnavailableMessage(errMsg))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	card, err := svc.Scorecard(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load insight outcomes")
		return
	}
	writeJSON(w, http.StatusOK, card)
}

func (s *Server) handleInsightsGet(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.insightsService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, insightsUnavailableMessage(errMsg))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	payload, err := s.collectInsights(ctx, svc, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute insights")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleInsightsRecomputePost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.insightsService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, insightsUnavailableMessage(errMsg))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	payload, err := s.collectInsights(ctx, svc, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute insights")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

type insightStatusPayload struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (s *Server) handleInsightStatusPost(w http.ResponseWriter, r *http.Request) {
	svc, errMsg := s.insightsService()
	if svc == nil {
		writeError(w, http.StatusServiceUnavailable, insightsUnavailableMessage(errMsg))
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	var payload insightStatusPayload
	if err := readJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := svc.SetStatus(ctx, id, strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Reason), time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, errInsightNotFound):
			writeError(w, http.StatusNotFound, "insight not found")
		case strings.HasPrefix(err.Error(), "invalid"):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "failed to update insight")
		}
		return
	}
	s.insightsCacheMu.Lock()
	s.insightsCache = nil
	s.insightsCacheMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func insightsUnavailableMessage(errMsg string) string {
	if msg := strings.TrimSpace(errMsg); msg != "" {
		return msg
	}
	return "insights unavailable"
}

// collectInsights gathers every signal from the in-process services, runs
// the rule engine and merges the operator's stored decisions. Sources that
// fail are reported as such instead of blanking the whole page.
func (s *Server) collectInsights(ctx context.Context, svc *InsightsService, force bool) (insightsPayload, error) {
	now := time.Now().UTC()
	if !force {
		s.insightsCacheMu.Lock()
		cached := s.insightsCache
		s.insightsCacheMu.Unlock()
		if cached != nil && now.Sub(cached.ComputedAt) < insightsCacheTTL {
			return *cached, nil
		}
	}

	in := insightsInput{RefillIntents: map[uint64][]AutomationIntent{}, AutofeeProfile: autofeeProfiles[autofeeProfileDefault]}
	sources := make([]insightSource, 0, 6)

	rankingSvc, rankingErr := s.channelRankingService()
	if rankingSvc != nil {
		items, status, err := rankingSvc.List(ctx, 500, "")
		src := insightSource{ID: "ranking", OK: err == nil && status.Available}
		if err != nil {
			src.Error = err.Error()
		} else {
			in.Ranking = items
			if status.LastSyncAt != nil {
				src.AgeSec = int64(now.Sub(*status.LastSyncAt).Seconds())
			}
		}
		sources = append(sources, src)
	} else {
		sources = append(sources, insightSource{ID: "ranking", Error: rankingErr})
	}

	if s.rebalance != nil {
		channels, err := s.rebalance.Channels(ctx)
		src := insightSource{ID: "rebalance", OK: err == nil}
		if err != nil {
			src.Error = err.Error()
		} else {
			in.Rebalance = channels
			if jobs, _, jobErr := s.rebalance.History(ctx, 0); jobErr == nil {
				in.Jobs24h = jobs
			}
		}
		sources = append(sources, src)
	} else {
		sources = append(sources, insightSource{ID: "rebalance", Error: "rebalance unavailable"})
	}

	if s.automationIntents != nil {
		intents, err := s.automationIntents.ActiveForConsumer(ctx, automationIntentProducerRebalance, now)
		src := insightSource{ID: "interlock", OK: err == nil}
		if err != nil {
			src.Error = err.Error()
		} else {
			in.RefillIntents = intents
		}
		sources = append(sources, src)
	} else {
		sources = append(sources, insightSource{ID: "interlock", Error: "automation intents unavailable"})
	}

	if autofeeSvc, _ := s.autofeeService(); autofeeSvc != nil {
		cfg, err := autofeeSvc.GetConfig(ctx)
		src := insightSource{ID: "autofee", OK: err == nil}
		if err != nil {
			src.Error = err.Error()
		} else if profile, ok := autofeeProfiles[effectiveAutofeeProfileName(cfg.Profile)]; ok {
			in.AutofeeProfile = profile
		}
		sources = append(sources, src)
	} else {
		sources = append(sources, insightSource{ID: "autofee", Error: "autofee unavailable"})
	}

	if candSvc, candErr := s.channelOpenCandidatesService(); candSvc != nil {
		items, status, err := candSvc.List(ctx, 50)
		src := insightSource{ID: "open_candidates", OK: err == nil && status.Available}
		if err != nil {
			src.Error = err.Error()
		} else {
			in.OpenCandidates = items
			if status.LastSyncAt != nil {
				src.AgeSec = int64(now.Sub(*status.LastSyncAt).Seconds())
			}
		}
		sources = append(sources, src)
	} else {
		sources = append(sources, insightSource{ID: "open_candidates", Error: candErr})
	}

	in.Commitments, in.MagmaStateKnown = s.channelCapitalPlanCommitments(ctx)
	if handled, state, err := system.LoopStatusWithBroker(ctx); handled && err == nil {
		in.LoopKnown = true
		in.LoopInstalled = state.Installed
	}

	// Phase 2: measured outcomes reorder the rules before they run.
	card, cardErr := svc.Scorecard(ctx)
	if cardErr != nil {
		s.logger.Printf("insights: scorecard failed: %v", cardErr)
	} else {
		in.RuleWeights = insightRuleWeightsFromScorecard(card)
	}

	items := buildInsights(in, now)
	items, err := svc.Reconcile(ctx, items, now)
	if err != nil {
		s.logger.Printf("insights: reconcile failed: %v", err)
	}
	followed, _ := svc.FollowedSince(ctx, now.Add(-insightsFollowedWindow))
	if items == nil {
		items = []Insight{}
	}

	payload := insightsPayload{ComputedAt: now, Sources: sources, Node: insightNodeInfoFor(in.Ranking), Scorecard: card.Rules, Items: items}
	if payload.Scorecard == nil {
		payload.Scorecard = []InsightRuleScore{}
	}
	payload.Summary.Followed30d = followed
	payload.Summary.Measured = card.Measured
	payload.Summary.Hits = card.Hits
	payload.Summary.Pending = card.Pending
	for _, item := range items {
		if item.Status != insightStatusOpen {
			continue
		}
		payload.Summary.Open++
		if item.Confidence == insightConfidenceHigh {
			payload.Summary.OpenHigh++
		}
		payload.Summary.ValueSatDay += item.ValueSatDay
		if item.Kind == insightKindIdleCapital {
			for _, ev := range item.Evidence {
				if ev.Key == "local_sat" {
					payload.Summary.IdleCapitalSat += int64(ev.Value)
				}
			}
		}
	}

	s.insightsCacheMu.Lock()
	cachedCopy := payload
	s.insightsCache = &cachedCopy
	s.insightsCacheMu.Unlock()
	return payload, nil
}
