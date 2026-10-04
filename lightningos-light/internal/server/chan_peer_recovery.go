package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const chanRecoveryStatePath = "/var/lib/lightningos/channel-peer-recovery.json"
const chanRecoveryWaitEnv = "LND_CHAN_HEAL_RECOVERY_WAIT_SEC"
const chanRecoveryCooldown = time.Hour
const chanRecoveryWindow = 24 * time.Hour
const chanRecoveryMaxAttempts = 2

type chanRecoveryLND interface {
	ListChannels(context.Context) ([]lndclient.ChannelInfo, error)
	ListPeers(context.Context) ([]lndclient.PeerInfo, error)
	ListPendingChannels(context.Context) ([]lndclient.PendingChannelInfo, error)
	DisconnectPeer(context.Context, string) error
}

type chanRecoveryPeer struct {
	Pubkey      string      `json:"pubkey"`
	State       string      `json:"state"`
	Detail      string      `json:"detail"`
	Attempts    []time.Time `json:"attempts"`
	VerifyUntil time.Time   `json:"verify_until,omitempty"`
	Points      []string    `json:"channel_points,omitempty"`
	Since       time.Time   `json:"-"`
	LastSeen    time.Time   `json:"-"`
}

// Owned exclusively by the healer's serialized tick. Observation is deliberately
// not restored after restart; only the attempt budget and verification survive.
type chanPeerRecovery struct {
	lnd     chanRecoveryLND
	logger  *log.Logger
	path    string
	wait    time.Duration
	peers   map[string]*chanRecoveryPeer
	loadErr error
	dirty   bool
	notify  func(string, string, string, time.Time)
}

func newChanPeerRecovery(lnd chanRecoveryLND, logger *log.Logger, path string, wait time.Duration) *chanPeerRecovery {
	r := &chanPeerRecovery{lnd: lnd, logger: logger, path: path, wait: wait, peers: map[string]*chanRecoveryPeer{}}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		r.loadErr = fmt.Errorf("recovery state is not a regular file")
		return r
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return r
	}
	if err == nil {
		err = json.Unmarshal(data, &r.peers)
	}
	if err == nil && r.peers == nil {
		err = fmt.Errorf("empty recovery state")
	}
	if err == nil {
		for key, p := range r.peers {
			if p == nil || p.Pubkey != key || len(p.Attempts) > chanRecoveryMaxAttempts {
				err = fmt.Errorf("invalid recovery state")
				break
			}
		}
	}
	r.loadErr = err // Invalid/unreadable state must never reset the attempt budget.
	return r
}

func readChanRecoveryWait() time.Duration {
	value := strings.TrimSpace(os.Getenv(chanRecoveryWaitEnv))
	if value == "" {
		value, _ = readEnvFileValue(secretsPath, chanRecoveryWaitEnv)
	}
	if d := parseEnvSeconds(value); d >= time.Minute && d <= 24*time.Hour {
		return d
	}
	return 15 * time.Minute
}

func (r *chanPeerRecovery) save() error {
	data, err := json.Marshal(r.peers)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(r.path), ".channel-recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), r.path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(r.path))
		if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return err
		}
	}
	r.dirty = false
	return nil
}

func (r *chanPeerRecovery) resetObservation() {
	for _, p := range r.peers {
		if p != nil {
			p.Since = time.Time{}
			p.LastSeen = time.Time{}
		}
	}
}

func (r *chanPeerRecovery) event(p *chanRecoveryPeer, state, detail string, now time.Time) {
	changed := p.State != state || p.Detail != detail
	r.dirty = r.dirty || changed
	p.State, p.Detail = state, detail
	if changed && r.logger != nil {
		r.logger.Printf("chan-heal recovery: peer=%s state=%s detail=%s", shortIdentifier(p.Pubkey), state, detail)
	}
	if changed && r.notify != nil && (state == "failed" || state == "exhausted" || state == "recovered") {
		r.notify(p.Pubkey, state, detail, now)
	}
}

func recoveryChannels(channels []lndclient.ChannelInfo) map[string][]lndclient.ChannelInfo {
	out := map[string][]lndclient.ChannelInfo{}
	for _, ch := range channels {
		key := normalizeChanHealPubkey(ch.RemotePubkey)
		if key != "" {
			out[key] = append(out[key], ch)
		}
	}
	return out
}

func safeRecoveryChannels(channels []lndclient.ChannelInfo, pending []lndclient.PendingChannelInfo, key string) bool {
	if len(channels) == 0 {
		return false
	}
	for _, ch := range channels {
		flags := strings.TrimSpace(ch.ChanStatusFlags)
		if ch.Active || ch.ChannelPoint == "" || ch.PendingHtlcCount > 0 || len(ch.PendingHtlcs) > 0 || ch.UnsettledBalanceSat > 0 ||
			(flags != "" && flags != "ChanStatusDefault") {
			return false
		}
	}
	for _, ch := range pending {
		if normalizeChanHealPubkey(ch.RemotePubkey) == key || ch.RemotePubkey == "" {
			return false
		}
	}
	return true
}

func recoveryPoints(channels []lndclient.ChannelInfo) []string {
	points := make([]string, 0, len(channels))
	for _, ch := range channels {
		points = append(points, ch.ChannelPoint)
	}
	sort.Strings(points)
	return points
}

// No reconnect RPC is issued here. While verification is pending, legacy
// reconnect is suppressed for this peer so LND gets the requested recovery window.
func (r *chanPeerRecovery) step(now time.Time, interval time.Duration, channels []lndclient.ChannelInfo, enabled func() bool) (map[string]bool, []chanRecoveryPeer, error) {
	blocked := map[string]bool{}
	if r.loadErr != nil {
		return blocked, nil, fmt.Errorf("channel recovery state unavailable: %w", r.loadErr)
	}
	for key, p := range r.peers {
		if !p.VerifyUntil.IsZero() {
			blocked[key] = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
	defer cancel()
	peers, err := r.lnd.ListPeers(ctx)
	if err != nil {
		r.resetObservation()
		return blocked, r.snapshot(), err
	}
	pending, err := r.lnd.ListPendingChannels(ctx)
	if err != nil {
		r.resetObservation()
		return blocked, r.snapshot(), err
	}
	connected, grouped := chanHealConnectedPeerSet(peers), recoveryChannels(channels)
	for key := range grouped {
		if r.peers[key] == nil {
			r.peers[key] = &chanRecoveryPeer{Pubkey: key}
		}
	}
	keys := make([]string, 0, len(r.peers))
	for key := range r.peers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	acted := false
	for _, key := range keys {
		p, group := r.peers[key], grouped[key]
		kept := p.Attempts[:0]
		for _, at := range p.Attempts {
			if now.Sub(at) < chanRecoveryWindow {
				kept = append(kept, at)
			}
		}
		p.Attempts = kept
		_, online := connected[key]
		if !p.VerifyUntil.IsZero() {
			allActive := len(group) > 0 && online && strings.Join(recoveryPoints(group), ",") == strings.Join(p.Points, ",")
			anyActive := false
			for _, ch := range group {
				allActive = allActive && ch.Active
				anyActive = anyActive || ch.Active
			}
			switch {
			case allActive:
				r.event(p, "recovered", "all observed channels are active again", now)
			case anyActive:
				r.event(p, "cancelled", "another channel with this peer is active; no further recovery", now)
			case len(group) == 0 || strings.Join(recoveryPoints(group), ",") != strings.Join(p.Points, ","):
				r.event(p, "cancelled", "channel set changed during verification", now)
			case !now.Before(p.VerifyUntil):
				r.event(p, "failed", "LND did not restore the peer and all channels before the verification deadline", now)
			default:
				continue
			}
			p.VerifyUntil, p.Since = time.Time{}, time.Time{}
			if err := r.save(); err != nil {
				return blocked, r.snapshot(), err
			}
			continue
		}
		if !online || !safeRecoveryChannels(group, pending, key) {
			p.Since, p.LastSeen = time.Time{}, time.Time{}
			allActive := online && len(group) > 0
			for _, ch := range group {
				allActive = allActive && ch.Active
			}
			if allActive && (p.State == "failed" || p.State == "exhausted") {
				r.event(p, "recovered", "channels are now active; no further recovery required", now)
			}
			if p.State == "observing" {
				r.event(p, "cancelled", "recovery condition no longer applies", now)
			}
			if len(p.Attempts) == 0 && (len(group) == 0 || p.State == "") {
				delete(r.peers, key)
			}
			continue
		}
		if len(p.Attempts) >= chanRecoveryMaxAttempts {
			r.event(p, "exhausted", "two attempts in 24 hours; manual review required", now)
			continue
		}
		if len(p.Attempts) > 0 && now.Sub(p.Attempts[len(p.Attempts)-1]) < chanRecoveryCooldown {
			continue
		}
		points := recoveryPoints(group)
		if p.Since.IsZero() || now.Sub(p.LastSeen) > 2*interval || !p.LastSeen.Before(now) || strings.Join(points, ",") != strings.Join(p.Points, ",") {
			p.Since = now
			p.Points = points
		}
		p.LastSeen = now
		r.event(p, "observing", "waiting for persistently inactive channels with a connected peer", now)
		if acted || now.Sub(p.Since) < r.wait {
			continue
		}
		// Re-read ALL channels for the peer immediately before disconnecting.
		freshCtx, freshCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
		fresh, e := r.lnd.ListChannels(freshCtx)
		var freshPeers []lndclient.PeerInfo
		var freshPending []lndclient.PendingChannelInfo
		if e == nil {
			freshPeers, e = r.lnd.ListPeers(freshCtx)
		}
		if e == nil {
			freshPending, e = r.lnd.ListPendingChannels(freshCtx)
		}
		freshCancel()
		if e != nil {
			r.resetObservation()
			return blocked, r.snapshot(), e
		}
		group = recoveryChannels(fresh)[key]
		_, online = chanHealConnectedPeerSet(freshPeers)[key]
		if !enabled() || !online || !safeRecoveryChannels(group, freshPending, key) || strings.Join(recoveryPoints(group), ",") != strings.Join(p.Points, ",") {
			p.Since = time.Time{}
			continue
		}
		p.Attempts = append(p.Attempts, now)
		p.Points = recoveryPoints(group)
		verify := 2 * interval
		if verify < 2*time.Minute {
			verify = 2 * time.Minute
		}
		p.VerifyUntil = now.Add(verify)
		p.Since = time.Time{}
		r.event(p, "verifying", "disconnect requested; waiting for LND to reconnect", now)
		// Reserve the budget BEFORE the RPC, even if it fails or the process exits.
		if err := r.save(); err != nil {
			r.loadErr = err
			p.VerifyUntil = time.Time{}
			r.event(p, "failed", "attempt reservation could not be saved; no disconnect performed", now)
			return blocked, r.snapshot(), err
		}
		blocked[key], acted = true, true
		if !enabled() {
			p.VerifyUntil = time.Time{}
			r.event(p, "cancelled", "auto-heal disabled before disconnect", now)
			_ = r.save()
			continue
		}
		disconnectCtx, disconnectCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
		err = r.lnd.DisconnectPeer(disconnectCtx, key)
		disconnectCancel()
		if err != nil {
			p.VerifyUntil = time.Time{}
			r.event(p, "failed", fmt.Sprintf("disconnect failed: %v", err), now)
			if saveErr := r.save(); saveErr != nil {
				return blocked, r.snapshot(), saveErr
			}
		}
	}
	if r.dirty {
		if err := r.save(); err != nil {
			r.loadErr = err
			return blocked, r.snapshot(), err
		}
	}
	return blocked, r.snapshot(), nil
}

func (r *chanPeerRecovery) snapshot() []chanRecoveryPeer {
	out := []chanRecoveryPeer{}
	for _, p := range r.peers {
		if p.State == "" {
			continue
		}
		copy := *p
		copy.Attempts = append([]time.Time{}, p.Attempts...)
		copy.Points = append([]string{}, p.Points...)
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pubkey < out[j].Pubkey })
	return out
}
