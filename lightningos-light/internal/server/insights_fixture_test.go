package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestInsightsFixture replays the rule engine over JSON captured from a live
// node's API (the phase-0 backtest of docs/insights-recommendations-plan.md).
// It is skipped unless INSIGHTS_FIXTURE_DIR points at a directory holding
// ranking.json, channels.json, history.json, intents.json, candidates.json
// and optionally magma.json / autofee.json, as the endpoints return them.
//
//	INSIGHTS_FIXTURE_DIR=/path/to/FP go test ./internal/server -run TestInsightsFixture -v
func TestInsightsFixture(t *testing.T) {
	dir := strings.TrimSpace(os.Getenv("INSIGHTS_FIXTURE_DIR"))
	if dir == "" {
		t.Skip("INSIGHTS_FIXTURE_DIR not set")
	}
	read := func(name string, dst any) bool {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Logf("fixture %s: %v", name, err)
			return false
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatalf("fixture %s: %v", name, err)
		}
		return true
	}

	var ranking struct {
		Items []ChannelRankingItem `json:"items"`
	}
	read("ranking.json", &ranking)
	var channels struct {
		Channels []RebalanceChannel `json:"channels"`
	}
	read("channels.json", &channels)
	var history struct {
		Jobs []RebalanceJob `json:"jobs"`
	}
	read("history.json", &history)
	var intents struct {
		Items []AutomationIntent `json:"items"`
	}
	read("intents.json", &intents)
	var candidates struct {
		Items []ChannelOpenCandidateItem `json:"items"`
	}
	read("candidates.json", &candidates)
	var magma struct {
		Items       []MagmaChannelCommitment `json:"items"`
		Commitments []MagmaChannelCommitment `json:"commitments"`
	}
	magmaKnown := read("magma.json", &magma)
	magma.Items = append(magma.Items, magma.Commitments...)
	var autofee struct {
		Config struct {
			Profile string `json:"profile"`
		} `json:"config"`
		Profile string `json:"profile"`
	}
	read("autofee.json", &autofee)
	profileName := autofee.Config.Profile
	if profileName == "" {
		profileName = autofee.Profile
	}

	in := insightsInput{
		Ranking:         ranking.Items,
		Rebalance:       channels.Channels,
		Jobs24h:         history.Jobs,
		RefillIntents:   map[uint64][]AutomationIntent{},
		OpenCandidates:  candidates.Items,
		Commitments:     magma.Items,
		MagmaStateKnown: magmaKnown,
		LoopKnown:       true,
		LoopInstalled:   true,
		AutofeeProfile:  autofeeProfiles[effectiveAutofeeProfileName(profileName)],
	}
	for _, intent := range intents.Items {
		if intent.Kind == automationIntentKindRefillTarget && intent.Active {
			in.RefillIntents[intent.ChannelID] = append(in.RefillIntents[intent.ChannelID], intent)
		}
	}

	items := buildInsights(in, time.Now().UTC())
	t.Logf("%d channels, %d rebalance rows, %d jobs 24h, %d refill intents, %d candidates -> %d insights",
		len(ranking.Items), len(channels.Channels), len(history.Jobs), len(in.RefillIntents), len(candidates.Items), len(items))
	for _, item := range items {
		alias := item.PeerAlias
		if alias == "" {
			alias = item.PeerPubkey
		}
		if len(alias) > 22 {
			alias = alias[:22]
		}
		var ev []string
		for _, e := range item.Evidence {
			switch {
			case e.Extra != "" && e.Value == 0:
				ev = append(ev, fmt.Sprintf("%s=%s", e.Key, e.Extra))
			case e.Compare > 0:
				ev = append(ev, fmt.Sprintf("%s=%g/%g%s", e.Key, e.Value, e.Compare, e.Unit))
			default:
				ev = append(ev, fmt.Sprintf("%s=%g%s", e.Key, e.Value, e.Unit))
			}
		}
		var tools []string
		for _, a := range item.Actions {
			tools = append(tools, a.Tool)
		}
		var guards []string
		for _, g := range item.Guards {
			mark := "+"
			if !g.Passed {
				mark = "-"
			}
			guards = append(guards, mark+g.Code)
		}
		t.Logf("%-3s %-16s %-22s conf=%-6s value=%6d prio=%5.0f | %s | %s | %s",
			item.Rule, item.Kind, alias, item.Confidence, item.ValueSatDay, item.Priority, strings.Join(ev, " "), strings.Join(tools, ","), strings.Join(guards, ","))
	}
}
