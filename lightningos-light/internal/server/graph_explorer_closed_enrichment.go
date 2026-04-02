package server

import "context"

func (s *GraphExplorerService) enrichLocalClosedChannelTypes(ctx context.Context, selectedPubkey string, items []GraphExplorerClosedChannel) {
	if len(items) == 0 {
		return
	}

	localPubkey := s.loadLocalPubkey(ctx)
	if localPubkey == "" {
		return
	}

	selectedPubkey = graphExplorerNormalizePubkey(selectedPubkey)
	needsLookup := false
	for _, item := range items {
		if normalizeGraphExplorerCloseType(item.CloseType) != "unknown" {
			continue
		}
		if selectedPubkey == localPubkey || graphExplorerNormalizePubkey(item.PeerPubKey) == localPubkey {
			needsLookup = true
			break
		}
	}
	if !needsLookup {
		return
	}

	lookup := s.loadLocalClosedChannelLookup(ctx)
	if len(lookup.byChanID) == 0 && len(lookup.byChanPoint) == 0 {
		return
	}

	for index := range items {
		item := &items[index]
		if normalizeGraphExplorerCloseType(item.CloseType) != "unknown" {
			continue
		}
		if selectedPubkey != localPubkey && graphExplorerNormalizePubkey(item.PeerPubKey) != localPubkey {
			continue
		}
		if closeType := normalizeGraphExplorerCloseType(lookup.find(item.ChannelID, item.ChanPoint)); closeType != "unknown" {
			item.CloseType = closeType
			if item.CloseSource == "" || item.CloseSource == "native" {
				item.CloseSource = "native+lnd"
			}
		}
	}
}
