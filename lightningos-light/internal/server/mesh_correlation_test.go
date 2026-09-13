package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"github.com/btcsuite/btcd/wire"
	"lightningos-light/internal/mesh"
	"testing"
	"time"
)

func setupCorrelatedRequest(t *testing.T) (*meshService, *meshTestWallet, mesh.Packet, meshContact) {
	t.Helper()
	m, w := meshIntegrationService(t)
	ctx := context.Background()
	peer := meshContact{Node: 10, Paired: true, AllowRelay: true, Key: bytes.Repeat([]byte{7}, 32)}
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_settings SET mode='send'")
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(ctx, "INSERT INTO los_mesh_peers(node,name,secret,paired,allow_relay) VALUES(10,'test',$1,true,true)", peer.Key)
	if err != nil {
		t.Fatal(err)
	}
	m.capabilities = map[uint32]*meshCapability{10: {Until: time.Now().Add(time.Minute)}}
	result, err := m.paymentRequest(ctx, meshAPIRequest{Action: "request_invoice", Node: 10, Amount: 10})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]string)["id"]
	return m, w, m.outgoing[id].Packets[0], peer
}
func TestMeshCorrelatedInvoiceAndDuplicateReply(t *testing.T) {
	m, w, request, peer := setupCorrelatedRequest(t)
	ctx := context.Background()
	raw, err := mesh.EncodeReply(mesh.Invoice, request, []byte("lnbc10n1simulated"))
	if err != nil {
		t.Fatal(err)
	}
	packets, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, raw, time.Now())
	for _, p := range packets {
		m.receive(ctx, p, peer, "send")
	}
	var state, response string
	if err = m.db.QueryRow(ctx, "SELECT state,response_id FROM los_mesh_sessions WHERE id=$1", sessionID(request)).Scan(&state, &response); err != nil {
		t.Fatal(err)
	}
	if state != "answered" || response != sessionID(packets[0]) || len(m.pending) != 1 || w.paid != 0 {
		t.Fatal("reply failed or implicitly paid", state, response)
	}
	if m.outgoing[sessionID(request)] != nil {
		t.Fatal("answered request still being retried")
	}
	for _, p := range packets {
		m.receive(ctx, p, peer, "send")
	}
	if len(m.pending) != 1 || w.paid != 0 {
		t.Fatal("duplicate changed pending/payment")
	}
	duplicate, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, raw, time.Now())
	for _, p := range duplicate {
		m.receive(ctx, p, peer, "send")
	}
	if len(m.pending) != 1 || w.paid != 0 {
		t.Fatal("new session bypassed response binding")
	}
	// A received status cannot advance the parent unless the child update passed.
	m.updateRequestResult(ctx, response, "paid")
	if err = m.db.QueryRow(ctx, "SELECT state FROM los_mesh_sessions WHERE id=$1", sessionID(request)).Scan(&state); err != nil || state != "answered" {
		t.Fatal("uncommitted result advanced request")
	}
	// Actual committed result updates the explicitly linked parent only.
	_, err = m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='paid' WHERE id=$1", response)
	if err != nil {
		t.Fatal(err)
	}
	m.updateRequestResult(ctx, response, "paid")
	if err = m.db.QueryRow(ctx, "SELECT state FROM los_mesh_sessions WHERE id=$1", sessionID(request)).Scan(&state); err != nil || state != "paid" {
		t.Fatal("confirmed result not linked")
	}
}

func TestMeshCorrelatedOnchainRelayAndReplay(t *testing.T) {
	m, w, _, peer := setupCorrelatedRequest(t)
	ctx := context.Background()
	tx := wire.NewMsgTx(2)
	var hash [32]byte
	hash[0] = 1
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: hash}, []byte{1, 1}, nil))
	script := append([]byte{0, 20}, bytes.Repeat([]byte{3}, 20)...)
	tx.AddTxOut(wire.NewTxOut(100, script))
	var buf bytes.Buffer
	_ = tx.Serialize(&buf)
	raw := buf.Bytes()
	preview, err := mesh.ValidateTransaction(raw)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.paymentRequest(ctx, meshAPIRequest{Action: "request", Node: 10, Amount: 100, Address: preview.Outputs[0].Address})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]string)["id"]
	request := m.outgoing[id].Packets[0]
	wrapped, err := mesh.EncodeReply(mesh.Transaction, request, raw)
	if err != nil {
		t.Fatal(err)
	}
	packets, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, wrapped, time.Now())
	for _, p := range packets {
		m.receive(ctx, p, peer, "both")
	}
	var state, txid string
	if err = m.db.QueryRow(ctx, "SELECT state,txid FROM los_mesh_sessions WHERE id=$1", id).Scan(&state, &txid); err != nil {
		t.Fatal(err)
	}
	if state != "published" || txid != preview.TXID || w.published != 1 {
		t.Fatal("publication not linked", state, txid, w.published)
	}
	for _, p := range packets {
		m.receive(ctx, p, peer, "both")
	}
	// Even another envelope session cannot publish the same request twice.
	duplicate, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, wrapped, time.Now())
	for _, p := range duplicate {
		m.receive(ctx, p, peer, "both")
	}
	if w.published != 1 || w.paid != 0 || w.signed != 0 {
		t.Fatal("duplicate caused financial operation")
	}
}

func TestMeshCapabilityRequiresMatchingAuthenticatedPeer(t *testing.T) {
	m := &meshService{capabilities: map[uint32]*meshCapability{}}
	var session [16]byte
	session[0] = 1
	m.capabilities[10] = &meshCapability{Session: session, Probed: time.Now()}
	p := mesh.Packet{Kind: mesh.Capabilities, From: 10, To: 20, Session: session, Payload: []byte{1, 1}}
	m.capabilityReceive(context.Background(), p, meshContact{Node: 10, Paired: false})
	if m.supportsReplies(10) {
		t.Fatal("unpaired capabilities trusted")
	}
	p.Session[0] = 2
	m.capabilityReceive(context.Background(), p, meshContact{Node: 10, Paired: true})
	if m.supportsReplies(10) {
		t.Fatal("unsolicited capability reply trusted")
	}
	p.Session = session
	m.capabilityReceive(context.Background(), p, meshContact{Node: 10, Paired: true})
	if !m.supportsReplies(10) {
		t.Fatal("negotiation did not complete")
	}
	m.capabilities[10].Until = time.Now().Add(-time.Second)
	if m.supportsReplies(10) {
		t.Fatal("expired capability trusted")
	}
}

func TestMeshRestartPreservesUncertainLinkedPayment(t *testing.T) {
	m, _, request, _ := setupCorrelatedRequest(t)
	ctx := context.Background()
	id := sessionID(request)
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='answered',response_id='child' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(ctx, `INSERT INTO los_mesh_sessions(id,peer,direction,state,expires,request_id) VALUES('child',10,'in','paying',now()+interval '1 minute',$1)`, id)
	if err != nil {
		t.Fatal(err)
	}
	boot, cancel := context.WithCancel(ctx)
	s := &Server{db: m.db, auth: m.server.auth, shutdownCtx: boot}
	restarted, err := s.meshService()
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err = m.db.QueryRow(ctx, "SELECT state FROM los_mesh_sessions WHERE id=$1", id).Scan(&state); err != nil || state != "payment_unknown" {
		t.Fatal("restart lost uncertainty", state, err)
	}
	if restarted.canRetry("child") || restarted.canRetry(id) {
		t.Fatal("restart offered financial retry")
	}
}
func TestMeshReplyRejectsWrongIdentityAndAmount(t *testing.T) {
	m, w, request, peer := setupCorrelatedRequest(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*mesh.Packet)
	}{
		{"hash", func(p *mesh.Packet) { p.Hash[0] ^= 1 }},
		{"session", func(p *mesh.Packet) { p.Session[0] ^= 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := request
			tc.mutate(&q)
			raw, _ := mesh.EncodeReply(mesh.Invoice, q, []byte("lnbc10n1simulated"))
			packets, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, raw, time.Now())
			for _, p := range packets {
				m.receive(ctx, p, peer, "send")
			}
		})
	}
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_sessions SET request_amount=11 WHERE id=$1", sessionID(request))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := mesh.EncodeReply(mesh.Invoice, request, []byte("lnbc10n1simulated"))
	packets, _ := mesh.Fragment(10, 20, mesh.CorrelatedReply, raw, time.Now())
	for _, p := range packets {
		m.receive(ctx, p, peer, "send")
	}
	if len(m.pending) != 0 || w.paid != 0 || w.published != 0 {
		t.Fatal("unmatched reply accepted")
	}
}
func TestMeshLegacyRequestAndSingleLocalResponse(t *testing.T) {
	m, w, _, peer := setupCorrelatedRequest(t)
	ctx := context.Background()
	packets, _ := mesh.Fragment(10, 20, mesh.PaymentRequest, []byte(`{"kind":"invoice_request","amount_sat":10}`), time.Now())
	for _, p := range packets {
		m.receive(ctx, p, peer, "send")
	}
	req := meshAPIRequest{RequestID: sessionID(packets[0]), Node: 10, Amount: 10, Invoice: "lnbc10n1simulated"}
	result, err := m.invoice(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]string)["id"]
	if m.outgoing[id].Packets[0].Kind != mesh.Invoice || len(m.pending) != 0 {
		t.Fatal("legacy reply compatibility/pending removal failed")
	}
	if _, err = m.invoice(ctx, req); err == nil {
		t.Fatal("request answered twice")
	}
	if w.paid != 0 || w.signed != 0 {
		t.Fatal("reply spent funds")
	}
}
func TestMeshRetryReusesOriginalSessionWithoutWalletCalls(t *testing.T) {
	m, w, request, _ := setupCorrelatedRequest(t)
	ctx := context.Background()
	id := sessionID(request)
	o := m.outgoing[id]
	o.Paused = true
	o.Attempts = 3
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='incomplete' WHERE id=$1", id)
	if err != nil {
		t.Fatal(err)
	}
	before := hex.EncodeToString(o.Packets[0].Hash[:])
	if err = m.retry(ctx, id); err != nil {
		t.Fatal(err)
	}
	if o.Paused || o.Resumes != 1 || before != hex.EncodeToString(o.Packets[0].Hash[:]) || o.Packets[0].Session != request.Session || w.paid != 0 || w.signed != 0 || w.published != 0 {
		t.Fatal("retry recreated financial operation")
	}
	o.Paused = true
	o.Resumes = 2
	if m.canRetry(id) {
		t.Fatal("retry bound ignored")
	}
	delete(m.outgoing, id)
	if m.canRetry(id) {
		t.Fatal("retry offered after losing original payload")
	}
}
