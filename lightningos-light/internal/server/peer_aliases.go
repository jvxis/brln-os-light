package server

import (
	"context"
	"strings"

	"lightningos-light/internal/lndclient"
)

func (s *Server) lookupGraphExplorerNodes(ctx context.Context, pubkeys []string) map[string]GraphExplorerNodeLookup {
	svc, _ := s.graphExplorerService()
	if svc == nil {
		return nil
	}
	nodes, err := svc.LookupNodes(ctx, pubkeys)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer node lookup failed: %v", err)
		}
		return nil
	}
	return nodes
}

func (s *Server) enrichPeerAliasesFromGraph(ctx context.Context, peers []lndclient.PeerInfo) []lndclient.PeerInfo {
	pubkeys := make([]string, 0, len(peers))
	for _, peer := range peers {
		if strings.TrimSpace(peer.Alias) != "" {
			continue
		}
		if pubkey := strings.TrimSpace(peer.PubKey); pubkey != "" {
			pubkeys = append(pubkeys, pubkey)
		}
	}
	return applyPeerAliasLookups(peers, s.lookupGraphExplorerNodes(ctx, pubkeys), true)
}

func applyPeerAliasLookups(peers []lndclient.PeerInfo, lookups map[string]GraphExplorerNodeLookup, fallbackShort bool) []lndclient.PeerInfo {
	if len(peers) == 0 {
		return peers
	}
	out := make([]lndclient.PeerInfo, len(peers))
	copy(out, peers)
	for i := range out {
		if strings.TrimSpace(out[i].Alias) != "" {
			out[i].Alias = strings.TrimSpace(out[i].Alias)
			continue
		}
		pubkey := graphExplorerNormalizePubkey(out[i].PubKey)
		if pubkey == "" {
			continue
		}
		if node, ok := lookups[pubkey]; ok {
			if alias := strings.TrimSpace(node.Alias); alias != "" {
				out[i].Alias = alias
				continue
			}
		}
		if fallbackShort {
			out[i].Alias = shortPubKey(out[i].PubKey)
		}
	}
	return out
}
