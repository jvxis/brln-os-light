package server

import (
	"fmt"
	"testing"

	"lightningos-light/internal/lndclient"
)

type loopOutBRLNTestRowFunc func(...any) error

func (fn loopOutBRLNTestRowFunc) Scan(dest ...any) error { return fn(dest...) }

func TestLoopOutBRLNPartsNeverOvershoot(t *testing.T) {
	parts, last := loopOutBRLNParts(250_001, 100_000)
	if parts != 3 || last != 50_001 {
		t.Fatalf("parts=%d last=%d, want 3 and 50001", parts, last)
	}
	if got := int64(parts-1)*100_000 + last; got != 250_001 {
		t.Fatalf("planned total=%d, want 250001", got)
	}
	parts, last = loopOutBRLNParts(loopOutBRLNMaxAmountSat, loopOutBRLNMaxAmountSat-1)
	if parts != 2 || last != 1 {
		t.Fatalf("large parts=%d last=%d, want 2 and 1", parts, last)
	}
}

func TestLoopOutBRLNFeeLimitRoundsUp(t *testing.T) {
	if got := loopOutBRLNFeeLimitSat(100_001, 2_500); got != 251 {
		t.Fatalf("fee limit=%d, want 251", got)
	}
	if got := loopOutBRLNMaxFeeTotal(250_001, 100_000, 2_500); got != 626 {
		t.Fatalf("total fee limit=%d, want 626", got)
	}
	if got := loopOutBRLNFeeLimitSat(9_000_000_000_000_000, 1_000_000); got != 9_000_000_000_000_000 {
		t.Fatalf("large fee limit=%d, want overflow-safe exact value", got)
	}
}

func TestLoopOutBRLNCandidatesPreserveFloorAndReserve(t *testing.T) {
	channels := []lndclient.ChannelInfo{
		{ChannelID: 1, Active: true, CapacitySat: 1_000_000, LocalBalanceSat: 800_000, LocalChanReserveSat: 10_000},
		{ChannelID: 2, Active: true, CapacitySat: 1_000_000, LocalBalanceSat: 650_000, LocalChanReserveSat: 10_000},
		{ChannelID: 3, Active: false, CapacitySat: 1_000_000, LocalBalanceSat: 900_000},
		{ChannelID: 4, Active: true, LocalDisabled: true, CapacitySat: 1_000_000, LocalBalanceSat: 900_000},
		{ChannelID: 5, Active: true, CapacitySat: 100_000, LocalBalanceSat: 90_000, LocalChanReserveSat: 95_000},
	}
	candidates := loopOutBRLNCandidates(channels, nil, 100_000, 250, 60)
	if len(candidates) != 1 {
		t.Fatalf("candidate count=%d, want 1: %#v", len(candidates), candidates)
	}
	if candidates[0].channel.ChannelID != 1 {
		t.Fatalf("candidate=%d, want channel 1", candidates[0].channel.ChannelID)
	}
	if candidates[0].reserve != 600_000 || candidates[0].drainable != 200_000 {
		t.Fatalf("reserve/drainable=%d/%d, want 600000/200000", candidates[0].reserve, candidates[0].drainable)
	}
}

func TestLoopOutBRLNCandidatesHonorManualSelection(t *testing.T) {
	channels := []lndclient.ChannelInfo{
		{ChannelID: 1, Active: true, CapacitySat: 1_000_000, LocalBalanceSat: 900_000},
		{ChannelID: 2, Active: true, CapacitySat: 1_000_000, LocalBalanceSat: 850_000},
	}
	candidates := loopOutBRLNCandidates(channels, map[uint64]struct{}{2: {}}, 100_000, 0, 60)
	if len(candidates) != 1 || candidates[0].channel.ChannelID != 2 {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

func TestLoopOutBRLNChooseCandidateKeepsSuccessfulSource(t *testing.T) {
	candidates := []loopOutBRLNCandidate{
		{channel: lndclient.ChannelInfo{ChannelID: 1}},
		{channel: lndclient.ChannelInfo{ChannelID: 2}},
		{channel: lndclient.ChannelInfo{ChannelID: 3}},
	}
	candidate, ok := loopOutBRLNChooseCandidate(candidates, nil, "2")
	if !ok || candidate.channel.ChannelID != 2 {
		t.Fatalf("candidate=%d ok=%t, want sticky channel 2", candidate.channel.ChannelID, ok)
	}
}

func TestLoopOutBRLNChooseCandidateAdvancesAfterFailure(t *testing.T) {
	candidates := []loopOutBRLNCandidate{
		{channel: lndclient.ChannelInfo{ChannelID: 1}},
		{channel: lndclient.ChannelInfo{ChannelID: 2}},
		{channel: lndclient.ChannelInfo{ChannelID: 3}},
	}
	candidate, ok := loopOutBRLNChooseCandidate(candidates, map[uint64]struct{}{2: {}}, "2")
	if !ok || candidate.channel.ChannelID != 3 {
		t.Fatalf("candidate=%d ok=%t, want channel 3 after channel 2 failed", candidate.channel.ChannelID, ok)
	}
	candidate, ok = loopOutBRLNChooseCandidate(candidates, map[uint64]struct{}{2: {}, 3: {}}, "3")
	if !ok || candidate.channel.ChannelID != 1 {
		t.Fatalf("candidate=%d ok=%t, want wrapped channel 1", candidate.channel.ChannelID, ok)
	}
	if _, ok := loopOutBRLNChooseCandidate(candidates, map[uint64]struct{}{1: {}, 2: {}, 3: {}}, "3"); ok {
		t.Fatal("expected no candidate after every source failed in this round")
	}
}

func TestLoopOutBRLNChooseCandidateLeavesIneligibleCursor(t *testing.T) {
	// Candidate generation has already removed inactive channels and channels
	// below the liquidity/fee floor. If the cursor is absent, selection safely
	// falls back to the best remaining candidate.
	candidates := []loopOutBRLNCandidate{
		{channel: lndclient.ChannelInfo{ChannelID: 1}},
		{channel: lndclient.ChannelInfo{ChannelID: 3}},
	}
	candidate, ok := loopOutBRLNChooseCandidate(candidates, nil, "2")
	if !ok || candidate.channel.ChannelID != 1 {
		t.Fatalf("candidate=%d ok=%t, want best eligible channel 1", candidate.channel.ChannelID, ok)
	}
}

func TestLoopOutBRLNChooseCandidateKeepsCursorAfterFeeRefresh(t *testing.T) {
	// Editing max_fee_ppm rebuilds the candidate list on the next tick but does
	// not change the persisted source cursor. The current source remains sticky
	// whenever it still satisfies the refreshed fee and liquidity guardrails.
	candidatesAfterFeeChange := []loopOutBRLNCandidate{
		{channel: lndclient.ChannelInfo{ChannelID: 1}},
		{channel: lndclient.ChannelInfo{ChannelID: 2}},
	}
	candidate, ok := loopOutBRLNChooseCandidate(candidatesAfterFeeChange, nil, "2")
	if !ok || candidate.channel.ChannelID != 2 {
		t.Fatalf("candidate=%d ok=%t, want cursor channel 2 after fee refresh", candidate.channel.ChannelID, ok)
	}
}

func TestScanLoopOutBRLNJobRestoresSourceCursor(t *testing.T) {
	row := loopOutBRLNTestRowFunc(func(dest ...any) error {
		if len(dest) != 24 {
			return fmt.Errorf("scan destination count=%d, want 24", len(dest))
		}
		*dest[0].(*int64) = 7
		*dest[9].(*[]byte) = []byte(`["11","22"]`)
		*dest[17].(*string) = "22"
		return nil
	})
	job, err := scanLoopOutBRLNJob(row)
	if err != nil {
		t.Fatalf("scan job: %v", err)
	}
	if job.ID != 7 || job.SourceCursorChannelID != "22" {
		t.Fatalf("job id/cursor=%d/%q, want 7/22", job.ID, job.SourceCursorChannelID)
	}
	if len(job.SelectedChannelIDs) != 2 || job.SelectedChannelIDs[1] != "22" {
		t.Fatalf("selected ids=%#v, want persisted channel list", job.SelectedChannelIDs)
	}
}

func TestNormalizeLoopOutBRLNRequest(t *testing.T) {
	req, err := normalizeLoopOutBRLNRequest(LoopOutBRLNRequest{
		LightningAddress: " Alice@Example.com ", TotalSat: 200_000, TrancheSat: 100_000,
		IntervalSeconds: 15, MaxFeePPM: 2_500, MinLocalPercent: 60,
		SelectedChannelIDs: []string{"2", "2", "3"}, SuppressFailedTelegram: true,
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.LightningAddress != "alice@example.com" || req.TimeoutSeconds != 120 {
		t.Fatalf("unexpected normalized request: %#v", req)
	}
	if len(req.SelectedChannelIDs) != 2 {
		t.Fatalf("selected ids not deduplicated: %#v", req.SelectedChannelIDs)
	}
	if !req.SuppressFailedTelegram {
		t.Fatal("telegram failure suppression was not preserved")
	}
}

func TestNormalizeLoopOutBRLNRequestRejectsZeroFeeLimit(t *testing.T) {
	_, err := normalizeLoopOutBRLNRequest(LoopOutBRLNRequest{
		LightningAddress: "alice@example.com", TotalSat: 100_000, TrancheSat: 100_000,
		TimeoutSeconds: 120, MaxFeePPM: 0, MinLocalPercent: 60,
	})
	if err == nil {
		t.Fatal("expected zero max_fee_ppm to be rejected")
	}
}

func TestNormalizeLoopOutBRLNRequestRejectsUnsafeJSONAmount(t *testing.T) {
	_, err := normalizeLoopOutBRLNRequest(LoopOutBRLNRequest{
		LightningAddress: "alice@example.com", TotalSat: loopOutBRLNMaxAmountSat + 1, TrancheSat: 100_000,
		TimeoutSeconds: 120, MaxFeePPM: 2_500, MinLocalPercent: 60,
	})
	if err == nil {
		t.Fatal("expected amount above JavaScript's safe integer range to be rejected")
	}
}

func TestValidateLoopOutBRLNProviderChecksFinalTrancheAndComment(t *testing.T) {
	provider := lnurlPayResponse{MinSendable: 50_000_000, MaxSendable: 200_000_000, CommentAllowed: 8}
	valid := LoopOutBRLNRequest{TotalSat: 250_000, TrancheSat: 100_000, Comment: "loop"}
	if err := validateLoopOutBRLNProvider(provider, valid); err != nil {
		t.Fatalf("valid provider parameters rejected: %v", err)
	}
	if err := validateLoopOutBRLNProvider(provider, LoopOutBRLNRequest{TotalSat: 240_000, TrancheSat: 100_000}); err == nil {
		t.Fatal("expected final tranche below provider minimum to be rejected")
	}
	if err := validateLoopOutBRLNProvider(provider, LoopOutBRLNRequest{TotalSat: 100_000, TrancheSat: 100_000, Comment: "too-long-comment"}); err == nil {
		t.Fatal("expected provider comment limit to be enforced")
	}
}

func TestSplitLightningAddressRejectsPrivateHosts(t *testing.T) {
	for _, value := range []string{
		"alice@127.0.0.1",
		"alice@10.0.0.2",
		"alice@localhost",
		"alice@example.com/path",
		"alice@@example.com",
		"alice@example.com:99999",
		"alice@user:pass@example.com",
		"alice@example.com\nInjected: value",
	} {
		if _, _, err := splitLightningAddress(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if user, domain, err := splitLightningAddress("alice@example.com"); err != nil || user != "alice" || domain != "example.com" {
		t.Fatalf("valid address rejected: user=%q domain=%q err=%v", user, domain, err)
	}
	if user, domain, err := splitLightningAddress("alice+tips@example.com:443"); err != nil || user != "alice+tips" || domain != "example.com:443" {
		t.Fatalf("valid address with port rejected: user=%q domain=%q err=%v", user, domain, err)
	}
}

func TestValidateLNURLCallbackRejectsUnsafeURLs(t *testing.T) {
	for _, value := range []string{
		"http://example.com/callback",
		"https://127.0.0.1/callback",
		"https://10.0.0.2/callback",
		"https://localhost/callback",
		"https://user:pass@example.com/callback",
		"https://example.com:99999/callback",
		"https://example.com/callback#fragment",
		"https://example.com/callback\nInjected: value",
	} {
		if _, err := validateLNURLCallback(value); err == nil {
			t.Fatalf("expected unsafe callback %q to be rejected", value)
		}
	}
	for _, value := range []string{
		"https://example.com/lnurl/callback?tag=payRequest",
		"https://example.com?tag=payRequest",
		"https://[2001:4860:4860::8888]/callback",
	} {
		if _, err := validateLNURLCallback(value); err != nil {
			t.Fatalf("valid callback %q rejected: %v", value, err)
		}
	}
}
