package lndclient

import (
	"testing"
	"time"

	"lightningos-light/lnrpc/routerrpc"
)

func TestMissionControlHalfLife(t *testing.T) {
	if got := missionControlHalfLife(nil); got != 0 {
		t.Fatalf("expected no half-life without a config, got %s", got)
	}
	legacy := &routerrpc.MissionControlConfig{HalfLifeSeconds: 3600}
	if got := missionControlHalfLife(legacy); got != time.Hour {
		t.Fatalf("expected the top-level half-life, got %s", got)
	}
	apriori := &routerrpc.MissionControlConfig{
		HalfLifeSeconds: 3600,
		EstimatorConfig: &routerrpc.MissionControlConfig_Apriori{Apriori: &routerrpc.AprioriParameters{HalfLifeSeconds: 1800}},
	}
	if got := missionControlHalfLife(apriori); got != 30*time.Minute {
		t.Fatalf("expected the apriori penalty half-life, got %s", got)
	}
	bimodal := &routerrpc.MissionControlConfig{
		EstimatorConfig: &routerrpc.MissionControlConfig_Bimodal{Bimodal: &routerrpc.BimodalParameters{DecayTime: 7200}},
	}
	if got := missionControlHalfLife(bimodal); got != 2*time.Hour {
		t.Fatalf("expected the bimodal decay time, got %s", got)
	}
}
