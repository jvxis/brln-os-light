package server

import (
	"fmt"

	"lightningos-light/internal/lndclient"
)

const (
	channelOpenMaxFeeRatePpm  = int64(1_000_000)
	channelOpenMaxBaseFeeMsat = int64(100_000_000)
)

// parseChannelOpenPolicy validates the optional initial forwarding policy of
// a channel being opened. An omitted field keeps the LND default; zero is an
// explicit value (free routing, no base fee), so the pointers stay distinct
// from blanks all the way to LND.
func parseChannelOpenPolicy(feeRatePpm *int64, baseFeeMsat *int64) (lndclient.ChannelOpenPolicy, error) {
	var policy lndclient.ChannelOpenPolicy
	if feeRatePpm != nil {
		if *feeRatePpm < 0 || *feeRatePpm > channelOpenMaxFeeRatePpm {
			return policy, fmt.Errorf("fee_rate_ppm must be between 0 and %d", channelOpenMaxFeeRatePpm)
		}
		value := *feeRatePpm
		policy.FeeRatePpm = &value
	}
	if baseFeeMsat != nil {
		if *baseFeeMsat < 0 || *baseFeeMsat > channelOpenMaxBaseFeeMsat {
			return policy, fmt.Errorf("base_fee_msat must be between 0 and %d", channelOpenMaxBaseFeeMsat)
		}
		value := *baseFeeMsat
		policy.BaseFeeMsat = &value
	}
	return policy, nil
}

// channelOpenPolicyAudit adds the requested initial policy to an audit payload.
func channelOpenPolicyAudit(fields map[string]any, policy lndclient.ChannelOpenPolicy) map[string]any {
	if fields == nil {
		fields = map[string]any{}
	}
	if policy.FeeRatePpm != nil {
		fields["fee_rate_ppm"] = *policy.FeeRatePpm
	}
	if policy.BaseFeeMsat != nil {
		fields["base_fee_msat"] = *policy.BaseFeeMsat
	}
	return fields
}
