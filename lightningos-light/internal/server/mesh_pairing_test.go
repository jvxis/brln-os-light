package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"lightningos-light/internal/mesh"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestMeshGuidedPairingBothOperatorsAndDefaultPermissions(t *testing.T) {
	a, _ := meshIntegrationService(t)
	b, _ := meshIntegrationService(t)
	ctx := context.Background()
	queue := []mesh.RadioPacket{}
	setup := func(m *meshService, local uint32) {
		m.client = &http.Client{Transport: meshRoundTrip(func(r *http.Request) (*http.Response, error) {
			body := "{}"
			switch r.URL.Path {
			case "/status":
				body = fmt.Sprintf(`{"state":"running","node":%d}`, local)
			case "/nodes":
				body = `[]`
			case "/packets":
				var p mesh.RadioPacket
				_ = json.NewDecoder(r.Body).Decode(&p)
				p.From = local
				queue = append(queue, p)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
	}
	setup(a, 1)
	setup(b, 2)
	pump := func() {
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			if p.To == 1 {
				a.pairReceive(ctx, p, 1, map[uint32]meshContact{})
			} else {
				b.pairReceive(ctx, p, 2, map[uint32]meshContact{})
			}
		}
	}
	result, err := a.pairAction(ctx, meshAPIRequest{Action: "pair_invite", Node: 2, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(*meshPairing).ID
	pump()
	if b.pairings[id].State != "invitation_received" || b.pairings[id].Exchange != nil {
		t.Fatal("invitation automatically accepted")
	}
	if _, err = b.pairAction(ctx, meshAPIRequest{Action: "pair_accept", ID: id, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	b.pairings[id].Sent = time.Time{}
	pump()
	if a.pairings[id].Code == "" || a.pairings[id].Code != b.pairings[id].Code {
		t.Fatal("code mismatch")
	}
	if _, err = a.pairAction(ctx, meshAPIRequest{Action: "pair_confirm", ID: id, Code: "wrong", Confirm: true}); err == nil {
		t.Fatal("accepted stale code")
	}
	if _, err = a.pairAction(ctx, meshAPIRequest{Action: "pair_confirm", ID: id, Code: a.pairings[id].Code, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	pump()
	peers, _ := a.contacts(ctx)
	if len(peers) != 0 {
		t.Fatal("one-sided confirmation trusted contact")
	}
	if _, err = b.pairAction(ctx, meshAPIRequest{Action: "pair_confirm", ID: id, Code: b.pairings[id].Code, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	pump()
	peers, _ = a.contacts(ctx)
	if len(peers) != 2 {
		t.Fatal("both endpoints not saved", len(peers))
	}
	for _, p := range peers {
		if !p.Paired || p.AllowRelay {
			t.Fatal("incorrect default permissions")
		}
	}
	if a.pairings[id].State != "verified" || b.pairings[id].State != "verified" {
		t.Fatal("verification incomplete")
	}
}

func TestMeshPairingFloodAndCancellation(t *testing.T) {
	m, _ := meshIntegrationService(t)
	ctx := context.Background()
	for i := byte(1); i < 30; i++ {
		msg := mesh.PairMessage{Kind: mesh.PairInvite, From: uint32(i), To: 100, ID: [16]byte{i}, Expires: time.Now().Add(time.Minute).Unix(), Data: make([]byte, 32)}
		m.pairReceive(ctx, mesh.RadioPacket{From: msg.From, To: 100, Payload: msg.Encode()}, 100, map[uint32]meshContact{})
	}
	if len(m.pairings) > 8 || len(m.pairSeen) > 128 {
		t.Fatal("unbounded pairing invitations")
	}
	for id, p := range m.pairings {
		delete(m.pairings, id)
		m.pairRate[p.Node] = time.Time{}
		m.pairReceive(ctx, mesh.RadioPacket{From: p.Node, To: 100, Payload: p.Invite.Encode()}, 100, map[uint32]meshContact{})
		if m.pairings[id] != nil {
			t.Fatal("cancelled invitation replayed")
		}
	}
}
