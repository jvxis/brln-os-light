package server

// autofeeCostEvidence describes the evaluator's margin input, not a realized
// cost or profit attribution. It is output-only: no pricing gate reads it.
// In particular, a global average or an outgoing reference is not channel cost.
type autofeeCostEvidence struct {
	Source              string `json:"source"`
	Kind                string `json:"kind"`
	ReferencePpm        int    `json:"reference_ppm"`
	EffectivePpm        int    `json:"effective_ppm"`
	MinAdjusted         bool   `json:"min_adjusted"`
	MarginActionable    bool   `json:"margin_actionable"`
	ForwardCount        int    `json:"forward_count"`
	NegativeMarginGuard bool   `json:"negative_margin_guard"`
}

func newAutofeeCostEvidence(source string, referencePpm, effectivePpm int, actionable bool, forwards int, tags []string) *autofeeCostEvidence {
	kind := "unknown"
	switch source {
	case "rebal", "rebal-recent":
		kind = "channel_rebalance"
	case "rebal-21d", "rebal-mem":
		kind = "historical_channel_rebalance"
	case "rebal-global":
		kind = "global_rebalance_reference"
	case "rebal-blend":
		kind = "blended_rebalance_reference"
	case "outrate", "outrate-mem":
		kind = "outgoing_reference"
	case "seed":
		kind = "market_reference"
	case "min":
		kind = "configured_minimum"
	}
	return &autofeeCostEvidence{
		Source: source, Kind: kind, ReferencePpm: referencePpm, EffectivePpm: effectivePpm,
		MinAdjusted: effectivePpm > referencePpm, MarginActionable: actionable,
		ForwardCount: forwards, NegativeMarginGuard: containsTag(tags, "no-down-neg-margin"),
	}
}
