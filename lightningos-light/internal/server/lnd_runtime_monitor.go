package server

import (
	"context"
	"fmt"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	lndRuntimeMonitorTick  = 15 * time.Second
	lndRuntimeProbeTimeout = 6 * time.Second
)

func (s *Server) startLNDRuntimeMonitor() {
	if s == nil || s.lnd == nil {
		return
	}
	go s.runLNDRuntimeMonitor(s.shutdownContext())
}

func (s *Server) runLNDRuntimeMonitor(ctx context.Context) {
	probe := func() lndclient.RuntimeInfo {
		probeCtx, cancel := context.WithTimeout(ctx, lndRuntimeProbeTimeout)
		defer cancel()
		info, _ := s.lnd.RuntimeInfo(probeCtx, false)
		return info
	}

	previousKey := ""
	observe := func() {
		info := probe()
		key := lndRuntimeInfoKey(info)
		if key == previousKey {
			return
		}
		previousKey = key
		if s.logger != nil {
			s.logger.Printf(
				"lnd runtime state: known=%t stale=%t chain=%t graph=%t active=%d inactive=%d pending=%d peers=%d",
				info.Known, info.Stale, info.SyncedToChain, info.SyncedToGraph,
				info.NumActiveChannels, info.NumInactiveChannels,
				info.NumPendingChannels, info.NumPeers,
			)
		}
	}

	observe()
	ticker := time.NewTicker(lndRuntimeMonitorTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			observe()
		}
	}
}

func lndRuntimeInfoKey(info lndclient.RuntimeInfo) string {
	return fmt.Sprintf(
		"%t|%t|%t|%t|%d|%d|%d|%d",
		info.Known, info.Stale, info.SyncedToChain, info.SyncedToGraph,
		info.NumActiveChannels, info.NumInactiveChannels,
		info.NumPendingChannels, info.NumPeers,
	)
}

func lndRuntimeHasNoChannels(info lndclient.RuntimeInfo) bool {
	return info.Known && info.NumActiveChannels == 0 &&
		info.NumInactiveChannels == 0 && info.NumPendingChannels == 0
}
