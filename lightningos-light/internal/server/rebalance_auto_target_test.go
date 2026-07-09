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
		{"cap never below min", 500_000_000, 10}, // 1% would be <min(10) => clamps to min
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
	// Defaults after normalize: max=50 min=10 step=5 minDrain=5000 minRev=500
	// up=0.5 down=0.25 drainFirst=3.0 maxLocalSat=5_000_000.

	cases := []struct {
		name        string
		sig         autoTargetSignals
		wantDir     string
		wantNew     int
		wantReason  string
	}{
		{
			name: "UP sells fast and viable",
			sig: autoTargetSignals{
				CurrentTargetPct: 45, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9337, SuccessRate: 0.67, Attempts: 6, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 50, wantReason: "sells_fast_viable",
		},
		{
			name: "UP drain-first with no route history",
			sig: autoTargetSignals{
				CurrentTargetPct: 20, CapacitySat: 3_000_000, DrainRateSatPerHr: 20000,
				Revenue7dSat: 1200, SuccessRate: 0, Attempts: 0, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 25, wantReason: "drain_first",
		},
		{
			name: "drain-first blocked when drain below multiplier",
			sig: autoTargetSignals{
				CurrentTargetPct: 20, CapacitySat: 3_000_000, DrainRateSatPerHr: 9000, // < 5000*3
				Revenue7dSat: 1200, SuccessRate: 0, Attempts: 0, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 20, wantReason: "hold",
		},
		{
			name: "non-candidate never raises even with strong signals",
			sig: autoTargetSignals{
				CurrentTargetPct: 45, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9337, SuccessRate: 0.67, Attempts: 6, StructuralFails24h: 0,
				IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 45, wantReason: "hold",
		},
		{
			name: "DOWN drain stalled",
			sig: autoTargetSignals{
				CurrentTargetPct: 30, CapacitySat: 3_000_000, DrainRateSatPerHr: 500, // < 5000/4
				Revenue7dSat: 100, SuccessRate: 0, Attempts: 0, StructuralFails24h: 0,
				IsRoundCandidate: false,
			},
			wantDir: autoTargetDown, wantNew: 25, wantReason: "drain_stalled",
		},
		{
			name: "DOWN low success on a draining candidate",
			sig: autoTargetSignals{
				CurrentTargetPct: 45, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.1, Attempts: 8, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetDown, wantNew: 40, wantReason: "low_success",
		},
		{
			name: "DOWN structural cooldowns",
			sig: autoTargetSignals{
				CurrentTargetPct: 40, CapacitySat: 3_000_000, DrainRateSatPerHr: 30000,
				Revenue7dSat: 2000, SuccessRate: 0, Attempts: 0, StructuralFails24h: 3,
				IsRoundCandidate: false,
			},
			wantDir: autoTargetDown, wantNew: 35, wantReason: "structural_cooldowns",
		},
		{
			name: "hysteresis hold in the neutral success band",
			sig: autoTargetSignals{
				CurrentTargetPct: 45, CapacitySat: 3_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.35, Attempts: 8, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 45, wantReason: "hold",
		},
		{
			name: "UP clamped to min floor cannot go below min",
			sig: autoTargetSignals{
				CurrentTargetPct: 10, CapacitySat: 3_000_000, DrainRateSatPerHr: 500,
				Revenue7dSat: 0, SuccessRate: 0, Attempts: 0, StructuralFails24h: 0,
				IsRoundCandidate: false,
			},
			wantDir: autoTargetNoop, wantNew: 10, wantReason: "at_min",
		},
		{
			name: "giant channel cap blocks UP",
			sig: autoTargetSignals{
				CurrentTargetPct: 30, CapacitySat: 50_000_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.8, Attempts: 6, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetNoop, wantNew: 30, wantReason: "at_effective_max",
		},
		{
			name: "capacity cap allows partial UP up to effective max",
			sig: autoTargetSignals{
				CurrentTargetPct: 42, CapacitySat: 11_200_000, DrainRateSatPerHr: 60000,
				Revenue7dSat: 9000, SuccessRate: 0.8, Attempts: 6, StructuralFails24h: 0,
				IsRoundCandidate: true,
			},
			wantDir: autoTargetUp, wantNew: 44, wantReason: "sells_fast_viable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := decideAutoTargetAdjustment(tc.sig, cfg)
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

	// Invalid band (min >= max) and inverted hysteresis get corrected.
	cfg := def
	cfg.AutoTargetMaxPct = 40
	cfg.AutoTargetMinPct = 45 // >= max
	cfg.AutoTargetUpSuccessThreshold = 0.3
	cfg.AutoTargetDownSuccessThreshold = 0.6 // >= up
	cfg.AutoTargetStepPct = 0                // invalid
	cfg.AutoTargetDrainFirstMultiplier = 0   // invalid
	got := normalizeRebalanceConfig(cfg)

	if got.AutoTargetMinPct >= got.AutoTargetMaxPct {
		t.Fatalf("min (%d) should be < max (%d) after normalize", got.AutoTargetMinPct, got.AutoTargetMaxPct)
	}
	if got.AutoTargetDownSuccessThreshold >= got.AutoTargetUpSuccessThreshold {
		t.Fatalf("down (%v) should be < up (%v) after normalize", got.AutoTargetDownSuccessThreshold, got.AutoTargetUpSuccessThreshold)
	}
	if got.AutoTargetStepPct != def.AutoTargetStepPct {
		t.Fatalf("step should fall back to default, got %d", got.AutoTargetStepPct)
	}
	if got.AutoTargetDrainFirstMultiplier != def.AutoTargetDrainFirstMultiplier {
		t.Fatalf("drain-first multiplier should fall back to default, got %v", got.AutoTargetDrainFirstMultiplier)
	}
}
