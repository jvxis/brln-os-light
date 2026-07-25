package server

import (
	"testing"

	"lightningos-light/internal/lndclient"
)

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
	} {
		if _, _, err := splitLightningAddress(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
	if user, domain, err := splitLightningAddress("alice@example.com"); err != nil || user != "alice" || domain != "example.com" {
		t.Fatalf("valid address rejected: user=%q domain=%q err=%v", user, domain, err)
	}
}
