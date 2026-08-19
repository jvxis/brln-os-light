package reports

// A dry run compares each stored component against what the node recomputes
// today, to decide whether a full recalculation would improve the history or
// quietly damage it.
//
// The direction is the whole point and is easy to get backwards: recomputing
// *lower* than what is stored means the source data is gone. LND pruned it, and
// writing the new number would replace something real with something smaller and
// wrong. Recomputing higher is harmless - it means the original run saw less
// than the node holds now, usually because it ran before the day was over.
type DryRunVerdict int

const (
	DryRunMatches DryRunVerdict = iota
	DryRunHigher
	DryRunPruned
)

func (v DryRunVerdict) String() string {
	switch v {
	case DryRunPruned:
		return "LOWER - likely pruned"
	case DryRunHigher:
		return "higher"
	default:
		return "ok"
	}
}

// SafeToRecalculate reports whether a verdict allows overwriting the stored row.
func (v DryRunVerdict) SafeToRecalculate() bool { return v != DryRunPruned }

func CompareStoredComponent(stored, recomputed int64) DryRunVerdict {
	switch {
	case recomputed < stored:
		return DryRunPruned
	case recomputed > stored:
		return DryRunHigher
	default:
		return DryRunMatches
	}
}
