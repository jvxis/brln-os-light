package server

import (
	"context"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const graphExplorerLocalPeerLoadTimeout = 5 * time.Second
const graphExplorerLocalClosedLoadTimeout = 8 * time.Second

func (s *GraphExplorerService) loadLocalOpenPeerSet(ctx context.Context) map[string]struct{} {
	if s == nil || s.lnd == nil {
		return nil
	}
	loadCtx := ctx
	cancel := func() {}
	if ctx == nil {
		loadCtx, cancel = context.WithTimeout(context.Background(), graphExplorerLocalPeerLoadTimeout)
	} else {
		loadCtx, cancel = context.WithTimeout(ctx, graphExplorerLocalPeerLoadTimeout)
	}
	defer cancel()

	channels, err := s.lnd.ListChannels(loadCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer: local open peer set unavailable: %v", err)
		}
		return nil
	}
	return graphExplorerBuildLocalOpenPeerSet(channels)
}

func graphExplorerBuildLocalOpenPeerSet(channels []lndclient.ChannelInfo) map[string]struct{} {
	if len(channels) == 0 {
		return map[string]struct{}{}
	}
	peers := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		pubkey := graphExplorerNormalizePubkey(channel.RemotePubkey)
		if pubkey == "" {
			continue
		}
		peers[pubkey] = struct{}{}
	}
	return peers
}

func graphExplorerHasLocalOpenChannel(localOpenPeers map[string]struct{}, pubkey string) bool {
	if len(localOpenPeers) == 0 {
		return false
	}
	_, ok := localOpenPeers[graphExplorerNormalizePubkey(pubkey)]
	return ok
}

func graphExplorerNormalizePubkey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func graphExplorerNormalizeChanPoint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func (s *GraphExplorerService) loadLocalPubkey(ctx context.Context) string {
	if s == nil || s.lnd == nil {
		return ""
	}
	if pubkey := graphExplorerNormalizePubkey(s.lnd.CachedPubkey()); pubkey != "" {
		return pubkey
	}

	loadCtx := ctx
	cancel := func() {}
	if ctx == nil {
		loadCtx, cancel = context.WithTimeout(context.Background(), graphExplorerLocalPeerLoadTimeout)
	} else {
		loadCtx, cancel = context.WithTimeout(ctx, graphExplorerLocalPeerLoadTimeout)
	}
	defer cancel()

	status, err := s.lnd.GetStatus(loadCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer: local pubkey unavailable: %v", err)
		}
		return ""
	}
	return graphExplorerNormalizePubkey(status.Pubkey)
}

type graphExplorerLocalClosedChannelLookup struct {
	byChanID    map[uint64]graphExplorerLocalClosedChannelInfo
	byChanPoint map[string]graphExplorerLocalClosedChannelInfo
}

type graphExplorerLocalClosedChannelInfo struct {
	CloseType   string
	CloseTxID   string
	CloseHeight int
}

func (l graphExplorerLocalClosedChannelLookup) find(chanID uint64, chanPoint string) (graphExplorerLocalClosedChannelInfo, bool) {
	if chanID != 0 {
		if value, ok := l.byChanID[chanID]; ok {
			return value, true
		}
	}
	if point := graphExplorerNormalizeChanPoint(chanPoint); point != "" {
		if value, ok := l.byChanPoint[point]; ok {
			return value, true
		}
	}
	return graphExplorerLocalClosedChannelInfo{}, false
}

func (s *GraphExplorerService) loadLocalClosedChannelLookup(ctx context.Context) graphExplorerLocalClosedChannelLookup {
	lookup := graphExplorerLocalClosedChannelLookup{
		byChanID:    map[uint64]graphExplorerLocalClosedChannelInfo{},
		byChanPoint: map[string]graphExplorerLocalClosedChannelInfo{},
	}
	if s == nil || s.lnd == nil {
		return lookup
	}

	loadCtx := ctx
	cancel := func() {}
	if ctx == nil {
		loadCtx, cancel = context.WithTimeout(context.Background(), graphExplorerLocalClosedLoadTimeout)
	} else {
		loadCtx, cancel = context.WithTimeout(ctx, graphExplorerLocalClosedLoadTimeout)
	}
	defer cancel()

	channels, err := s.lnd.ListClosedChannels(loadCtx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("graph explorer: local closed channel lookup unavailable: %v", err)
		}
		return lookup
	}

	for _, channel := range channels {
		info := graphExplorerLocalClosedChannelInfo{
			CloseType:   normalizeGraphExplorerCloseType(channel.CloseTypeLabel),
			CloseTxID:   strings.ToLower(strings.TrimSpace(channel.ClosingTxHash)),
			CloseHeight: int(channel.CloseHeight),
		}
		if info.CloseType == "unknown" && info.CloseTxID == "" {
			continue
		}
		if channel.ChanID != 0 {
			lookup.byChanID[channel.ChanID] = info
		}
		if chanPoint := graphExplorerNormalizeChanPoint(channel.ChannelPoint); chanPoint != "" {
			lookup.byChanPoint[chanPoint] = info
		}
	}
	return lookup
}
