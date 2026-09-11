package server

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"lightningos-light/internal/mesh"
	"time"
)

type meshPairing struct {
	ID              string             `json:"id"`
	Node            uint32             `json:"node"`
	Name            string             `json:"name"`
	State           string             `json:"state"`
	Code            string             `json:"code,omitempty"`
	Expires         time.Time          `json:"expires"`
	LocalConfirmed  bool               `json:"local_confirmed"`
	RemoteConfirmed bool               `json:"remote_confirmed"`
	Invite          mesh.PairMessage   `json:"-"`
	Exchange        *mesh.PairExchange `json:"-"`
	Last            mesh.PairMessage   `json:"-"`
	Sent            time.Time          `json:"-"`
	Attempts        int                `json:"-"`
}

func (m *meshService) initPairing() {
	if m.pairings == nil {
		m.pairings = map[string]*meshPairing{}
		m.pairSeen = map[string]time.Time{}
		m.pairRate = map[uint32]time.Time{}
	}
}
func pairID(p mesh.PairMessage) string { return hex.EncodeToString(p.ID[:]) }
func (m *meshService) pairSend(ctx context.Context, p mesh.PairMessage) error {
	return m.bridge(ctx, "POST", "/packets", mesh.RadioPacket{To: p.To, Payload: p.Encode()}, nil)
}
func (m *meshService) pairQueue(ctx context.Context, p *meshPairing, msg mesh.PairMessage) {
	p.Last = msg
	p.Sent = time.Now()
	p.Attempts = 1
	_ = m.pairSend(ctx, msg)
}
func (m *meshService) pairTick(ctx context.Context) {
	m.initPairing()
	now := time.Now()
	for id, until := range m.pairSeen {
		if now.After(until) {
			delete(m.pairSeen, id)
		}
	}
	for node, last := range m.pairRate {
		if now.Sub(last) > 10*time.Minute {
			delete(m.pairRate, node)
		}
	}
	for id, p := range m.pairings {
		if now.After(p.Expires) {
			delete(m.pairings, id)
			continue
		}
		if p.Last.Kind != 0 && p.Attempts < 12 && now.Sub(p.Sent) >= 12*time.Second {
			out := p.Last
			if p.Exchange != nil && p.LocalConfirmed && !p.RemoteConfirmed && p.Attempts%2 == 0 {
				if reveal, e := p.Exchange.Reveal(); e == nil {
					out = reveal
				}
			}
			_ = m.pairSend(ctx, out)
			p.Sent = now
			p.Attempts++
		}
	}
}
func (m *meshService) pairName(ctx context.Context, node uint32) string {
	var nodes []mesh.RadioNode
	if m.bridge(ctx, "GET", "/nodes", nil, &nodes) == nil {
		for _, n := range nodes {
			if n.Node == node {
				if n.Name != "" && n.ShortName != "" && n.Name != n.ShortName {
					return n.Name + " (" + n.ShortName + ")"
				}
				if n.Name != "" {
					return n.Name
				}
				if n.ShortName != "" {
					return n.ShortName
				}
			}
		}
	}
	return fmt.Sprintf("!%08x", node)
}
func (m *meshService) pairAction(ctx context.Context, req meshAPIRequest) (any, error) {
	m.initPairing()
	var radio mesh.RadioStatus
	if m.bridge(ctx, "GET", "/status", nil, &radio) != nil || radio.State != "running" {
		return nil, errors.New("connect the radio first")
	}
	if req.Action == "peer_permissions" {
		if !req.Confirm {
			return nil, errors.New("confirm contact permissions")
		}
		tag, err := m.db.Exec(ctx, "UPDATE los_mesh_peers SET allow_relay=$2 WHERE node=$1 AND paired=true", req.Node, req.AllowRelay)
		if err != nil || tag.RowsAffected() != 1 {
			return nil, errors.New("verified contact not found")
		}
		return map[string]bool{"ok": true}, nil
	}
	if req.Action == "pair_probe" || req.Action == "pair_invite" {
		if req.Node == 0 || req.Node == radio.Node || req.Node == 0xffffffff {
			return nil, errors.New("select a remote node")
		}
		if req.Action == "pair_invite" && !req.Confirm {
			return nil, errors.New("confirm invitation")
		}
		hasResponse := false
		for _, p := range m.pairings {
			if p.Node == req.Node && p.State == "available" && time.Now().Before(p.Expires) {
				hasResponse = true
			}
			if p.Node == req.Node && time.Now().Before(p.Expires) && p.State != "available" && p.State != "verified" {
				return nil, errors.New("a request is already pending for this node")
			}
		}
		if len(m.pairings) >= 8 || len(m.pairSeen) >= 128 || len(m.pairRate) >= 256 || (time.Since(m.pairRate[req.Node]) < 15*time.Second && !(req.Action == "pair_invite" && hasResponse)) {
			return nil, errors.New("wait before sending another request")
		}
		peers, err := m.contacts(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range peers {
			if p.Node == req.Node && req.Action == "pair_invite" {
				return nil, errors.New("remove the existing contact before pairing again")
			}
		}
		id, err := randomMeshSession()
		if err != nil {
			return nil, err
		}
		msg := mesh.PairMessage{Kind: mesh.PairProbe, From: radio.Node, To: req.Node, ID: id, Expires: time.Now().Add(5 * time.Minute).Unix()}
		p := &meshPairing{ID: pairID(msg), Node: req.Node, Name: m.pairName(ctx, req.Node), State: "checking", Expires: time.Unix(msg.Expires, 0)}
		if req.Action == "pair_invite" {
			p.Exchange, err = mesh.NewPairExchange(radio.Node, req.Node, id, msg.Expires, true)
			if err != nil {
				return nil, err
			}
			msg = p.Exchange.Commitment()
			p.State = "invitation_sent"
		}
		m.pairRate[req.Node] = time.Now()
		m.pairSeen[p.ID] = p.Expires
		m.pairings[p.ID] = p
		m.pairQueue(ctx, p, msg)
		return p, nil
	}
	p := m.pairings[req.ID]
	if p == nil || time.Now().After(p.Expires) {
		return nil, errors.New("pairing expired; start a new invitation")
	}
	switch req.Action {
	case "pair_accept":
		if !req.Confirm || p.State != "invitation_received" {
			return nil, errors.New("invitation unavailable")
		}
		x, err := mesh.NewPairExchange(radio.Node, p.Node, p.Invite.ID, p.Invite.Expires, false)
		if err != nil {
			return nil, err
		}
		if err = x.ReceiveCommitment(p.Invite); err != nil {
			return nil, err
		}
		p.Exchange = x
		p.State = "exchanging"
		m.pairQueue(ctx, p, x.Commitment())
	case "pair_confirm":
		if !req.Confirm || p.Exchange == nil || p.Code == "" || req.Code != p.Code || p.State == "verified" {
			return nil, errors.New("compare the current code on both devices before confirming")
		}
		msg, err := p.Exchange.Confirm()
		if err != nil {
			return nil, err
		}
		p.LocalConfirmed = true
		m.pairQueue(ctx, p, msg)
		m.finishPair(ctx, p)
	case "pair_cancel":
		// Discard provisional keys. Existing verified contacts require explicit removal.
		if p.State == "verified" {
			return nil, errors.New("remove the verified contact in contact permissions")
		}
		reject := p.Invite
		if p.Exchange != nil {
			reject = p.Exchange.Commitment()
		} else if p.Last.Kind != 0 {
			reject = p.Last
		}
		reject.Kind = mesh.PairReject
		reject.From = radio.Node
		reject.To = p.Node
		reject.Data = nil
		_ = m.pairSend(ctx, reject)
		delete(m.pairings, p.ID)
	default:
		return nil, errors.New("unknown pairing action")
	}
	return map[string]bool{"ok": true}, nil
}
func (m *meshService) finishPair(ctx context.Context, p *meshPairing) {
	p.LocalConfirmed = p.Exchange.LocalConfirmed
	p.RemoteConfirmed = p.Exchange.RemoteConfirmed
	key := p.Exchange.VerifiedKey()
	if len(key) == 0 {
		return
	}
	tag, err := m.db.Exec(ctx, `INSERT INTO los_mesh_peers(node,name,secret,paired,allow_relay) SELECT $1,$2,$3,true,false WHERE (SELECT count(*) FROM los_mesh_peers)<16 ON CONFLICT DO NOTHING`, p.Node, p.Name, key)
	if err != nil {
		return
	}
	if tag.RowsAffected() == 1 {
		p.State = "verified"
		p.Code = ""
		return
	}
	// Idempotent retries never replace an existing contact or its permissions.
	if p.State != "verified" {
		p.State = "contact_conflict"
		p.Code = ""
	}
}
func (m *meshService) pairReceive(ctx context.Context, wire mesh.RadioPacket, local uint32, known map[uint32]meshContact) {
	m.initPairing()
	msg, err := mesh.DecodePair(wire.Payload, wire.From, local, time.Now())
	if err != nil || wire.To != local {
		return
	}
	id := pairID(msg)
	p := m.pairings[id]
	if msg.Kind == mesh.PairProbe {
		if time.Since(m.pairRate[msg.From]) < 15*time.Second || len(m.pairRate) >= 256 {
			return
		}
		m.pairRate[msg.From] = time.Now()
		msg.From, msg.To = msg.To, msg.From
		msg.Kind = mesh.PairAvailable
		_ = m.pairSend(ctx, msg)
		return
	}
	if msg.Kind == mesh.PairInvite && p == nil {
		if _, ok := known[msg.From]; ok {
			return
		}
		if _, seen := m.pairSeen[id]; seen || len(m.pairSeen) >= 128 || len(m.pairings) >= 8 || len(m.pairRate) >= 256 || time.Since(m.pairRate[msg.From]) < 15*time.Second {
			return
		}
		for _, active := range m.pairings {
			if active.Node == msg.From && active.State != "available" {
				return
			}
		}
		m.pairRate[msg.From] = time.Now()
		m.pairSeen[id] = time.Unix(msg.Expires, 0)
		m.pairings[id] = &meshPairing{ID: id, Node: msg.From, Name: m.pairName(ctx, msg.From), State: "invitation_received", Expires: time.Unix(msg.Expires, 0), Invite: msg}
		return
	}
	if p == nil || p.Node != msg.From || p.Expires.Unix() != msg.Expires {
		return
	}
	if msg.Kind == mesh.PairAvailable && p.State == "checking" {
		p.State = "available"
		p.Last = mesh.PairMessage{}
		return
	}
	if msg.Kind == mesh.PairReject && p.State != "verified" {
		p.State = "declined"
		p.Code = ""
		p.Exchange = nil
		p.Last = mesh.PairMessage{}
		return
	}
	if p.Exchange == nil {
		return
	}
	switch msg.Kind {
	case mesh.PairAccept:
		if p.Exchange.ReceiveCommitment(msg) != nil {
			return
		}
		if p.Code == "" {
			p.State = "exchanging"
			out, e := p.Exchange.Reveal()
			if e == nil {
				m.pairQueue(ctx, p, out)
			}
		}
	case mesh.PairReveal:
		if p.Exchange.ReceiveReveal(msg) != nil {
			return
		}
		if p.State != "verified" && p.State != "contact_conflict" {
			p.Code = p.Exchange.SAS()
			p.State = "compare_code"
		}
		// Keep sending the reveal until the remote operator's confirmation arrives.
		if !p.LocalConfirmed {
			out, e := p.Exchange.Reveal()
			if e == nil && time.Since(p.Sent) > 4*time.Second {
				m.pairQueue(ctx, p, out)
			}
		}
	case mesh.PairConfirm:
		if p.Exchange.ReceiveConfirmation(msg) != nil {
			return
		}
		m.finishPair(ctx, p)
	}
}
