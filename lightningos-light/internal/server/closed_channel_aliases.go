package server

import (
	"context"
	"time"

	"lightningos-light/internal/lndclient"
)

// Alias enrichment belongs to the presentation endpoint, not ListClosedChannels:
// close reconciliation, retirement and other financial callers need no metadata.
func (s *Server) enrichClosedChannelAliases(ctx context.Context, channels []lndclient.ClosedChannelInfo) []lndclient.ClosedChannelInfo {
	if s.db == nil {
		return channels
	}
	// Use the existing batch reader without initializing the graph service,
	// creating schema or starting graph synchronization/LND requests.
	reader := &GraphExplorerService{db: s.db}
	return resolveClosedChannelAliases(ctx, channels, reader.LookupNodes)
}

func resolveClosedChannelAliases(ctx context.Context, channels []lndclient.ClosedChannelInfo,
	lookup func(context.Context, []string) (map[string]GraphExplorerNodeLookup, error),
) []lndclient.ClosedChannelInfo {
	if len(channels) == 0 {
		return channels
	}
	out := append([]lndclient.ClosedChannelInfo(nil), channels...)
	keys := make([]string, 0, len(out))
	seen := make(map[string]bool)
	for i := range out {
		out[i].PeerAlias = lndclient.DisplayPeerAlias(out[i].PeerAlias)
		key := graphExplorerNormalizePubkey(out[i].RemotePubkey)
		if out[i].PeerAlias == "" && key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 || lookup == nil {
		return out
	}
	readCtx, cancel := context.WithTimeout(ctx, closedChannelAliasTimeout)
	defer cancel()
	nodes, err := lookup(readCtx, keys)
	if err != nil {
		// Missing metadata must never hide the channel history or delay it
		// indefinitely. The existing UI falls back to the original pubkey.
		return out
	}
	for i := range out {
		if out[i].PeerAlias == "" {
			key := graphExplorerNormalizePubkey(out[i].RemotePubkey)
			if key != "" {
				out[i].PeerAlias = lndclient.DisplayPeerAlias(nodes[key].Alias)
			}
		}
	}
	return out
}

const closedChannelAliasTimeout = 2 * time.Second
