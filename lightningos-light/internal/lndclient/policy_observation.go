package lndclient

import (
	"context"
	"time"

	"lightningos-light/lnrpc"
)

// PolicyApplicationObservation records an RPC acknowledgement, not propagation
// or proof that the requested policy was applied. FailedUpdates is significant
// even when the transport returned no error. Never include raw RPC errors here.
type PolicyApplicationObservation struct {
	StartedAt     time.Time                 `json:"started_at"`
	CompletedAt   time.Time                 `json:"completed_at"`
	Source        string                    `json:"source"`
	Request       UpdateChannelPolicyParams `json:"request"`
	Acknowledged  bool                      `json:"acknowledged"`
	FailedUpdates int                       `json:"failed_updates"`
}

type policyObservationSourceKey struct{}

func WithPolicyObservationSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, policyObservationSourceKey{}, source)
}

// PolicyApplicationObservations has one diagnostics consumer. Producers never
// wait for that consumer or its database; queue loss is visible in each sample.
func (c *Client) PolicyApplicationObservations() <-chan PolicyApplicationObservation {
	c.policyObservationMu.Lock()
	defer c.policyObservationMu.Unlock()
	if c.policyObservations == nil {
		c.policyObservations = make(chan PolicyApplicationObservation, 256)
	}
	return c.policyObservations
}

func (c *Client) PolicyObservationLoss() uint64 {
	c.policyObservationMu.Lock()
	defer c.policyObservationMu.Unlock()
	return c.policyObservationLoss
}

func (c *Client) observePolicyApplication(ctx context.Context, params UpdateChannelPolicyParams, started time.Time, resp *lnrpc.PolicyUpdateResponse, err error) {
	source, _ := ctx.Value(policyObservationSourceKey{}).(string)
	if source == "" {
		source = "unspecified"
	}
	// Copy optional scalars: the caller retains ownership of its request.
	if params.MinHtlcMsat != nil {
		v := *params.MinHtlcMsat
		params.MinHtlcMsat = &v
	}
	if params.MaxHtlcMsat != nil {
		v := *params.MaxHtlcMsat
		params.MaxHtlcMsat = &v
	}
	event := PolicyApplicationObservation{
		StartedAt: started, CompletedAt: time.Now().UTC(), Source: source, Request: params,
		Acknowledged:  err == nil && resp != nil && len(resp.GetFailedUpdates()) == 0,
		FailedUpdates: len(resp.GetFailedUpdates()),
	}
	c.policyObservationMu.Lock()
	defer c.policyObservationMu.Unlock()
	if c.policyObservations == nil {
		return
	}
	select {
	case c.policyObservations <- event:
	default:
		c.policyObservationLoss++
	}
}

type ObservedChannelFees struct {
	BaseMsat        int64 `json:"base_msat"`
	RatePPM         int64 `json:"rate_ppm"`
	InboundBaseMsat int32 `json:"inbound_base_msat"`
	InboundRatePPM  int32 `json:"inbound_rate_ppm"`
}

type LocalPolicyObservation struct {
	ChannelPoint        string               `json:"channel_point"`
	ChannelID           uint64               `json:"channel_id,string"`
	Fees                *ObservedChannelFees `json:"fees"`
	Active              bool                 `json:"active"`
	Disabled            bool                 `json:"disabled"`
	LocalBalanceSat     int64                `json:"local_balance_sat"`
	UnsettledBalanceSat int64                `json:"unsettled_balance_sat"`
}

// ObserveLocalPolicies performs two bounded, read-only RPCs with no per-channel
// graph lookups, alias resolution, or writes to the decision engine's caches.
// The caller records the whole acquisition window: these RPCs are not atomic.
func (c *Client) ObserveLocalPolicies(ctx context.Context) ([]LocalPolicyObservation, error) {
	conn, release, err := c.borrowConn(ctx, grpcRoleAdminUnary)
	if err != nil {
		return nil, err
	}
	defer release()
	client := lnrpc.NewLightningClient(conn)
	channels, err := client.ListChannels(ctx, &lnrpc.ListChannelsRequest{})
	if err != nil {
		return nil, err
	}
	fees, err := client.FeeReport(ctx, &lnrpc.FeeReportRequest{})
	if err != nil {
		return nil, err
	}
	return localPolicyObservations(channels.GetChannels(), fees.GetChannelFees()), nil
}

func localPolicyObservations(channels []*lnrpc.Channel, fees []*lnrpc.ChannelFeeReport) []LocalPolicyObservation {
	byPoint := make(map[string]*lnrpc.ChannelFeeReport, len(fees))
	for _, fee := range fees {
		if fee != nil {
			byPoint[fee.ChannelPoint] = fee
		}
	}
	result := make([]LocalPolicyObservation, 0, len(channels))
	for _, ch := range channels {
		if ch == nil || ch.ChannelPoint == "" || ch.ChanId == 0 {
			continue
		}
		item := LocalPolicyObservation{ChannelPoint: ch.ChannelPoint, ChannelID: ch.ChanId,
			Active: ch.Active, Disabled: isLocalChanDisabledFlags(ch.ChanStatusFlags),
			LocalBalanceSat: ch.LocalBalance, UnsettledBalanceSat: ch.UnsettledBalance}
		if f := byPoint[ch.ChannelPoint]; f != nil && f.ChanId == ch.ChanId {
			item.Fees = &ObservedChannelFees{f.BaseFeeMsat, f.FeePerMil, f.InboundBaseFeeMsat, f.InboundFeePerMil}
		}
		result = append(result, item)
	}
	return result
}
