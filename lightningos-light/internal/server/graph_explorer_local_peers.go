package server

import (
	"context"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const graphExplorerLocalPeerLoadTimeout = 5 * time.Second

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
