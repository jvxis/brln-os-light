package server

import (
	"net/url"
	"testing"
	"time"

	"lightningos-light/internal/lndclient"
)

func exposureFixture(at time.Time) autofeePolicySnapshot {
	return autofeePolicySnapshot{LocalPolicyObservation: lndclient.LocalPolicyObservation{
		ChannelPoint: "synthetic:0", ChannelID: 9007199254740993, Active: true, LocalBalanceSat: 100000,
		Fees: &lndclient.ObservedChannelFees{RatePPM: 100, InboundRatePPM: -10},
	}, StartedAt: at.Add(-time.Second), ObservedAt: at, Session: "test"}
}

func TestAutofeeExposureConfidenceSafetyMatrix(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name, flag string
		change     func(*autofeePolicySnapshot)
	}{
		{"same", "", func(*autofeePolicySnapshot) {}},
		{"zero_is_valid", "", func(s *autofeePolicySnapshot) { s.Fees.RatePPM = 0 }},
		{"outgoing", "outgoing_changed", func(s *autofeePolicySnapshot) { s.Fees.RatePPM++ }},
		{"base", "outgoing_changed", func(s *autofeePolicySnapshot) { s.Fees.BaseMsat++ }},
		{"inbound", "inbound_changed", func(s *autofeePolicySnapshot) { s.Fees.InboundRatePPM-- }},
		{"unknown", "policy_unavailable", func(s *autofeePolicySnapshot) { s.Fees = nil }},
		{"restart", "observer_restart", func(s *autofeePolicySnapshot) { s.Session = "restart" }},
		{"queue_or_db_loss", "observation_loss", func(s *autofeePolicySnapshot) { s.Loss++ }},
		{"timeout_gap", "observation_gap", func(s *autofeePolicySnapshot) { s.ObservedAt = s.ObservedAt.Add(time.Hour) }},
		{"identity", "channel_identity_changed", func(s *autofeePolicySnapshot) { s.ChannelID++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, after := exposureFixture(now), exposureFixture(now.Add(autofeeExposurePeriod))
			if tc.name == "zero_is_valid" {
				before.Fees.RatePPM = 0
			}
			tc.change(&after)
			got := buildAutofeePolicyExposure(before, after, nil)
			if tc.flag == "" {
				if got.Confidence != "sampled" {
					t.Fatalf("%+v", got)
				}
				return
			}
			if got.Confidence != "unknown" || !containsTag(got.Flags, tc.flag) {
				t.Fatalf("missing uncertainty %s: %+v", tc.flag, got)
			}
		})
	}
}

func TestAutofeeExposureConcurrentChangesCannotBecomeAttributedPolicy(t *testing.T) {
	now := time.Now().UTC()
	before, after := exposureFixture(now), exposureFixture(now.Add(5*time.Minute))
	after.LocalBalanceSat--
	after.Active = false
	application := lndclient.PolicyApplicationObservation{StartedAt: now.Add(time.Minute), CompletedAt: now.Add(2 * time.Minute), Source: "manual", Acknowledged: true,
		Request: lndclient.UpdateChannelPolicyParams{ChannelPoint: after.ChannelPoint, FeeRatePpm: 200}}
	got := buildAutofeePolicyExposure(before, after, []lndclient.PolicyApplicationObservation{application})
	for _, flag := range []string{"policy_application_overlap", "manual_intervention", "liquidity_changed", "availability_changed", "unavailable_observed"} {
		if !containsTag(got.Flags, flag) {
			t.Fatalf("missing %s: %+v", flag, got)
		}
	}
	if got.Confidence != "unknown" || got.PolicyActivity != nil {
		t.Fatal("same sampled fee hid intervening manual update")
	}
	application.Request.ChannelPoint = "unrelated"
	if len(buildAutofeePolicyExposure(before, after, []lndclient.PolicyApplicationObservation{application}).Applications) != 0 {
		t.Fatal("unrelated channel contaminated exposure")
	}
	application.Request.ApplyAll = true
	if len(buildAutofeePolicyExposure(before, after, []lndclient.PolicyApplicationObservation{application}).Applications) != 1 {
		t.Fatal("global update omitted")
	}
}

func TestAutofeeExposureQueryBounds(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for _, suffix := range []string{"&limit=0", "&limit=201", "&limit=no", "&since=bad", "&until=2026-09-24T00:00:00Z", "&since=2026-09-01T00:00:00Z", "&since=2026-09-23T12:00:00Z"} {
		q, _ := url.ParseQuery("channel_point=synthetic:0" + suffix)
		if _, err := parseAutofeeExposureQuery(q, now); err == nil {
			t.Fatalf("accepted %s", suffix)
		}
	}
	if _, err := parseAutofeeExposureQuery(url.Values{}, now); err == nil {
		t.Fatal("unbounded channel query")
	}
	q, _ := url.ParseQuery("channel_point=synthetic:0&until=2026-09-22T12:00:00Z&limit=2")
	got, err := parseAutofeeExposureQuery(q, now)
	if err != nil || got.Until.Sub(got.Since) != 24*time.Hour || got.Limit != 2 {
		t.Fatalf("bad pagination default: %+v %v", got, err)
	}
}
