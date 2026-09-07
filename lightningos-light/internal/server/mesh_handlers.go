package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"io"
	"lightningos-light/internal/lndclient"
	"lightningos-light/internal/mesh"
	"lightningos-light/internal/privileged"
	"lightningos-light/internal/system"
	"net/http"
	"strings"
	"time"
)

type meshPending struct {
	Packet      mesh.Packet `json:"-"`
	Invoice     string      `json:"-"`
	ID          string      `json:"id"`
	Peer        uint32      `json:"peer"`
	Kind        string      `json:"kind"`
	Address     string      `json:"address,omitempty"`
	Amount      int64       `json:"amount_sat"`
	Hash        string      `json:"payment_hash,omitempty"`
	Destination string      `json:"destination,omitempty"`
	Memo        string      `json:"memo,omitempty"`
	Expires     time.Time   `json:"expires"`
}
type meshProposal struct {
	Owner   string
	Funded  *lndclient.MeshFundedTransaction
	Raw     []byte
	Peer    uint32
	Expires time.Time
	Session string
}
type meshAPIRequest struct {
	Owner      string `json:"-"`
	Action     string `json:"action"`
	Device     string `json:"device"`
	Mode       string `json:"mode"`
	Node       uint32 `json:"node"`
	Name       string `json:"name"`
	Key        string `json:"key"`
	AllowRelay bool   `json:"allow_relay"`
	ID         string `json:"id"`
	Raw        string `json:"raw_tx"`
	Address    string `json:"address"`
	Amount     int64  `json:"amount_sat"`
	Rate       int64  `json:"sat_per_vbyte"`
	Invoice    string `json:"invoice"`
	Memo       string `json:"memo"`
	MaxFee     int64  `json:"max_fee_sat"`
	Confirm    bool   `json:"confirm"`
	Password   string `json:"confirm_password"`
}

func (s *Server) meshAccess(w http.ResponseWriter, r *http.Request) (*meshService, bool) {
	if s.auth == nil || !s.auth.Enabled() {
		writeError(w, 403, "enable login protection before using LOS Mesh")
		return nil, false
	}
	m, err := s.meshService()
	if err != nil {
		writeError(w, 503, err.Error())
		return nil, false
	}
	return m, true
}
func (s *Server) handleMeshStatus(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meshAccess(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	raw, err := system.MeshControlWithBroker(ctx, "status", "")
	if err != nil {
		writeError(w, 503, err.Error())
		return
	}
	var app privileged.MeshState
	if json.Unmarshal([]byte(raw), &app) != nil {
		writeError(w, 503, "invalid bridge status")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	peers, err := m.contacts(ctx)
	if err != nil {
		writeError(w, 503, "contact storage unavailable")
		return
	}
	mode, err := m.mode(ctx)
	if err != nil {
		writeError(w, 503, "configuration unavailable")
		return
	}
	radio := mesh.RadioStatus{State: app.Status}
	if app.Installed {
		if m.bridge(ctx, "GET", "/status", nil, &radio) != nil && app.Status == "running" {
			radio.State = "communication_error"
		}
	}
	if radio.State == "running" && radio.Protocol != mesh.Version {
		radio.State = "upgrade_required"
	}
	if radio.State == "running" {
		paired := false
		for _, p := range peers {
			paired = paired || p.Paired
		}
		if !paired {
			radio.State = "awaiting_pairing"
		}
	}
	rows, err := m.db.Query(ctx, "SELECT id,peer,direction,state,txid,received,total,created FROM los_mesh_sessions WHERE state<>'handshake' ORDER BY created DESC LIMIT 100")
	if err != nil {
		writeError(w, 503, "history unavailable")
		return
	}
	defer rows.Close()
	history := []meshHistory{}
	for rows.Next() {
		var h meshHistory
		if rows.Scan(&h.ID, &h.Peer, &h.Direction, &h.State, &h.TXID, &h.Received, &h.Total, &h.Created) != nil {
			writeError(w, 503, "history unavailable")
			return
		}
		history = append(history, h)
	}
	pending := []*meshPending{}
	for _, p := range m.pending {
		pending = append(pending, p)
	}
	writeJSON(w, 200, map[string]any{"app": app, "radio": radio, "mode": mode, "peers": peers, "history": history, "pending": pending, "protocol_version": mesh.Version})
}

func (s *Server) handleMeshAction(w http.ResponseWriter, r *http.Request) {
	m, ok := s.meshAccess(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 40000)
	var req meshAPIRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		writeError(w, 400, "invalid request")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid request")
		return
	}
	session, authenticated := authSessionFromContext(r.Context())
	if !authenticated {
		writeError(w, 401, "authentication required")
		return
	}
	req.Owner = session.ID
	// Policy changes and spending actions require fresh LOS Mesh reauthentication.
	if req.Action != "preview" && req.Action != "invoice" && req.Action != "request" && req.Action != "request_invoice" && req.Action != "cancel" {
		if !s.requireSensitiveReauth(w, r, authScopeMesh, req.Password, "mesh_reauth_required", "confirm your password for LOS Mesh") {
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := r.Context()
	var result any = map[string]bool{"ok": true}
	var err error
	switch req.Action {
	case "install":
		if !req.Confirm || (req.Mode != "send" && req.Mode != "relay" && req.Mode != "both") {
			err = errors.New("choose send, relay or both")
			break
		}
		_, err = system.MeshControlWithBroker(ctx, "install", req.Device)
		if err == nil {
			connected := false
			for deadline := time.Now().Add(22 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; {
				var radio mesh.RadioStatus
				if m.bridge(ctx, "GET", "/status", nil, &radio) == nil && radio.State == "running" && radio.Node != 0 {
					connected = true
					break
				}
				select {
				case <-ctx.Done():
				case <-time.After(500 * time.Millisecond):
				}
			}
			if !connected {
				cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, _ = system.MeshControlWithBroker(cleanup, "remove", "")
				cancel()
				err = errors.New("USB device did not respond to the Meshtastic serial API; installation rolled back")
			} else {
				_, err = m.db.Exec(ctx, "UPDATE los_mesh_settings SET mode=$1", req.Mode)
			}
			s.invalidateAppListCache()
		}
	case "mode":
		if !req.Confirm || (req.Mode != "send" && req.Mode != "relay" && req.Mode != "both") {
			err = errors.New("confirm the selected mode")
			break
		}
		_, err = m.db.Exec(ctx, "UPDATE los_mesh_settings SET mode=$1", req.Mode)
	case "peer":
		err = m.savePeer(ctx, req)
	case "remove_peer":
		if !req.Confirm {
			err = errors.New("confirm contact removal")
			break
		}
		_, err = m.db.Exec(ctx, "DELETE FROM los_mesh_peers WHERE node=$1", req.Node)
		for id, p := range m.pending {
			if p.Peer == req.Node {
				delete(m.pending, id)
			}
		}
		for id, a := range m.incoming {
			if a.First.From == req.Node {
				delete(m.incoming, id)
			}
		}
	case "preview":
		result, err = m.preview(ctx, req)
	case "send":
		if !req.Confirm {
			err = errors.New("explicit transmission approval required")
			break
		}
		result, err = m.approveSend(ctx, req)
	case "invoice":
		result, err = m.invoice(ctx, req)
	case "request", "request_invoice":
		result, err = m.paymentRequest(ctx, req)
	case "pay":
		if !req.Confirm {
			err = errors.New("explicit payment approval required")
			break
		}
		result, err = m.pay(ctx, req)
	case "cancel":
		err = m.cancel(ctx, req.ID, req.Owner)
	default:
		err = errors.New("unknown LOS Mesh action")
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.recordAuditEventAsync(r, "mesh."+req.Action, "los-mesh", nil)
	writeJSON(w, 200, result)
}

func (m *meshService) radioPeer(ctx context.Context, node uint32) (mesh.RadioStatus, meshContact, error) {
	var radio mesh.RadioStatus
	var peer meshContact
	if err := m.bridge(ctx, "GET", "/status", nil, &radio); err != nil || radio.State != "running" {
		return radio, peer, errors.New("USB radio unavailable")
	}
	peers, err := m.contacts(ctx)
	if err != nil {
		return radio, peer, errors.New("contacts unavailable")
	}
	for _, p := range peers {
		if p.Node == node && p.Paired {
			return radio, p, nil
		}
	}
	return radio, peer, errors.New("select a paired contact")
}
func (m *meshService) savePeer(ctx context.Context, req meshAPIRequest) error {
	key, err := hex.DecodeString(req.Key)
	if err != nil || len(key) != 32 || req.Node == 0 || req.Node == 0xffffffff || len(strings.TrimSpace(req.Name)) < 1 || len(req.Name) > 64 || !req.Confirm {
		return errors.New("provide a name, radio node ID, 32-byte shared key and pairing confirmation")
	}
	var radio mesh.RadioStatus
	if m.bridge(ctx, "GET", "/status", nil, &radio) != nil || radio.State != "running" {
		return errors.New("connect the USB radio before pairing")
	}
	if req.Node == radio.Node {
		return errors.New("cannot pair the local radio")
	}
	id, err := randomMeshSession()
	if err != nil {
		return err
	}
	expires := time.Now().Add(5 * time.Minute)
	tag, err := m.db.Exec(ctx, `INSERT INTO los_mesh_peers(node,name,secret,allow_relay,challenge,challenge_until) SELECT $1,$2,$3,$4,$5,$6 WHERE (SELECT count(*) FROM los_mesh_peers)<16 OR EXISTS(SELECT 1 FROM los_mesh_peers WHERE node=$1) ON CONFLICT(node) DO UPDATE SET name=excluded.name,secret=excluded.secret,paired=false,allow_relay=excluded.allow_relay,challenge=excluded.challenge,challenge_until=excluded.challenge_until`, req.Node, strings.TrimSpace(req.Name), key, req.AllowRelay, hex.EncodeToString(id[:]), expires)
	if err != nil {
		return errors.New("contact storage unavailable")
	}
	if tag.RowsAffected() != 1 {
		return errors.New("contact limit reached")
	}
	return m.send(ctx, mesh.Packet{Kind: mesh.Hello, From: radio.Node, To: req.Node, Session: id, Expires: expires.Unix()}, key)
}
func (m *meshService) queue(ctx context.Context, node uint32, kind byte, content []byte) (string, error) {
	mode, err := m.mode(ctx)
	if err != nil || mode == "relay" {
		return "", errors.New("enable send mode first")
	}
	radio, _, err := m.radioPeer(ctx, node)
	if err != nil {
		return "", err
	}
	if len(m.outgoing) >= 4 {
		return "", errors.New("outbound session limit reached")
	}
	packets, err := mesh.Fragment(radio.Node, node, kind, content, time.Now())
	if err != nil {
		return "", err
	}
	id := sessionID(packets[0])
	txid := ""
	if kind == mesh.Transaction {
		p, e := mesh.ValidateTransaction(content)
		if e != nil {
			return "", e
		}
		txid = p.TXID
	}
	tag, err := m.db.Exec(ctx, `INSERT INTO los_mesh_sessions(id,peer,direction,state,txid,total,hash,expires) SELECT $1,$2,'out','sending',$3,$4,$5,$6 WHERE (SELECT count(*) FROM los_mesh_sessions)<1000`, id, node, txid, len(packets), hex.EncodeToString(packets[0].Hash[:]), time.Unix(packets[0].Expires, 0))
	if err != nil || tag.RowsAffected() != 1 {
		return "", errors.New("session ledger full or unavailable")
	}
	m.outgoing[id] = &meshOutbound{Packets: packets}
	return id, nil
}
func (m *meshService) preview(ctx context.Context, req meshAPIRequest) (any, error) {
	if _, _, err := m.radioPeer(ctx, req.Node); err != nil {
		return nil, err
	}
	if len(m.proposals) >= 4 || len(m.leased) >= 8 {
		return nil, errors.New("cancel or wait for existing previews to expire")
	}
	var result any
	proposal := &meshProposal{Owner: req.Owner, Peer: req.Node, Expires: time.Now().Add(2 * time.Minute)}
	if req.Raw != "" {
		raw, err := hex.DecodeString(strings.TrimSpace(req.Raw))
		if err != nil {
			return nil, errors.New("invalid raw transaction")
		}
		p, err := mesh.ValidateTransaction(raw)
		if err != nil {
			return nil, err
		}
		proposal.Raw = raw
		result = p
	} else {
		funded, err := m.wallet.FundMeshTransaction(ctx, req.Address, req.Amount, req.Rate)
		if err != nil {
			return nil, errors.New("cannot fund transaction; check confirmed funds, mainnet address and fee rate")
		}
		proposal.Funded = funded
		result = funded.Preview
	}
	id, err := randomMeshSession()
	if err != nil {
		if proposal.Funded != nil {
			m.wallet.ReleaseMeshTransaction(ctx, proposal.Funded)
		}
		return nil, err
	}
	token := hex.EncodeToString(id[:])
	m.proposals[token] = proposal
	return map[string]any{"id": token, "expires": proposal.Expires, "preview": result}, nil
}
func (m *meshService) approveSend(ctx context.Context, req meshAPIRequest) (any, error) {
	p := m.proposals[req.ID]
	if p == nil || p.Owner == "" || p.Owner != req.Owner || time.Now().After(p.Expires) {
		return nil, errors.New("preview expired; review a new transaction")
	}
	if _, _, err := m.radioPeer(ctx, p.Peer); err != nil {
		return nil, err
	}
	if len(m.outgoing) >= 4 {
		return nil, errors.New("outbound session limit reached")
	}
	raw := p.Raw
	if p.Funded != nil {
		if err := m.wallet.MeshMainnetReady(ctx); err != nil {
			return nil, errors.New("synced mainnet wallet required")
		}
		var err error
		raw, err = m.wallet.FinalizeMeshTransaction(ctx, p.Funded)
		if err != nil {
			return nil, errors.New("transaction signing failed")
		}
	}
	id, err := m.queue(ctx, p.Peer, mesh.Transaction, raw)
	if err != nil {
		return nil, err
	}
	p.Raw = nil
	p.Session = id
	// Keep leases until the transfer expires, even after relay acceptance. A
	// signed transaction cannot be revoked by cancelling its radio transfer.
	p.Expires = time.Now().Add(20 * time.Minute)
	m.leased[id] = p
	delete(m.proposals, req.ID)
	return map[string]string{"id": id, "state": "sending"}, nil
}
func (m *meshService) invoice(ctx context.Context, req meshAPIRequest) (any, error) {
	if _, _, err := m.radioPeer(ctx, req.Node); err != nil {
		return nil, err
	}
	if len(req.Memo) > 120 {
		return nil, errors.New("memo too long")
	}
	invoice := strings.TrimSpace(req.Invoice)
	if invoice == "" {
		if req.Amount <= 0 || req.Amount > 100_000_000 {
			return nil, errors.New("invoice amount must be between 1 and 100000000 sats")
		}
		created, err := m.wallet.CreateInvoice(ctx, req.Amount, req.Memo, 1200, nil)
		if err != nil {
			return nil, errors.New("invoice creation failed")
		}
		invoice = created.PaymentRequest
	}
	if _, err := m.decodeInvoice(ctx, invoice); err != nil {
		return nil, err
	}
	id, err := m.queue(ctx, req.Node, mesh.Invoice, []byte(invoice))
	return map[string]string{"id": id}, err
}
func (m *meshService) decodeInvoice(ctx context.Context, invoice string) (lndclient.DecodedInvoice, error) {
	if len(invoice) < 6 || len(invoice) > 4096 || !strings.HasPrefix(strings.ToLower(invoice), "lnbc") || invoice[4] < '0' || invoice[4] > '9' {
		return lndclient.DecodedInvoice{}, errors.New("Bitcoin mainnet BOLT11 invoice required")
	}
	d, err := m.wallet.DecodeInvoice(ctx, invoice)
	if err != nil || d.AmountMsat <= 0 || d.AmountMsat%1000 != 0 || d.AmountSat > 100_000_000 || time.Now().Unix() >= d.Timestamp+d.Expiry {
		return d, errors.New("invoice invalid, expired or outside the supported amount limits")
	}
	return d, nil
}

type meshAddressRequest struct {
	Kind    string `json:"kind,omitempty"`
	Address string `json:"address"`
	Amount  int64  `json:"amount_sat"`
	Memo    string `json:"memo"`
}

func validateMeshAddress(p meshAddressRequest) error {
	if p.Kind == "invoice_request" {
		if p.Address != "" || p.Amount <= 0 || p.Amount > 100_000_000 || len(p.Memo) > 120 {
			return errors.New("invalid invoice request")
		}
		return nil
	}
	if p.Kind != "" && p.Kind != "onchain_request" {
		return errors.New("unsupported payment request kind")
	}
	address, err := btcutil.DecodeAddress(p.Address, &chaincfg.MainNetParams)
	if err != nil || !address.IsForNet(&chaincfg.MainNetParams) || p.Amount <= 0 || p.Amount > 21_000_000*100_000_000 || len(p.Memo) > 120 {
		return errors.New("invalid mainnet payment request")
	}
	return nil
}
func (m *meshService) paymentRequest(ctx context.Context, req meshAPIRequest) (any, error) {
	p := meshAddressRequest{Address: req.Address, Amount: req.Amount, Memo: req.Memo}
	if req.Action == "request_invoice" {
		p.Kind = "invoice_request"
	}
	if err := validateMeshAddress(p); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(p)
	id, err := m.queue(ctx, req.Node, mesh.PaymentRequest, raw)
	return map[string]string{"id": id}, err
}

func (m *meshService) acceptRequest(ctx context.Context, p mesh.Packet, raw []byte) string {
	if len(m.pending) >= 8 {
		return "rejected"
	}
	pending := &meshPending{Packet: p, ID: sessionID(p), Peer: p.From, Expires: time.Unix(p.Expires, 0)}
	pending.Packet.Payload = nil
	if p.Kind == mesh.Invoice {
		d, err := m.decodeInvoice(ctx, string(raw))
		if err != nil {
			return "rejected"
		}
		pending.Invoice = string(raw)
		pending.Kind = "invoice"
		pending.Amount = d.AmountSat
		pending.Hash = d.PaymentHash
		pending.Destination = d.Destination
		pending.Memo = d.Memo
		if len(pending.Memo) > 160 {
			pending.Memo = pending.Memo[:160]
		}
		expiry := time.Unix(d.Timestamp+d.Expiry, 0)
		if expiry.Before(pending.Expires) {
			pending.Expires = expiry
		}
	} else {
		var request meshAddressRequest
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var extra any
		if dec.Decode(&request) != nil || dec.Decode(&extra) != io.EOF || validateMeshAddress(request) != nil {
			return "rejected"
		}
		pending.Kind = "onchain_request"
		if request.Kind == "invoice_request" {
			pending.Kind = request.Kind
		}
		pending.Address = request.Address
		pending.Amount = request.Amount
		pending.Memo = request.Memo
	}
	m.pending[pending.ID] = pending
	return "awaiting_approval"
}
func (m *meshService) pay(ctx context.Context, req meshAPIRequest) (any, error) {
	p := m.pending[req.ID]
	if p == nil || p.Kind != "invoice" || time.Now().After(p.Expires) {
		return nil, errors.New("invoice request expired")
	}
	if req.Amount != p.Amount || req.MaxFee < 0 || req.MaxFee > 100000 {
		return nil, errors.New("review the invoice amount and fee limit")
	}
	_, peer, err := m.radioPeer(ctx, p.Peer)
	if err != nil {
		return nil, err
	}
	if _, err = m.decodeInvoice(ctx, p.Invoice); err != nil {
		return nil, err
	}
	if m.wallet.MeshMainnetReady(ctx) != nil {
		return nil, errors.New("mainnet wallet unavailable")
	}
	tag, err := m.db.Exec(ctx, `INSERT INTO los_mesh_payments(hash,state) SELECT $1,'paying' WHERE (SELECT count(*) FROM los_mesh_payments)<1000 ON CONFLICT DO NOTHING`, p.Hash)
	if err != nil || tag.RowsAffected() != 1 {
		return nil, errors.New("payment already attempted or ledger unavailable; consult wallet activity")
	}
	reservation, paymentHash, err := m.server.reserveInvoiceSpending(ctx, p.Invoice, p.Amount, req.MaxFee, "los-mesh")
	if err != nil {
		_, _ = m.db.Exec(ctx, "UPDATE los_mesh_payments SET state='rejected' WHERE hash=$1", p.Hash)
		return nil, errors.New("spending guard rejected this payment")
	}
	paymentCtx, cancel := context.WithTimeout(m.server.shutdownContext(), lndWalletPaymentTimeout)
	defer cancel()
	paymentErr := m.wallet.PayMeshInvoice(paymentCtx, p.Invoice, req.MaxFee)
	m.server.finishSpendingReservation(reservation, paymentHash, paymentErr)
	state := "paid"
	if paymentErr != nil {
		state = "payment_unknown"
	}
	_, _ = m.db.Exec(paymentCtx, "UPDATE los_mesh_payments SET state=$2 WHERE hash=$1", p.Hash, state)
	_, _ = m.db.Exec(paymentCtx, "UPDATE los_mesh_sessions SET state=$2 WHERE id=$1", req.ID, state)
	_ = m.send(paymentCtx, reply(p.Packet, mesh.Result, []byte(state)), peer.Key)
	delete(m.pending, req.ID)
	m.server.recordWalletActivity(p.Hash)
	return map[string]string{"state": state, "payment_hash": p.Hash}, nil
}
func (m *meshService) cancel(ctx context.Context, id, owner string) error {
	if p := m.proposals[id]; p != nil {
		if p.Owner != owner {
			return errors.New("preview belongs to another session")
		}
		m.wallet.ReleaseMeshTransaction(ctx, p.Funded)
		delete(m.proposals, id)
		return nil
	}
	if o := m.outgoing[id]; o != nil {
		_, peer, err := m.radioPeer(ctx, o.Packets[0].To)
		if err == nil {
			p := o.Packets[0]
			p.Kind = mesh.Cancel
			p.Size = 0
			p.Total = 0
			p.Payload = nil
			_ = m.send(ctx, p, peer.Key)
		}
		delete(m.outgoing, id)
	}
	delete(m.incoming, id)
	delete(m.pending, id)
	_, err := m.db.Exec(ctx, "UPDATE los_mesh_sessions SET state='cancelled' WHERE id=$1 AND state IN ('receiving','sending','awaiting_approval','awaiting_result')", id)
	return err
}
