package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFinancialIntentBalancedContinuationPreservesApprovedTerms(t *testing.T) {
	transit := balancedOpenTransitDetails{OutputScript: "0014" + strings.Repeat("aa", 20), OutputSat: 50095, Key: balancedOpenKeyDescriptor{PublicKey: "02" + strings.Repeat("11", 32), Family: 805, Index: 1}}
	metadata := map[string]any{"execution_mode": balancedOpenExecutionModeDual, "initiator_transit": transit, "accepter_multisig_key": balancedOpenKeyDescriptor{PublicKey: "03" + strings.Repeat("22", 32)}, "accepter_transit": balancedOpenTransitDetails{TxID: strings.Repeat("bb", 32), OutputScript: "0014" + strings.Repeat("bb", 20), OutputSat: 50095}}
	raw, _ := json.Marshal(metadata)
	session := BalancedOpenSession{SessionID: "fixture", Role: balancedOpenRoleInitiator, PeerPubkey: "03" + strings.Repeat("33", 32), PeerHost: "127.0.0.1:1", CapacitySat: 100000, FeeRateSatVb: 1, CloseAddress: "fixture-owner", Metadata: raw}
	snapshot := financialSnapshot(balancedFinancialTerms(session))
	transit.TxID = strings.Repeat("cc", 32)
	transit.Vout = 1
	metadata["initiator_transit"] = transit
	metadata["last_execution_err"] = "native reply was lost"
	updated := session
	updated.Metadata, _ = json.Marshal(metadata)
	updated.UpdatedAt = time.Now()
	updated.State = balancedOpenStateProposalSent
	if !balancedFinancialContinuationMatches(snapshot, updated) {
		t.Fatal("native resolution of the existing funding identity changed approval")
	}
	for _, change := range []func(*BalancedOpenSession){
		func(s *BalancedOpenSession) { s.PeerPubkey = "changed" },
		func(s *BalancedOpenSession) { s.CapacitySat++ },
		func(s *BalancedOpenSession) { s.FeeRateSatVb++ },
		func(s *BalancedOpenSession) { s.CloseAddress = "changed" },
		func(s *BalancedOpenSession) { s.Private = !s.Private },
		func(s *BalancedOpenSession) { s.Role = balancedOpenRoleAccepter },
		func(s *BalancedOpenSession) {
			var m map[string]any
			json.Unmarshal(s.Metadata, &m)
			m["accepter_multisig_key"] = map[string]any{"public_key": "changed"}
			s.Metadata, _ = json.Marshal(m)
		},
		func(s *BalancedOpenSession) {
			var m map[string]any
			json.Unmarshal(s.Metadata, &m)
			m["initiator_transit"] = map[string]any{"output_script": "changed", "output_sat": 50095}
			s.Metadata, _ = json.Marshal(m)
		},
	} {
		changed := updated
		change(&changed)
		if balancedFinancialContinuationMatches(snapshot, changed) {
			t.Fatal("continuation inherited changed financial terms")
		}
	}
}
