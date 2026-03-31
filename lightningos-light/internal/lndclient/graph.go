package lndclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lightningos-light/lnrpc"
)

type GraphFeature struct {
	Name       string `json:"name,omitempty"`
	IsRequired bool   `json:"is_required,omitempty"`
	IsKnown    bool   `json:"is_known,omitempty"`
}

type GraphNodeAddress struct {
	Network string `json:"network,omitempty"`
	Addr    string `json:"addr,omitempty"`
}

type GraphNode struct {
	LastUpdate time.Time               `json:"last_update,omitempty"`
	PubKey     string                  `json:"pubkey"`
	Alias      string                  `json:"alias,omitempty"`
	Color      string                  `json:"color,omitempty"`
	Addresses  []GraphNodeAddress      `json:"addresses,omitempty"`
	Features   map[uint32]GraphFeature `json:"features,omitempty"`
}

type GraphRoutingPolicy struct {
	TimeLockDelta      uint32    `json:"time_lock_delta"`
	MinHtlcMsat        int64     `json:"min_htlc_msat"`
	FeeBaseMsat        int64     `json:"fee_base_msat"`
	FeeRatePpm         int64     `json:"fee_rate_ppm"`
	Disabled           bool      `json:"disabled"`
	MaxHtlcMsat        uint64    `json:"max_htlc_msat"`
	LastUpdate         time.Time `json:"last_update,omitempty"`
	InboundFeeBaseMsat int64     `json:"inbound_fee_base_msat"`
	InboundFeeRatePpm  int64     `json:"inbound_fee_rate_ppm"`
}

type GraphChannel struct {
	ChannelID   uint64              `json:"channel_id"`
	ChanPoint   string              `json:"chan_point"`
	LastUpdate  time.Time           `json:"last_update,omitempty"`
	Node1PubKey string              `json:"node1_pubkey"`
	Node2PubKey string              `json:"node2_pubkey"`
	CapacitySat int64               `json:"capacity_sat"`
	Node1Policy *GraphRoutingPolicy `json:"node1_policy,omitempty"`
	Node2Policy *GraphRoutingPolicy `json:"node2_policy,omitempty"`
}

type GraphSnapshot struct {
	Nodes    []GraphNode    `json:"nodes"`
	Channels []GraphChannel `json:"channels"`
}

type GraphNodeUpdate struct {
	PubKey    string                  `json:"pubkey"`
	Alias     string                  `json:"alias,omitempty"`
	Color     string                  `json:"color,omitempty"`
	Addresses []GraphNodeAddress      `json:"addresses,omitempty"`
	Features  map[uint32]GraphFeature `json:"features,omitempty"`
}

type GraphChannelUpdate struct {
	ChannelID       uint64              `json:"channel_id"`
	ChanPoint       string              `json:"chan_point"`
	CapacitySat     int64               `json:"capacity_sat"`
	AdvertisingNode string              `json:"advertising_node"`
	ConnectingNode  string              `json:"connecting_node"`
	RoutingPolicy   *GraphRoutingPolicy `json:"routing_policy,omitempty"`
}

type GraphClosedChannelUpdate struct {
	ChannelID    uint64 `json:"channel_id"`
	ChanPoint    string `json:"chan_point"`
	CapacitySat  int64  `json:"capacity_sat"`
	ClosedHeight uint32 `json:"closed_height"`
}

type GraphUpdate struct {
	ObservedAt     time.Time                  `json:"observed_at"`
	NodeUpdates    []GraphNodeUpdate          `json:"node_updates,omitempty"`
	ChannelUpdates []GraphChannelUpdate       `json:"channel_updates,omitempty"`
	ClosedChannels []GraphClosedChannelUpdate `json:"closed_channels,omitempty"`
}

type GraphSubscription interface {
	Recv() (GraphUpdate, error)
	Close() error
}

type graphSubscription struct {
	stream lnrpc.Lightning_SubscribeChannelGraphClient
	close  func() error
}

func (s *graphSubscription) Recv() (GraphUpdate, error) {
	if s == nil || s.stream == nil {
		return GraphUpdate{}, errors.New("graph subscription unavailable")
	}
	resp, err := s.stream.Recv()
	if err != nil {
		return GraphUpdate{}, err
	}
	return convertGraphUpdate(resp), nil
}

func (s *graphSubscription) Close() error {
	if s == nil || s.close == nil {
		return nil
	}
	return s.close()
}

func (c *Client) DescribeGraph(ctx context.Context) (GraphSnapshot, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return GraphSnapshot{}, err
	}
	defer conn.Close()

	client := lnrpc.NewLightningClient(conn)
	resp, err := client.DescribeGraph(ctx, &lnrpc.ChannelGraphRequest{IncludeUnannounced: false})
	if err != nil {
		return GraphSnapshot{}, err
	}
	return convertGraphSnapshot(resp), nil
}

func (c *Client) SubscribeChannelGraph(ctx context.Context) (GraphSubscription, error) {
	conn, err := c.dial(ctx, true)
	if err != nil {
		return nil, err
	}

	client := lnrpc.NewLightningClient(conn)
	stream, err := client.SubscribeChannelGraph(ctx, &lnrpc.GraphTopologySubscription{})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &graphSubscription{
		stream: stream,
		close:  conn.Close,
	}, nil
}

func convertGraphSnapshot(resp *lnrpc.ChannelGraph) GraphSnapshot {
	if resp == nil {
		return GraphSnapshot{}
	}

	nodes := make([]GraphNode, 0, len(resp.GetNodes()))
	for _, node := range resp.GetNodes() {
		if node == nil {
			continue
		}
		nodes = append(nodes, convertGraphNode(node))
	}

	channels := make([]GraphChannel, 0, len(resp.GetEdges()))
	for _, edge := range resp.GetEdges() {
		if edge == nil {
			continue
		}
		channels = append(channels, convertGraphChannel(edge))
	}

	return GraphSnapshot{
		Nodes:    nodes,
		Channels: channels,
	}
}

func convertGraphNode(node *lnrpc.LightningNode) GraphNode {
	return GraphNode{
		LastUpdate: unixTime(uint64(node.GetLastUpdate())),
		PubKey:     strings.TrimSpace(node.GetPubKey()),
		Alias:      strings.TrimSpace(node.GetAlias()),
		Color:      strings.TrimSpace(node.GetColor()),
		Addresses:  convertGraphNodeAddresses(node.GetAddresses()),
		Features:   convertGraphFeatures(node.GetFeatures()),
	}
}

func convertGraphChannel(edge *lnrpc.ChannelEdge) GraphChannel {
	return GraphChannel{
		ChannelID:   edge.GetChannelId(),
		ChanPoint:   strings.TrimSpace(edge.GetChanPoint()),
		LastUpdate:  unixTime(uint64(edge.GetLastUpdate())),
		Node1PubKey: strings.TrimSpace(edge.GetNode1Pub()),
		Node2PubKey: strings.TrimSpace(edge.GetNode2Pub()),
		CapacitySat: edge.GetCapacity(),
		Node1Policy: convertGraphRoutingPolicy(edge.GetNode1Policy()),
		Node2Policy: convertGraphRoutingPolicy(edge.GetNode2Policy()),
	}
}

func convertGraphRoutingPolicy(policy *lnrpc.RoutingPolicy) *GraphRoutingPolicy {
	if policy == nil {
		return nil
	}
	return &GraphRoutingPolicy{
		TimeLockDelta:      policy.GetTimeLockDelta(),
		MinHtlcMsat:        policy.GetMinHtlc(),
		FeeBaseMsat:        policy.GetFeeBaseMsat(),
		FeeRatePpm:         policy.GetFeeRateMilliMsat(),
		Disabled:           policy.GetDisabled(),
		MaxHtlcMsat:        policy.GetMaxHtlcMsat(),
		LastUpdate:         unixTime(uint64(policy.GetLastUpdate())),
		InboundFeeBaseMsat: int64(policy.GetInboundFeeBaseMsat()),
		InboundFeeRatePpm:  int64(policy.GetInboundFeeRateMilliMsat()),
	}
}

func convertGraphNodeAddresses(items []*lnrpc.NodeAddress) []GraphNodeAddress {
	if len(items) == 0 {
		return nil
	}
	addresses := make([]GraphNodeAddress, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		addr := strings.TrimSpace(item.GetAddr())
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		addresses = append(addresses, GraphNodeAddress{
			Network: strings.TrimSpace(item.GetNetwork()),
			Addr:    addr,
		})
	}
	if len(addresses) == 0 {
		return nil
	}
	return addresses
}

func convertGraphFeatures(items map[uint32]*lnrpc.Feature) map[uint32]GraphFeature {
	if len(items) == 0 {
		return nil
	}
	features := make(map[uint32]GraphFeature, len(items))
	for key, feature := range items {
		if feature == nil {
			continue
		}
		features[key] = GraphFeature{
			Name:       strings.TrimSpace(feature.GetName()),
			IsRequired: feature.GetIsRequired(),
			IsKnown:    feature.GetIsKnown(),
		}
	}
	return features
}

func convertGraphUpdate(resp *lnrpc.GraphTopologyUpdate) GraphUpdate {
	update := GraphUpdate{ObservedAt: time.Now().UTC()}
	if resp == nil {
		return update
	}

	if items := resp.GetNodeUpdates(); len(items) > 0 {
		update.NodeUpdates = make([]GraphNodeUpdate, 0, len(items))
		for _, item := range items {
			if item == nil {
				continue
			}
			update.NodeUpdates = append(update.NodeUpdates, GraphNodeUpdate{
				PubKey:    strings.TrimSpace(item.GetIdentityKey()),
				Alias:     strings.TrimSpace(item.GetAlias()),
				Color:     strings.TrimSpace(item.GetColor()),
				Addresses: convertGraphNodeAddresses(item.GetNodeAddresses()),
				Features:  convertGraphFeatures(item.GetFeatures()),
			})
		}
	}

	if items := resp.GetChannelUpdates(); len(items) > 0 {
		update.ChannelUpdates = make([]GraphChannelUpdate, 0, len(items))
		for _, item := range items {
			if item == nil {
				continue
			}
			update.ChannelUpdates = append(update.ChannelUpdates, GraphChannelUpdate{
				ChannelID:       item.GetChanId(),
				ChanPoint:       channelPointToString(item.GetChanPoint()),
				CapacitySat:     item.GetCapacity(),
				AdvertisingNode: strings.TrimSpace(item.GetAdvertisingNode()),
				ConnectingNode:  strings.TrimSpace(item.GetConnectingNode()),
				RoutingPolicy:   convertGraphRoutingPolicy(item.GetRoutingPolicy()),
			})
		}
	}

	if items := resp.GetClosedChans(); len(items) > 0 {
		update.ClosedChannels = make([]GraphClosedChannelUpdate, 0, len(items))
		for _, item := range items {
			if item == nil {
				continue
			}
			update.ClosedChannels = append(update.ClosedChannels, GraphClosedChannelUpdate{
				ChannelID:    item.GetChanId(),
				ChanPoint:    channelPointToString(item.GetChanPoint()),
				CapacitySat:  item.GetCapacity(),
				ClosedHeight: item.GetClosedHeight(),
			})
		}
	}

	return update
}

func channelPointToString(point *lnrpc.ChannelPoint) string {
	if point == nil {
		return ""
	}
	txid := strings.TrimSpace(point.GetFundingTxidStr())
	if txid == "" {
		txid = txidFromBytes(point.GetFundingTxidBytes())
	}
	if txid == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", strings.ToLower(txid), point.GetOutputIndex())
}

func unixTime(seconds uint64) time.Time {
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(int64(seconds), 0).UTC()
}
