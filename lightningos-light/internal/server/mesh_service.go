package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/mesh"
	"net"
	"net/http"
	"os/user"
	"strconv"
	"sync"
	"time"
)

type meshContact struct {
	Node           uint32    `json:"node"`
	Name           string    `json:"name"`
	Paired         bool      `json:"paired"`
	AllowRelay     bool      `json:"allow_relay"`
	Fingerprint    string    `json:"fingerprint"`
	Key            []byte    `json:"-"`
	Challenge      string    `json:"-"`
	ChallengeUntil time.Time `json:"-"`
}
type meshHistory struct {
	ID        string    `json:"id"`
	Peer      uint32    `json:"peer"`
	Direction string    `json:"direction"`
	State     string    `json:"state"`
	TXID      string    `json:"txid"`
	Received  int       `json:"received"`
	Total     int       `json:"total"`
	Created   time.Time `json:"created"`
}
type meshOutbound struct {
	Packets  []mesh.Packet
	Next     int
	Attempts int
	Sent     time.Time
}
type meshWallet interface {
	MeshMainnetReady(context.Context) error
	PublishTransaction(context.Context, string, string) error
	FundMeshTransaction(context.Context, string, int64, int64) (*lndclient.MeshFundedTransaction, error)
	FinalizeMeshTransaction(context.Context, *lndclient.MeshFundedTransaction) ([]byte, error)
	ReleaseMeshTransaction(context.Context, *lndclient.MeshFundedTransaction)
	CreateInvoice(context.Context, int64, string, int64, *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error)
	DecodeInvoice(context.Context, string) (lndclient.DecodedInvoice, error)
	PayMeshInvoice(context.Context, string, int64) error
}

type meshService struct {
	wallet      meshWallet
	mu          sync.Mutex
	db          *pgxpool.Pool
	client      *http.Client
	server      *Server
	incoming    map[string]*mesh.Assembly
	outgoing    map[string]*meshOutbound
	lastControl map[uint32]time.Time
	pending     map[string]*meshPending
	proposals   map[string]*meshProposal
	leased      map[string]*meshProposal
}

func (s *Server) meshService() (*meshService, error) {
	s.meshMu.Lock()
	defer s.meshMu.Unlock()
	if s.mesh != nil {
		return s.mesh, nil
	}
	if s.db == nil {
		return nil, errors.New("LOS Mesh database unavailable")
	}
	m := &meshService{db: s.db, server: s, wallet: s.lnd, incoming: map[string]*mesh.Assembly{}, outgoing: map[string]*meshOutbound{}, lastControl: map[uint32]time.Time{}, pending: map[string]*meshPending{}, proposals: map[string]*meshProposal{}, leased: map[string]*meshProposal{}}
	m.client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		account, err := user.Lookup("losmesh")
		if err != nil {
			return nil, errors.New("radio bridge unavailable")
		}
		want, err := strconv.ParseUint(account.Uid, 10, 32)
		if err != nil {
			return nil, err
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", mesh.SocketPath)
		if err != nil {
			return nil, err
		}
		uid, err := mesh.PeerUID(conn)
		if err != nil || uid != uint32(want) {
			conn.Close()
			return nil, errors.New("radio bridge identity mismatch")
		}
		return conn, nil
	}}}
	ctx, cancel := context.WithTimeout(s.shutdownContext(), 5*time.Second)
	defer cancel()
	_, err := m.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS los_mesh_settings (id boolean PRIMARY KEY DEFAULT true CHECK(id), mode text NOT NULL DEFAULT 'send');
INSERT INTO los_mesh_settings(id) VALUES(true) ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS los_mesh_peers (node bigint PRIMARY KEY, name text NOT NULL, secret bytea NOT NULL, paired boolean NOT NULL DEFAULT false, allow_relay boolean NOT NULL DEFAULT false, challenge text NOT NULL DEFAULT '', challenge_until timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS los_mesh_sessions (id text PRIMARY KEY, peer bigint NOT NULL, direction text NOT NULL, state text NOT NULL, txid text NOT NULL DEFAULT '', received integer NOT NULL DEFAULT 0, total integer NOT NULL DEFAULT 0, hash text NOT NULL DEFAULT '', expires timestamptz NOT NULL, created timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS los_mesh_publications (txid text PRIMARY KEY, state text NOT NULL, created timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS los_mesh_payments (hash text PRIMARY KEY,state text NOT NULL,created timestamptz NOT NULL DEFAULT now());
UPDATE los_mesh_payments SET state='payment_unknown' WHERE state='paying';
UPDATE los_mesh_sessions SET state='interrupted' WHERE state IN ('receiving','sending','awaiting_result','awaiting_approval');
UPDATE los_mesh_publications SET state='publication_unknown' WHERE state='publishing';`)
	if err != nil {
		return nil, errors.New("LOS Mesh storage initialization failed")
	}
	s.mesh = m
	go m.run(s.shutdownContext())
	return m, nil
}

func (m *meshService) bridge(ctx context.Context, method, path string, body any, result any) error {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://mesh"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return errors.New("radio bridge unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return errors.New("radio unavailable or queue full")
	}
	if result != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 64000)).Decode(result)
	}
	return nil
}
func (m *meshService) contacts(ctx context.Context) ([]meshContact, error) {
	rows, err := m.db.Query(ctx, "SELECT node,name,secret,paired,allow_relay,challenge,challenge_until FROM los_mesh_peers ORDER BY name LIMIT 16")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []meshContact{}
	for rows.Next() {
		var p meshContact
		if err = rows.Scan(&p.Node, &p.Name, &p.Key, &p.Paired, &p.AllowRelay, &p.Challenge, &p.ChallengeUntil); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(p.Key)
		p.Fingerprint = hex.EncodeToString(sum[:8])
		out = append(out, p)
	}
	return out, rows.Err()
}
func (m *meshService) mode(ctx context.Context) (string, error) {
	var mode string
	err := m.db.QueryRow(ctx, "SELECT mode FROM los_mesh_settings WHERE id=true").Scan(&mode)
	return mode, err
}
func sessionID(p mesh.Packet) string { return fmt.Sprintf("%08x:%x", p.From, p.Session) }
func (m *meshService) send(ctx context.Context, p mesh.Packet, key []byte) error {
	raw, err := p.Seal(key)
	if err != nil {
		return err
	}
	return m.bridge(ctx, "POST", "/packets", mesh.RadioPacket{To: p.To, Payload: raw}, nil)
}
func reply(p mesh.Packet, kind byte, payload []byte) mesh.Packet {
	return mesh.Packet{Kind: kind, From: p.To, To: p.From, Session: p.Session, Index: p.Index, Hash: p.Hash, Expires: p.Expires, Payload: payload}
}

func (m *meshService) run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycle, cancel := context.WithTimeout(ctx, 25*time.Second)
			m.mu.Lock()
			m.tick(cycle)
			m.mu.Unlock()
			cancel()
		}
	}
}
func (m *meshService) tick(ctx context.Context) {
	now := time.Now()
	for id, p := range m.proposals {
		if now.After(p.Expires) {
			m.wallet.ReleaseMeshTransaction(ctx, p.Funded)
			delete(m.proposals, id)
		}
	}
	for id, p := range m.leased {
		if now.After(p.Expires) {
			m.wallet.ReleaseMeshTransaction(ctx, p.Funded)
			delete(m.leased, id)
		}
	}
	for id, p := range m.pending {
		if now.After(p.Expires) {
			delete(m.pending, id)
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='expired' WHERE id=$1 AND state='awaiting_approval'", id)
		}
	}
	_, err := m.db.Exec(ctx, `DELETE FROM los_mesh_sessions WHERE created < now()-interval '30 days'; DELETE FROM los_mesh_payments WHERE created < now()-interval '30 days'; DELETE FROM los_mesh_publications WHERE created < now()-interval '30 days'; UPDATE los_mesh_sessions SET state='expired' WHERE expires<now() AND state IN ('receiving','sending','awaiting_result')`)
	if err != nil {
		return
	}
	for id, a := range m.incoming {
		if a.First.Expires <= now.Unix() {
			delete(m.incoming, id)
		}
	}
	for id, o := range m.outgoing {
		if o.Packets[0].Expires <= now.Unix() {
			delete(m.outgoing, id)
		}
	}
	var status mesh.RadioStatus
	if m.bridge(ctx, "GET", "/status", nil, &status) != nil || (status.State != "running" || status.Protocol != mesh.Version) {
		return
	}
	peers, err := m.contacts(ctx)
	if err != nil {
		return
	}
	known := map[uint32]meshContact{}
	for _, p := range peers {
		known[p.Node] = p
	}
	mode, err := m.mode(ctx)
	if err != nil {
		return
	}
	var received []mesh.RadioPacket
	if m.bridge(ctx, "GET", "/packets", nil, &received) != nil {
		return
	}
	for _, wire := range received {
		peer, ok := known[wire.From]
		if !ok {
			continue
		}
		p, err := mesh.Open(wire.Payload, peer.Key, wire.From, status.Node, now)
		if err != nil || wire.To != status.Node {
			continue
		}
		m.receive(ctx, p, peer, mode)
	}
	// One outstanding data packet globally per tick. The serial daemon also
	// enforces a two-second minimum interval across all outbound radio packets.
	for id, o := range m.outgoing {
		peer, ok := known[o.Packets[0].To]
		if !ok || !peer.Paired || mode == "relay" {
			delete(m.outgoing, id)
			continue
		}
		if now.Sub(o.Sent) < 12*time.Second {
			continue
		}
		if o.Attempts >= 3 {
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='incomplete' WHERE id=$1", id)
			delete(m.outgoing, id)
			continue
		}
		index := o.Next
		if index >= len(o.Packets) {
			index = len(o.Packets) - 1
		}
		if m.send(ctx, o.Packets[index], peer.Key) == nil {
			o.Sent = now
			o.Attempts++
		}
		break
	}
}

func (m *meshService) receive(ctx context.Context, p mesh.Packet, peer meshContact, mode string) {
	id := sessionID(p)
	switch p.Kind {
	case mesh.Hello:
		// Paired trust is provisioned locally; the handshake proves key possession.
		if time.Since(m.lastControl[p.From]) < 10*time.Second {
			return
		}
		m.lastControl[p.From] = time.Now()
		tag, e := m.db.Exec(ctx, `INSERT INTO los_mesh_sessions(id,peer,direction,state,expires) SELECT $1,$2,'in','handshake',$3 WHERE (SELECT count(*) FROM los_mesh_sessions)<1000 ON CONFLICT DO NOTHING`, id, p.From, time.Unix(p.Expires, 0))
		if e != nil || tag.RowsAffected() != 1 {
			return
		}
		if _, err := m.db.Exec(ctx, "UPDATE los_mesh_peers SET paired=true WHERE node=$1", p.From); err != nil {
			return
		}
		_ = m.send(ctx, reply(p, mesh.HelloAck, nil), peer.Key)
	case mesh.HelloAck:
		if peer.Challenge == hex.EncodeToString(p.Session[:]) && time.Now().Before(peer.ChallengeUntil) {
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_peers SET paired=true,challenge='' WHERE node=$1", p.From)
		}
	case mesh.ChunkAck, mesh.Result:
		outID := fmt.Sprintf("%08x:%x", p.To, p.Session)
		o := m.outgoing[outID]
		if o == nil && p.Kind == mesh.Result && peer.Paired {
			state := string(p.Payload)
			if state == "paid" || state == "payment_unknown" {
				_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state=$4 WHERE id=$1 AND peer=$2 AND hash=$3 AND direction='out' AND state='awaiting_approval'", outID, p.From, hex.EncodeToString(p.Hash[:]), state)
			}
			return
		}
		if o == nil || p.From != o.Packets[0].To || p.Hash != o.Packets[0].Hash {
			return
		}
		if p.Kind == mesh.Result {
			state := string(p.Payload)
			if state != "published" && state != "rejected" && state != "publication_unknown" && state != "relay_disabled" && state != "awaiting_approval" && state != "paid" && state != "payment_unknown" {
				return
			}
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state=$2 WHERE id=$1", outID, state)
			delete(m.outgoing, outID)
		} else if int(p.Index) == o.Next {
			o.Next++
			o.Attempts = 0
			o.Sent = time.Time{}
			state := "sending"
			if o.Next == len(o.Packets) {
				state = "awaiting_result"
			}
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET received=$2,state=$3 WHERE id=$1", outID, o.Next, state)
		}
	case mesh.Cancel:
		delete(m.incoming, id)
		delete(m.pending, id)
		_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='cancelled' WHERE id=$1 AND state IN ('receiving','awaiting_approval')", id)
	case mesh.Transaction, mesh.Invoice, mesh.PaymentRequest:
		if !peer.Paired || (p.Kind == mesh.Transaction && (!peer.AllowRelay || mode == "send")) {
			if time.Since(m.lastControl[p.From]) > 10*time.Second {
				m.lastControl[p.From] = time.Now()
				_ = m.send(ctx, reply(p, mesh.Result, []byte("relay_disabled")), peer.Key)
			}
			return
		}
		var state, hash string
		err := m.db.QueryRow(ctx, "SELECT state,hash FROM los_mesh_sessions WHERE id=$1", id).Scan(&state, &hash)
		if err == nil {
			if hash != hex.EncodeToString(p.Hash[:]) {
				return
			}
			if state != "receiving" {
				if (state == "published" || state == "rejected" || state == "publication_unknown" || state == "awaiting_approval" || state == "paid" || state == "payment_unknown") && time.Since(m.lastControl[p.From]) > 4*time.Second {
					m.lastControl[p.From] = time.Now()
					_ = m.send(ctx, reply(p, mesh.Result, []byte(state)), peer.Key)
				}
				return
			}
		} else {
			if !errors.Is(err, pgx.ErrNoRows) {
				return
			}
			if len(m.incoming) >= 8 {
				return
			}
			// Durable ledger bounds both replay memory and per-peer transaction rate.
			tag, e := m.db.Exec(ctx, `INSERT INTO los_mesh_sessions(id,peer,direction,state,total,hash,expires) SELECT $1,$2,'in','receiving',$3,$4,$5 WHERE (SELECT count(*) FROM los_mesh_sessions)<1000 AND (SELECT count(*) FROM los_mesh_sessions WHERE peer=$2 AND created>now()-interval '1 hour')<8 ON CONFLICT DO NOTHING`, id, p.From, p.Total, hex.EncodeToString(p.Hash[:]), time.Unix(p.Expires, 0))
			if e != nil || tag.RowsAffected() != 1 {
				return
			}
		}
		a := m.incoming[id]
		if a == nil {
			if len(m.incoming) >= 8 {
				return
			}
			a = &mesh.Assembly{}
			m.incoming[id] = a
		}
		content, err := a.Add(p)
		if err != nil {
			delete(m.incoming, id)
			_, _ = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='rejected' WHERE id=$1", id)
			return
		}
		_, err = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET received=$2 WHERE id=$1", id, len(a.Parts))
		if err != nil {
			return
		}
		_ = m.send(ctx, reply(p, mesh.ChunkAck, nil), peer.Key)
		if content == nil {
			return
		}
		delete(m.incoming, id)
		txid := ""
		if p.Kind == mesh.Transaction {
			state = m.publish(ctx, content)
			preview, _ := mesh.ValidateTransaction(content)
			txid = preview.TXID
		} else {
			state = m.acceptRequest(ctx, p, content)
		}
		if _, err = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state=$2,txid=$3 WHERE id=$1", id, state, txid); err == nil {
			_ = m.send(ctx, reply(p, mesh.Result, []byte(state)), peer.Key)
		}
	}
}

func (m *meshService) publish(ctx context.Context, raw []byte) string {
	preview, err := mesh.ValidateTransaction(raw)
	if err != nil {
		return "rejected"
	}
	if m.server.auth == nil || !m.server.auth.Enabled() || m.wallet == nil || m.wallet.MeshMainnetReady(ctx) != nil {
		return "rejected"
	}
	// Commit the durable idempotency record before calling LND. An uncertain
	// response is never automatically retried, even after Manager restart.
	tag, err := m.db.Exec(ctx, `INSERT INTO los_mesh_publications(txid,state) SELECT $1,'publishing' WHERE (SELECT count(*) FROM los_mesh_publications)<1000 ON CONFLICT DO NOTHING`, preview.TXID)
	if err != nil {
		return "publication_unknown"
	}
	if tag.RowsAffected() == 0 {
		var state string
		if m.db.QueryRow(ctx, "SELECT state FROM los_mesh_publications WHERE txid=$1", preview.TXID).Scan(&state) != nil {
			return "rejected"
		}
		if state == "publishing" {
			return "publication_unknown"
		}
		return state
	}
	state := "published"
	if m.wallet.PublishTransaction(ctx, hex.EncodeToString(raw), "LOS Mesh authorized relay") != nil {
		state = "publication_unknown"
	}
	if _, err = m.db.Exec(ctx, "UPDATE los_mesh_publications SET state=$2 WHERE txid=$1", preview.TXID, state); err != nil {
		return "publication_unknown"
	}
	return state
}

func randomMeshSession() ([16]byte, error) {
	var id [16]byte
	_, err := rand.Read(id[:])
	return id, err
}
