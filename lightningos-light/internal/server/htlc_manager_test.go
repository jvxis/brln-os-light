package server

import (
  "testing"

  "lightningos-light/internal/lndclient"
)

func TestWithinMaxHTLCHysteresis(t *testing.T) {
  tests := []struct {
    name        string
    currentSat  int64
    targetSat   int64
    effectiveCapacitySat int64
    volatileBalance bool
    want        bool
  }{
    {
      name:        "small delta under threshold",
      currentSat:  981511,
      targetSat:   981557,
      effectiveCapacitySat: 1000000,
      volatileBalance: false,
      want:        true,
    },
    {
      name:        "large delta over threshold",
      currentSat:  981000,
      targetSat:   983000,
      effectiveCapacitySat: 1000000,
      volatileBalance: false,
      want:        false,
    },
    {
      name:        "tiny channel still uses base threshold",
      currentSat:  5000,
      targetSat:   5090,
      effectiveCapacitySat: 10000,
      volatileBalance: false,
      want:        true,
    },
    {
      name:        "tiny channel delta still below new relevance threshold",
      currentSat:  5000,
      targetSat:   5200,
      effectiveCapacitySat: 10000,
      volatileBalance: false,
      want:        true,
    },
    {
      name:        "volatile balance increases threshold",
      currentSat:  120000,
      targetSat:   130500,
      effectiveCapacitySat: 1000000,
      volatileBalance: true,
      want:        false,
    },
  }

  for _, tc := range tests {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      currentMsat, err := satToMsat(tc.currentSat)
      if err != nil {
        t.Fatalf("satToMsat(currentSat) failed: %v", err)
      }
      targetMsat, err := satToMsat(tc.targetSat)
      if err != nil {
        t.Fatalf("satToMsat(targetSat) failed: %v", err)
      }
      if got := withinMaxHTLCHysteresis(currentMsat, targetMsat, tc.effectiveCapacitySat, tc.volatileBalance); got != tc.want {
        t.Fatalf("withinMaxHTLCHysteresis(%d, %d, %d, %v) = %v, want %v", currentMsat, targetMsat, tc.effectiveCapacitySat, tc.volatileBalance, got, tc.want)
      }
    })
  }
}

func TestMinutesToHoursCeil(t *testing.T) {
  tests := []struct {
    in   int
    want int
  }{
    {in: 0, want: 0},
    {in: 1, want: 1},
    {in: 59, want: 1},
    {in: 60, want: 1},
    {in: 61, want: 2},
    {in: 240, want: 4},
  }
  for _, tc := range tests {
    if got := minutesToHoursCeil(tc.in); got != tc.want {
      t.Fatalf("minutesToHoursCeil(%d) = %d, want %d", tc.in, got, tc.want)
    }
  }
}

func TestValidateHTLCManagerConfigIntervalMinutes(t *testing.T) {
  cfg := defaultHTLCManagerConfig()
  cfg.IntervalMinutes = 30
  if err := validateHTLCManagerConfig(cfg); err != nil {
    t.Fatalf("expected valid config for 30 minutes: %v", err)
  }

  cfg.IntervalMinutes = 0
  if err := validateHTLCManagerConfig(cfg); err == nil {
    t.Fatalf("expected validation error for zero minutes")
  }
}

func TestComputeHTLCTargetsUsesLocalReserve(t *testing.T) {
  cfg := HtlcManagerConfig{
    MinHtlcSat: 1,
    MaxLocalPct: 0,
  }
  ch := lndclient.ChannelInfo{
    CapacitySat: 1_000_000,
    LocalBalanceSat: 250_000,
    LocalChanReserveSat: 50_000,
  }

  minMsat, maxMsat, err := computeHTLCTargets(ch, cfg)
  if err != nil {
    t.Fatalf("computeHTLCTargets failed: %v", err)
  }
  wantMin, _ := satToMsat(1)
  wantMax, _ := satToMsat(200_000) // 250k local - 50k reserve
  if minMsat != wantMin {
    t.Fatalf("minMsat=%d want=%d", minMsat, wantMin)
  }
  if maxMsat != wantMax {
    t.Fatalf("maxMsat=%d want=%d", maxMsat, wantMax)
  }
}

func TestComputeHTLCTargetsClampsToEffectiveCapacity(t *testing.T) {
  cfg := HtlcManagerConfig{
    MinHtlcSat: 1,
    MaxLocalPct: 50,
  }
  ch := lndclient.ChannelInfo{
    CapacitySat: 1_000_000,
    LocalBalanceSat: 990_000,
    LocalChanReserveSat: 100_000,
  }

  _, maxMsat, err := computeHTLCTargets(ch, cfg)
  if err != nil {
    t.Fatalf("computeHTLCTargets failed: %v", err)
  }
  wantMax, _ := satToMsat(900_000) // effective capacity = 1M - 100k
  if maxMsat != wantMax {
    t.Fatalf("maxMsat=%d want=%d", maxMsat, wantMax)
  }
}
