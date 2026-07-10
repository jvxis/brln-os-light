package server

import "testing"

func TestAutoTargetEffectiveMaxPct(t *testing.T) {
	cfg := defaultRebalanceConfig() // MaxPct=50, MinPct=10, MaxLocalSat=5_000_000
	cases := []struct {
		name     string
		capacity int64
		want     int
	}{
		{"small channel uses configured max", 2_000_000, 50},
		{"exact cap boundary", 10_000_000, 50},
		{"large channel capped below max", 20_000_000, 25},
		{"giant channel capped hard", 50_000_000, 10},
		{"cap never below min", 500_000_000, 10},
		{"zero capacity ignores cap", 0, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoTargetEffectiveMaxPct(cfg, tc.capacity); got != tc.want {
				t.Fatalf("effectiveMax(cap=%d) = %d, want %d", tc.capacity, got, tc.want)
			}
		})
	}
}

func TestDecideAutoTargetAdjustment(t *testing.T) {
	cfg := defaultRebalanceConfig()
	// Defaults: up_factor 1.1, down_factor 0.5, min_revenue 500, drain_first_mult 3,
	// max 50, min 10, step 5, max_local 5_000_000.
	// Node baseline: sell-through 0.65 → up thresh 0.715, down thresh 0.325.
	base := autoTargetNodeBaseline{SellThrough: 0.65, MedianRevenue7dSat: 500, DrainP70: 30000}

	cases := []struct {
		name       string
		sig        autoTargetSignals
		wantDir    string
		wantNew    int
		wantReason string
	}{
		{
			name: "UP sells above node baseline",
			sig: autoTargetSignals{
				CurrentTargetPct: 40, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.3, Attempts: 6, SellThrough: 0.90,
				HasHistory: true, IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 45, wantReason: "sells_above_node",
		},
		{
			name: "kappa: hard route (low success) but high sell-through still rises",
			sig: autoTargetSignals{
				CurrentTargetPct: 35, CapacitySat: 5_000_000, DrainRateSatPerHr: 14000,
				Revenue7dSat: 12000, SuccessRate: 0.05, Attempts: 8, SellThrough: 0.85,
				HasHistory: true, IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 40, wantReason: "sells_above_node",
		},
		{
			name: "dead route (many attempts, zero success) blocks UP",
			sig: autoTargetSignals{
				CurrentTargetPct: 40, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0, Attempts: 8, SellThrough: 0.90,
				HasHistory: true, IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 40, wantReason: "hold",
		},
		{
			name: "drain-first bootstrap (no history, strong earner + draining)",
			sig: autoTargetSignals{
				CurrentTargetPct: 20, CapacitySat: 3_000_000, DrainRateSatPerHr: 40000,
				Revenue7dSat: 2000, SuccessRate: 0, Attempts: 0, SellThrough: 0,
				HasHistory: false, IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 25, wantReason: "drain_first",
		},
		{
			name: "no history + weak revenue does not drain-first",
			sig: autoTargetSignals{
				CurrentTargetPct: 20, CapacitySat: 3_000_000, DrainRateSatPerHr: 40000,
				Revenue7dSat: 1000, SuccessRate: 0, Attempts: 0, SellThrough: 0,
				HasHistory: false, IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 20, wantReason: "hold",
		},
		{
			name: "sell-through in the hysteresis band holds",
			sig: autoTargetSignals{
				CurrentTargetPct: 40, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 100, SuccessRate: 0.3, Attempts: 6, SellThrough: 0.50,
				HasHistory: true, IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 40, wantReason: "hold",
		},
		{
			name: "non-candidate never rises",
			sig: autoTargetSignals{
				CurrentTargetPct: 40, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.5, Attempts: 6, SellThrough: 0.90,
				HasHistory: true, IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 40, wantReason: "hold",
		},
		{
			name: "DOWN fill-and-hold (absorbed capital, did not sell, no revenue)",
			sig: autoTargetSignals{
				CurrentTargetPct: 30, CapacitySat: 3_000_000, DrainRateSatPerHr: 200,
				Revenue7dSat: 100, SuccessRate: 0.2, Attempts: 4, SellThrough: 0.20,
				HasHistory: true, IsRoundCandidate: false,
			},
			wantDir: autoTargetDown, wantNew: 25, wantReason: "fill_and_hold",
		},
		{
			name: "earner with low sell-through is NOT demoted",
			sig: autoTargetSignals{
				CurrentTargetPct: 45, CapacitySat: 10_000_000, DrainRateSatPerHr: 0,
				Revenue7dSat: 9000, SuccessRate: 0, Attempts: 2, SellThrough: 0.10,
				HasHistory: true, IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 45, wantReason: "hold",
		},
		{
			name: "channel with no rebalance history is NOT demoted on a quiet window",
			sig: autoTargetSignals{
				CurrentTargetPct: 25, CapacitySat: 5_000_000, DrainRateSatPerHr: 0,
				Revenue7dSat: 0, SuccessRate: 0, Attempts: 0, SellThrough: 0,
				HasHistory: false, IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 25, wantReason: "hold",
		},
		{
			name: "DOWN clamped at min floor",
			sig: autoTargetSignals{
				CurrentTargetPct: 10, CapacitySat: 3_000_000, DrainRateSatPerHr: 0,
				Revenue7dSat: 0, SuccessRate: 0, Attempts: 3, SellThrough: 0.05,
				HasHistory: true, IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 10, wantReason: "at_min",
		},
		{
			name: "giant channel capacity cap blocks UP",
			sig: autoTargetSignals{
				CurrentTargetPct: 30, CapacitySat: 50_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.5, Attempts: 6, SellThrough: 0.90,
				HasHistory: true, IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 30, wantReason: "at_effective_max",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := decideAutoTargetAdjustment(tc.sig, base, cfg)
			if dec.Direction != tc.wantDir {
				t.Fatalf("direction = %q, want %q (%+v)", dec.Direction, tc.wantDir, dec)
			}
			if dec.NewTarget != tc.wantNew {
				t.Fatalf("newTarget = %d, want %d (%+v)", dec.NewTarget, tc.wantNew, dec)
			}
			if dec.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", dec.Reason, tc.wantReason)
			}
		})
	}
}

func TestNormalizeRebalanceConfigAutoTarget(t *testing.T) {
	def := defaultRebalanceConfig()

	cfg := def
	cfg.AutoTargetMaxPct = 40
	cfg.AutoTargetMinPct = 45 // >= max
	cfg.AutoTargetStepPct = 0 // invalid
	cfg.AutoTargetUpSellThroughFactor = 0.8
	cfg.AutoTargetDownSellThroughFactor = 1.2 // >= up
	cfg.AutoTargetMaxDownsPerCycle = 0         // invalid
	got := normalizeRebalanceConfig(cfg)

	if got.AutoTargetMinPct >= got.AutoTargetMaxPct {
		t.Fatalf("min (%d) should be < max (%d)", got.AutoTargetMinPct, got.AutoTargetMaxPct)
	}
	if got.AutoTargetStepPct != def.AutoTargetStepPct {
		t.Fatalf("step should fall back to default, got %d", got.AutoTargetStepPct)
	}
	if got.AutoTargetDownSellThroughFactor >= got.AutoTargetUpSellThroughFactor {
		t.Fatalf("down factor (%v) should be < up factor (%v)", got.AutoTargetDownSellThroughFactor, got.AutoTargetUpSellThroughFactor)
	}
	if got.AutoTargetMaxDownsPerCycle != def.AutoTargetMaxDownsPerCycle {
		t.Fatalf("max downs should fall back to default, got %d", got.AutoTargetMaxDownsPerCycle)
	}
}
