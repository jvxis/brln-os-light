package server

import "testing"

func TestLNDMaintenanceOptionsRoundTripWhenInitiallyAbsent(t *testing.T) {
	original := "[Application Options]\nalias=test-node\n\n[Bitcoin]\nbitcoin.active=true\n"
	state := lndMaintenanceState{}
	state.PreviousRejectHTLCValue, state.PreviousRejectHTLCPresent = readLNDOption(original, "Application Options", "rejecthtlc")
	state.PreviousMaxPendingValue, state.PreviousMaxPendingPresent = readLNDOption(original, "Application Options", "maxpendingchannels")

	active := applyLNDMaintenanceOptions(original)
	if value, ok := readLNDOption(active, "Application Options", "rejecthtlc"); !ok || value != "true" {
		t.Fatalf("rejecthtlc not applied: value=%q present=%t", value, ok)
	}
	if value, ok := readLNDOption(active, "Application Options", "maxpendingchannels"); !ok || value != "0" {
		t.Fatalf("maxpendingchannels not applied: value=%q present=%t", value, ok)
	}

	restored := restoreLNDMaintenanceOptions(active, state)
	if restored != original {
		t.Fatalf("absent options did not round-trip exactly\noriginal: %q\nrestored: %q", original, restored)
	}
	if _, ok := readLNDOption(restored, "Application Options", "rejecthtlc"); ok {
		t.Fatal("rejecthtlc should have been removed")
	}
	if _, ok := readLNDOption(restored, "Application Options", "maxpendingchannels"); ok {
		t.Fatal("maxpendingchannels should have been removed")
	}
	if value, ok := readLNDOption(restored, "Application Options", "alias"); !ok || value != "test-node" {
		t.Fatalf("unrelated option changed: value=%q present=%t", value, ok)
	}
}

func TestLNDMaintenanceOptionsRestorePreviousValues(t *testing.T) {
	original := "[application options]\nRejectHTLC=false\nmaxpendingchannels=7\nalias=preserved\n"
	state := lndMaintenanceState{}
	state.PreviousRejectHTLCValue, state.PreviousRejectHTLCPresent = readLNDOption(original, "Application Options", "rejecthtlc")
	state.PreviousMaxPendingValue, state.PreviousMaxPendingPresent = readLNDOption(original, "Application Options", "maxpendingchannels")

	restored := restoreLNDMaintenanceOptions(applyLNDMaintenanceOptions(original), state)
	if value, ok := readLNDOption(restored, "Application Options", "rejecthtlc"); !ok || value != "false" {
		t.Fatalf("rejecthtlc not restored: value=%q present=%t", value, ok)
	}
	if value, ok := readLNDOption(restored, "Application Options", "maxpendingchannels"); !ok || value != "7" {
		t.Fatalf("maxpendingchannels not restored: value=%q present=%t", value, ok)
	}
	if value, ok := readLNDOption(restored, "Application Options", "alias"); !ok || value != "preserved" {
		t.Fatalf("unrelated option changed: value=%q present=%t", value, ok)
	}
}

func TestSetOrRemoveLNDOptionCreatesApplicationSection(t *testing.T) {
	original := "[Bitcoin]\nbitcoin.active=true"
	updated := applyLNDMaintenanceOptions(original)
	if value, ok := readLNDOption(updated, "Application Options", "rejecthtlc"); !ok || value != "true" {
		t.Fatalf("missing created rejecthtlc option: value=%q present=%t", value, ok)
	}
	if value, ok := readLNDOption(updated, "Application Options", "maxpendingchannels"); !ok || value != "0" {
		t.Fatalf("missing created maxpendingchannels option: value=%q present=%t", value, ok)
	}
}
