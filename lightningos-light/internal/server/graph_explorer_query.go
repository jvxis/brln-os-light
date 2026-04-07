package server

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"

	"github.com/jackc/pgx/v5"
)

var ErrGraphExplorerNodeNotFound = errors.New("graph explorer node not found")
var graphExplorerSearchNoisePattern = regexp.MustCompile(`[^[:alnum:]]+`)

type GraphExplorerSearchResponse struct {
	Query         string                      `json:"query"`
	CoverageSince *time.Time                  `json:"coverage_since,omitempty"`
	Items         []GraphExplorerSearchResult `json:"items"`
}

type GraphExplorerSearchResult struct {
	PubKey              string     `json:"pubkey"`
	Alias               string     `json:"alias,omitempty"`
	Color               string     `json:"color,omitempty"`
	ChannelCount        int        `json:"channel_count"`
	TotalCapacitySat    int64      `json:"total_capacity_sat"`
	LastSeenAt          *time.Time `json:"last_seen_at,omitempty"`
	HasLocalOpenChannel bool       `json:"has_local_open_channel"`
}

type GraphExplorerNodeGeneral struct {
	CoverageSince *time.Time               `json:"coverage_since,omitempty"`
	Source        string                   `json:"source"`
	Node          GraphExplorerNodeProfile `json:"node"`
}

type GraphExplorerNodeProfile struct {
	PubKey                string                       `json:"pubkey"`
	Alias                 string                       `json:"alias,omitempty"`
	Color                 string                       `json:"color,omitempty"`
	Addresses             []lndclient.GraphNodeAddress `json:"addresses,omitempty"`
	AddressCount          int                          `json:"address_count"`
	ClearnetAddressCount  int                          `json:"clearnet_address_count"`
	OnionAddressCount     int                          `json:"onion_address_count"`
	ChannelCount          int                          `json:"channel_count"`
	OpenChannelCount      int                          `json:"open_channel_count"`
	PeerCount             int                          `json:"peer_count"`
	TotalCapacitySat      int64                        `json:"total_capacity_sat"`
	SmallestChannelSat    int64                        `json:"smallest_channel_sat"`
	LargestChannelSat     int64                        `json:"largest_channel_sat"`
	AverageChannelSizeSat int64                        `json:"average_channel_size_sat"`
	OldestChannelBlock    int                          `json:"oldest_channel_block"`
	YoungestChannelBlock  int                          `json:"youngest_channel_block"`
	FirstSeenAt           *time.Time                   `json:"first_seen_at,omitempty"`
	LastSeenAt            *time.Time                   `json:"last_seen_at,omitempty"`
	LastGraphUpdateAt     *time.Time                   `json:"last_graph_update_at,omitempty"`
	LastPolicyUpdateAt    *time.Time                   `json:"last_policy_update_at,omitempty"`
}

func (s *GraphExplorerService) SearchNodes(ctx context.Context, query string, limit int) (GraphExplorerSearchResponse, error) {
	if s == nil || s.db == nil {
		return GraphExplorerSearchResponse{}, ErrGraphExplorerDBUnavailable
	}

	query = strings.TrimSpace(query)
	if len(query) < 2 {
		return GraphExplorerSearchResponse{
			Query: query,
			Items: []GraphExplorerSearchResult{},
		}, nil
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	coverageSince, err := s.coverageSince(ctx)
	if err != nil {
		return GraphExplorerSearchResponse{}, err
	}
	localOpenPeers := s.loadLocalOpenPeerSet(ctx)
	normalizedQuery := normalizeGraphExplorerSearchText(query)

	rows, err := s.db.Query(ctx, `
with input as (
  select
    trim($1)::text as q,
    lower(trim($1)::text) as q_lower,
    trim(lower($2)::text) as q_norm
),
nodes as (
  select
    n.pubkey,
    coalesce(n.alias, '') as alias,
    coalesce(n.color, '') as color,
    n.channel_count,
    n.total_capacity_sat,
    n.last_seen_at,
    lower(coalesce(n.alias, '')) as alias_lower,
    trim(regexp_replace(lower(coalesce(n.alias, '')), '[^[:alnum:]]+', ' ', 'g')) as alias_norm
  from graph_nodes n
)
select
  n.pubkey,
  n.alias,
  n.color,
  n.channel_count,
  n.total_capacity_sat,
  n.last_seen_at
from nodes n
cross join input i
where i.q <> ''
  and (
    n.pubkey = i.q
    or n.pubkey like i.q || '%'
    or n.alias_lower = i.q_lower
    or n.alias_lower like i.q_lower || '%'
    or n.alias_lower like '%' || i.q_lower || '%'
    or (i.q_norm <> '' and n.alias_norm = i.q_norm)
    or (i.q_norm <> '' and n.alias_norm like i.q_norm || '%')
    or (i.q_norm <> '' and n.alias_norm like '%' || i.q_norm || '%')
  )
order by
  case
    when n.pubkey = i.q then 0
    when n.alias_lower = i.q_lower then 1
    when i.q_norm <> '' and n.alias_norm = i.q_norm then 2
    when n.pubkey like i.q || '%' then 3
    when n.alias_lower like i.q_lower || '%' then 4
    when i.q_norm <> '' and n.alias_norm like i.q_norm || '%' then 5
    else 6
  end asc,
  n.channel_count desc,
  n.total_capacity_sat desc,
  n.last_seen_at desc,
  n.pubkey asc
limit $2
`, query, normalizedQuery, limit)
	if err != nil {
		return GraphExplorerSearchResponse{}, err
	}
	defer rows.Close()

	items := make([]GraphExplorerSearchResult, 0, limit)
	for rows.Next() {
		var item GraphExplorerSearchResult
		if err := rows.Scan(
			&item.PubKey,
			&item.Alias,
			&item.Color,
			&item.ChannelCount,
			&item.TotalCapacitySat,
			&item.LastSeenAt,
		); err != nil {
			return GraphExplorerSearchResponse{}, err
		}
		item.PubKey = strings.TrimSpace(item.PubKey)
		item.Alias = strings.TrimSpace(item.Alias)
		item.Color = strings.TrimSpace(item.Color)
		item.HasLocalOpenChannel = graphExplorerHasLocalOpenChannel(localOpenPeers, item.PubKey)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return GraphExplorerSearchResponse{}, err
	}

	return GraphExplorerSearchResponse{
		Query:         query,
		CoverageSince: coverageSince,
		Items:         items,
	}, nil
}

func normalizeGraphExplorerSearchText(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	value = graphExplorerSearchNoisePattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " ")
}

func (s *GraphExplorerService) GetNodeGeneral(ctx context.Context, pubkey string) (GraphExplorerNodeGeneral, error) {
	if s == nil || s.db == nil {
		return GraphExplorerNodeGeneral{}, ErrGraphExplorerDBUnavailable
	}

	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return GraphExplorerNodeGeneral{}, ErrGraphExplorerNodeNotFound
	}

	coverageSince, err := s.coverageSince(ctx)
	if err != nil {
		return GraphExplorerNodeGeneral{}, err
	}

	var result GraphExplorerNodeGeneral
	result.CoverageSince = coverageSince

	var node GraphExplorerNodeProfile
	var rawAddresses []byte
	var source string
	err = s.db.QueryRow(ctx, `
select
  n.pubkey,
  coalesce(n.alias, ''),
  coalesce(n.color, ''),
  n.addresses_json,
  n.channel_count,
  coalesce(ch.open_channel_count, 0),
  coalesce(ch.peer_count, 0),
  n.total_capacity_sat,
  coalesce(ch.smallest_channel_sat, 0),
  coalesce(ch.largest_channel_sat, 0),
  coalesce(ch.average_channel_size_sat, 0),
  coalesce(ch.oldest_channel_block, 0),
  coalesce(ch.youngest_channel_block, 0),
  n.first_seen_at,
  n.last_seen_at,
  n.last_graph_update_at,
  policy.last_policy_update_at,
  coalesce(n.source, 'native')
from graph_nodes n
left join lateral (
  select
    count(*)::integer as open_channel_count,
    count(distinct case when ch.node1_pubkey = n.pubkey then ch.node2_pubkey else ch.node1_pubkey end)::integer as peer_count,
    min(ch.capacity_sat)::bigint as smallest_channel_sat,
    max(ch.capacity_sat)::bigint as largest_channel_sat,
    round(avg(ch.capacity_sat))::bigint as average_channel_size_sat,
    min(ch.open_block_height)::integer as oldest_channel_block,
    max(ch.open_block_height)::integer as youngest_channel_block
  from graph_channels ch
  where ch.status = 'open'
    and (ch.node1_pubkey = n.pubkey or ch.node2_pubkey = n.pubkey)
) ch on true
left join lateral (
  select max(policy.policy_last_update_at) as last_policy_update_at
  from graph_channels ch
  join graph_channel_policy_current policy on policy.chan_id = ch.chan_id
  where ch.status = 'open'
    and (ch.node1_pubkey = n.pubkey or ch.node2_pubkey = n.pubkey)
) policy on true
where n.pubkey = $1
`, pubkey).Scan(
		&node.PubKey,
		&node.Alias,
		&node.Color,
		&rawAddresses,
		&node.ChannelCount,
		&node.OpenChannelCount,
		&node.PeerCount,
		&node.TotalCapacitySat,
		&node.SmallestChannelSat,
		&node.LargestChannelSat,
		&node.AverageChannelSizeSat,
		&node.OldestChannelBlock,
		&node.YoungestChannelBlock,
		&node.FirstSeenAt,
		&node.LastSeenAt,
		&node.LastGraphUpdateAt,
		&node.LastPolicyUpdateAt,
		&source,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GraphExplorerNodeGeneral{}, ErrGraphExplorerNodeNotFound
		}
		return GraphExplorerNodeGeneral{}, err
	}

	addresses, err := decodeGraphNodeAddresses(rawAddresses)
	if err != nil {
		return GraphExplorerNodeGeneral{}, err
	}
	clearnetCount, onionCount := classifyGraphNodeAddresses(addresses)

	node.PubKey = strings.TrimSpace(node.PubKey)
	node.Alias = strings.TrimSpace(node.Alias)
	node.Color = strings.TrimSpace(node.Color)
	node.Addresses = addresses
	node.AddressCount = len(addresses)
	node.ClearnetAddressCount = clearnetCount
	node.OnionAddressCount = onionCount

	result.Source = strings.TrimSpace(source)
	result.Node = node
	return result, nil
}

func (s *GraphExplorerService) coverageSince(ctx context.Context) (*time.Time, error) {
	var coverageSince *time.Time
	err := s.db.QueryRow(ctx, `
select first_native_coverage_at
from graph_sync_state
where id = true
`).Scan(&coverageSince)
	if err != nil {
		return nil, err
	}
	return coverageSince, nil
}

func decodeGraphNodeAddresses(raw []byte) ([]lndclient.GraphNodeAddress, error) {
	if len(raw) == 0 {
		return []lndclient.GraphNodeAddress{}, nil
	}
	var addresses []lndclient.GraphNodeAddress
	if err := json.Unmarshal(raw, &addresses); err != nil {
		return nil, err
	}
	return normalizeGraphNodeAddresses(addresses), nil
}

func normalizeGraphNodeAddresses(addresses []lndclient.GraphNodeAddress) []lndclient.GraphNodeAddress {
	if len(addresses) == 0 {
		return []lndclient.GraphNodeAddress{}
	}
	normalized := make([]lndclient.GraphNodeAddress, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		addr := strings.TrimSpace(address.Addr)
		if addr == "" {
			continue
		}
		key := strings.ToLower(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, lndclient.GraphNodeAddress{
			Network: strings.TrimSpace(address.Network),
			Addr:    addr,
		})
	}
	if len(normalized) == 0 {
		return []lndclient.GraphNodeAddress{}
	}
	return normalized
}

func classifyGraphNodeAddresses(addresses []lndclient.GraphNodeAddress) (int, int) {
	clearnetCount := 0
	onionCount := 0
	for _, address := range addresses {
		addr := strings.ToLower(strings.TrimSpace(address.Addr))
		if addr == "" {
			continue
		}
		if strings.Contains(addr, ".onion") {
			onionCount++
			continue
		}
		clearnetCount++
	}
	return clearnetCount, onionCount
}
