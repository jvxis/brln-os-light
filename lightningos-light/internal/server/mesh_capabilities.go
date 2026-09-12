package server

import (
	"context"
	"lightningos-light/internal/mesh"
	"time"
)

type meshCapability struct {
	Session  [16]byte
	Probed   time.Time
	Answered time.Time
	Until    time.Time
}

func (m *meshService) supportsReplies(node uint32) bool {
	c := m.capabilities[node]
	return c != nil && time.Now().Before(c.Until)
}
func (m *meshService) capabilityTick(ctx context.Context, local uint32, peers []meshContact) {
	if len(m.outgoing) > 0 || time.Since(m.capabilitySent) < 30*time.Second {
		return
	}
	if m.capabilities == nil {
		m.capabilities = map[uint32]*meshCapability{}
	}
	// Prune removed contacts to retain the existing 16-peer bound.
	for node := range m.capabilities {
		found := false
		for _, p := range peers {
			if p.Node == node {
				found = true
				break
			}
		}
		if !found {
			delete(m.capabilities, node)
		}
	}
	for _, p := range peers {
		if !p.Paired {
			continue
		}
		c := m.capabilities[p.Node]
		if c == nil {
			c = &meshCapability{}
			m.capabilities[p.Node] = c
		}
		if time.Since(c.Probed) < 5*time.Minute {
			continue
		}
		id, err := randomMeshSession()
		if err != nil {
			return
		}
		c.Session = id
		c.Probed = time.Now()
		m.capabilitySent = c.Probed
		_ = m.send(ctx, mesh.Packet{Kind: mesh.Capabilities, From: local, To: p.Node, Session: id, Expires: time.Now().Add(time.Minute).Unix(), Payload: []byte{1, 0}}, p.Key)
		return
	}
}
func (m *meshService) capabilityReceive(ctx context.Context, p mesh.Packet, peer meshContact) {
	if !peer.Paired || peer.Node != p.From || len(p.Payload) != 2 || p.Payload[0] != 1 || p.Payload[1] > 1 {
		return
	}
	if m.capabilities == nil {
		m.capabilities = map[uint32]*meshCapability{}
	}
	c := m.capabilities[p.From]
	if c == nil {
		if len(m.capabilities) >= 16 {
			return
		}
		c = &meshCapability{}
		m.capabilities[p.From] = c
	}
	if p.Payload[1] == 1 {
		if p.Session == c.Session && time.Since(c.Probed) < time.Minute {
			c.Until = time.Now().Add(10 * time.Minute)
		}
		return
	}
	if time.Since(c.Answered) < 30*time.Second {
		return
	}
	c.Answered = time.Now()
	c.Until = time.Now().Add(10 * time.Minute)
	_ = m.send(ctx, reply(p, mesh.Capabilities, []byte{1, 1}), peer.Key)
}
