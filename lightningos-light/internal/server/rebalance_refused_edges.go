package server

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"lightningos-light/lnrpc"
)

// refusedEdgeMemory carries the probe's liquidity verdict from one rebalance
// job to the next.
//
// Since 0.5.59 a job stops asking the pathfinder for a third-party channel
// direction once a probe was refused there at the minimum probe amount. That
// list died with the job: every source gets one attempt per job, so a job
// explored one layer of routes, and the next job for the same target started
// from the same cheapest routes and was refused at the same channels again.
// LND's mission control does record those failures, but the fee-first rung of
// the route query only needs a route above the minimum probability, so the
// penalty stops excluding the direction within minutes.
//
// No duration here is a constant. A first refusal is remembered for the
// half-life the node's own LND uses to age a failure. When the direction is
// retested after that and refuses again, it is remembered for as long as it
// has already been seen dry, so retests of a persistently dry direction thin
// out on their own. The history of a direction ends when a later probe gets
// through it, or when it has gone unrefused for the horizon the pair
// statistics already use to age a permanent failure.
//
// Process memory only: a manager restart clears it.
type refusedEdgeMemory struct {
	mu      sync.Mutex
	entries map[refusedEdgeKey]refusedEdgeEntry
}

type refusedEdgeKey struct {
	channelID uint64
	reverse   bool
}

type refusedEdgeEntry struct {
	firstRefused time.Time
	lastRefused  time.Time
	until        time.Time
}

func newRefusedEdgeMemory() *refusedEdgeMemory {
	return &refusedEdgeMemory{entries: map[refusedEdgeKey]refusedEdgeEntry{}}
}

// forgotten reports whether the direction went unrefused for the horizon the
// pair statistics use to age a permanent failure.
func (e refusedEdgeEntry) forgotten(now time.Time) bool {
	return now.Sub(e.lastRefused) > permanentFailScoreHalfLife
}

// clearForwarded ends the history of the third-party directions a probe got
// through: the hops before the one that refused it, or the whole route when
// the probe found an amount. This node's own first and last hops are never
// stored, so they are skipped.
func (m *refusedEdgeMemory) clearForwarded(route *lnrpc.Route, refusedIndex int, selfPubkey string) {
	if m == nil || route == nil {
		return
	}
	last := len(route.Hops)
	if refusedIndex >= 0 && refusedIndex < last {
		last = refusedIndex
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) == 0 {
		return
	}
	for i := 1; i < last; i++ {
		edge := rebalanceProbeFailedEdge(route, uint32(i), selfPubkey)
		if edge == nil {
			continue
		}
		delete(m.entries, refusedEdgeKey{channelID: edge.ChannelId, reverse: edge.DirectionReverse})
	}
}

// note records a probe refusal. seed is the node's mission control half-life;
// without it there is nothing to scale from and nothing is remembered.
func (m *refusedEdgeMemory) note(edge *lnrpc.EdgeLocator, now time.Time, seed time.Duration) {
	if m == nil || edge == nil || edge.ChannelId == 0 || seed <= 0 {
		return
	}
	key := refusedEdgeKey{channelID: edge.ChannelId, reverse: edge.DirectionReverse}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[key]
	if !ok || entry.forgotten(now) {
		entry = refusedEdgeEntry{firstRefused: now}
	}
	hold := seed
	if seenDry := now.Sub(entry.firstRefused); seenDry > hold {
		hold = seenDry
	}
	entry.lastRefused = now
	entry.until = now.Add(hold)
	m.entries[key] = entry
}

// active returns the directions still remembered, most recently refused
// first, and drops the forgotten ones.
func (m *refusedEdgeMemory) active(now time.Time, limit int) []*lnrpc.EdgeLocator {
	if m == nil || limit <= 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type remembered struct {
		key  refusedEdgeKey
		last time.Time
	}
	live := make([]remembered, 0, len(m.entries))
	for key, entry := range m.entries {
		if entry.forgotten(now) {
			delete(m.entries, key)
			continue
		}
		if now.After(entry.until) {
			continue
		}
		live = append(live, remembered{key: key, last: entry.lastRefused})
	}
	sort.Slice(live, func(i, j int) bool {
		if !live[i].last.Equal(live[j].last) {
			return live[i].last.After(live[j].last)
		}
		if live[i].key.channelID != live[j].key.channelID {
			return live[i].key.channelID < live[j].key.channelID
		}
		return !live[i].key.reverse && live[j].key.reverse
	})
	if len(live) > limit {
		live = live[:limit]
	}
	out := make([]*lnrpc.EdgeLocator, 0, len(live))
	for _, item := range live {
		out = append(out, &lnrpc.EdgeLocator{ChannelId: item.key.channelID, DirectionReverse: item.key.reverse})
	}
	return out
}

// rebalanceProbeFailureOnOwnChannel reports whether the refused hop is one of
// this node's own channels: the source channel (the first hop) or the target
// channel (the hop that pays back into this node). Those are judged by the
// job that owns them, with the pair and target cooldowns; remembering them
// across jobs would close a target or a source for every later job.
func rebalanceProbeFailureOnOwnChannel(route *lnrpc.Route, sourceIndex uint32, selfPubkey string) bool {
	if route == nil || uint64(sourceIndex) >= uint64(len(route.Hops)) {
		return true
	}
	if sourceIndex == 0 {
		return true
	}
	hop := route.Hops[sourceIndex]
	if hop == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(hop.PubKey), strings.TrimSpace(selfPubkey))
}

// refusedEdgeSeed is how long a first refusal is remembered: the half-life
// this node's LND applies to a routing failure. The live LND value wins; the
// rebalance setting that the manager writes to LND is the fallback when the
// node cannot be asked.
func (s *RebalanceService) refusedEdgeSeed(ctx context.Context, cfg RebalanceConfig) time.Duration {
	if s != nil && s.lnd != nil {
		if halfLife, err := s.lnd.MissionControlHalfLife(ctx); err == nil && halfLife > 0 {
			return halfLife
		}
	}
	if cfg.MissionControlHalfLifeSec > 0 {
		return time.Duration(cfg.MissionControlHalfLifeSec) * time.Second
	}
	return 0
}

func (s *RebalanceService) refusedEdgeStore() *refusedEdgeMemory {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refusedEdges == nil {
		s.refusedEdges = newRefusedEdgeMemory()
	}
	return s.refusedEdges
}
