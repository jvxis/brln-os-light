package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"lightningos-light/lnrpc"
)

type invoiceCursorClient struct {
	lnrpc.LightningClient
	page, live     []*lnrpc.Invoice
	confirmed      *lnrpc.Invoice
	lookupErr      error
	lookups, lists int
	subscribed     uint64
}

func (c *invoiceCursorClient) ListInvoices(_ context.Context, req *lnrpc.ListInvoiceRequest, _ ...grpc.CallOption) (*lnrpc.ListInvoiceResponse, error) {
	c.lists++
	if !req.Reversed || req.NumMaxInvoices != invoiceReconcileLimit {
		return nil, errors.New("unbounded reconciliation")
	}
	return &lnrpc.ListInvoiceResponse{Invoices: append([]*lnrpc.Invoice(nil), c.page...)}, nil
}

func (c *invoiceCursorClient) SubscribeInvoices(_ context.Context, req *lnrpc.InvoiceSubscription, _ ...grpc.CallOption) (lnrpc.Lightning_SubscribeInvoicesClient, error) {
	c.subscribed = req.SettleIndex
	return &invoiceCursorStream{invoices: c.live}, nil
}

func (c *invoiceCursorClient) LookupInvoice(context.Context, *lnrpc.PaymentHash, ...grpc.CallOption) (*lnrpc.Invoice, error) {
	c.lookups++
	return c.confirmed, c.lookupErr
}

type invoiceCursorStream struct {
	lnrpc.Lightning_SubscribeInvoicesClient
	invoices []*lnrpc.Invoice
}

func (s *invoiceCursorStream) Recv() (*lnrpc.Invoice, error) {
	if len(s.invoices) == 0 {
		return nil, io.EOF
	}
	invoice := s.invoices[0]
	s.invoices = s.invoices[1:]
	return invoice, nil
}

func cursorInvoice(index, add uint64, settled int64, hash byte) *lnrpc.Invoice {
	rhash := make([]byte, 32)
	rhash[0] = hash
	return &lnrpc.Invoice{State: lnrpc.Invoice_SETTLED, SettleIndex: index, AddIndex: add, SettleDate: settled, RHash: rhash}
}

func TestInvoiceCursorRecoveryAndRestart(t *testing.T) {
	old := cursorInvoice(31078, 40000, 1000, 1)
	first := cursorInvoice(1, 40001, 1100, 2)
	second := cursorInvoice(2, 39000, 1101, 3) // created before migration, settled afterwards
	durable := invoiceCheckpoint{Index: 31078}
	seen := map[string]bool{}
	mirrors, warnings := 0, 0
	process := func(_ context.Context, invoice *lnrpc.Invoice, historical bool) error {
		hash := hex.EncodeToString(invoice.RHash)
		if !seen[hash] && !historical {
			mirrors++
		}
		seen[hash] = true
		return nil
	}
	save := func(_ context.Context, c invoiceCheckpoint) error { durable = c; return nil }
	warn := func(string, ...any) { warnings++ }
	client := &invoiceCursorClient{page: []*lnrpc.Invoice{second, old, first}, live: []*lnrpc.Invoice{old, first, second}}
	err := followSettledInvoices(context.Background(), client, durable, save, process, warn)
	if !errors.Is(err, io.EOF) || durable.Index != 2 || durable.EpochAfter != 1000 || len(seen) != 2 || mirrors != 0 || warnings != 1 {
		t.Fatalf("err=%v checkpoint=%+v seen=%d mirrors=%d warnings=%d", err, durable, len(seen), mirrors, warnings)
	}
	// The persisted fence survives serialization and filters larger old indices.
	raw, _ := json.Marshal(durable)
	restored, err := decodeInvoiceCheckpoint(string(raw), 31078)
	if err != nil {
		t.Fatal(err)
	}
	client = &invoiceCursorClient{page: []*lnrpc.Invoice{old, first, second}, live: []*lnrpc.Invoice{old, first, second}}
	processed := 0
	err = followSettledInvoices(context.Background(), client, restored, save, func(context.Context, *lnrpc.Invoice, bool) error { processed++; return nil }, warn)
	if !errors.Is(err, io.EOF) || processed != 0 || client.subscribed != 2 {
		t.Fatalf("restart replayed: count=%d err=%v", processed, err)
	}
}

func TestInvoiceCursorMonotonicAndReplay(t *testing.T) {
	anchor := cursorInvoice(10, 20, 1000, 1)
	checkpoint := (invoiceCheckpoint{}).advance(anchor, false)
	client := &invoiceCursorClient{live: []*lnrpc.Invoice{anchor, cursorInvoice(9, 21, 999, 2), cursorInvoice(11, 19, 1000, 3), cursorInvoice(12, 22, 1001, 4)}}
	processed := 0
	err := followSettledInvoices(context.Background(), client, checkpoint,
		func(_ context.Context, c invoiceCheckpoint) error { checkpoint = c; return nil },
		func(_ context.Context, _ *lnrpc.Invoice, historical bool) error {
			if historical {
				t.Fatal("normal stream marked historical")
			}
			processed++
			return nil
		},
		func(string, ...any) { t.Fatal("normal replay must not warn/reset") })
	if !errors.Is(err, io.EOF) || checkpoint.Index != 12 || processed != 2 || client.lookups != 0 || client.lists != 1 {
		t.Fatalf("normal path changed: %+v count=%d err=%v", checkpoint, processed, err)
	}
}

func TestInvoiceCursorLiveRegressionRequiresConfirmation(t *testing.T) {
	for _, mode := range []string{"valid", "lookup failure", "mismatch", "same second"} {
		t.Run(mode, func(t *testing.T) {
			checkpoint := (invoiceCheckpoint{}).advance(cursorInvoice(31078, 40000, 1000, 1), false)
			invoice := cursorInvoice(1, 39999, 1001, 2)
			client := &invoiceCursorClient{live: []*lnrpc.Invoice{invoice}, confirmed: invoice}
			switch mode {
			case "lookup failure":
				client.lookupErr = errors.New("offline")
			case "mismatch":
				client.confirmed = cursorInvoice(1, 39999, 1001, 3)
			case "same second":
				invoice.SettleDate = 1000
			}
			processed := 0
			_ = followSettledInvoices(context.Background(), client, checkpoint,
				func(_ context.Context, c invoiceCheckpoint) error { checkpoint = c; return nil },
				func(_ context.Context, _ *lnrpc.Invoice, historical bool) error {
					if historical {
						t.Fatal("live event muted")
					}
					processed++
					return nil
				}, func(string, ...any) {})
			if mode == "valid" {
				if checkpoint.Index != 1 || processed != 1 || client.lookups != 1 {
					t.Fatal("valid live regression lost")
				}
			} else if checkpoint.Index != 31078 || processed != 0 {
				t.Fatal("unconfirmed regression changed cursor")
			}
		})
	}
}

func TestInvoiceCursorFailureAndRestartDuringRecovery(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "process", true: "checkpoint"}[failSave], func(t *testing.T) {
			durable := (invoiceCheckpoint{}).advance(cursorInvoice(31078, 40000, 1000, 1), false)
			invoice := cursorInvoice(1, 40001, 1100, 2)
			stored := map[string]bool{}
			fail := true
			mirrors := 0
			process := func(_ context.Context, invoice *lnrpc.Invoice, historical bool) error {
				if fail && !failSave {
					return errors.New("storage unavailable")
				}
				hash := hex.EncodeToString(invoice.RHash)
				if !stored[hash] && !historical {
					mirrors++
				}
				stored[hash] = true
				return nil
			}
			save := func(_ context.Context, c invoiceCheckpoint) error {
				if fail {
					return errors.New("checkpoint unavailable")
				}
				durable = c
				return nil
			}
			client := &invoiceCursorClient{page: []*lnrpc.Invoice{invoice}}
			err := followSettledInvoices(context.Background(), client, durable, save, process, func(string, ...any) {})
			if err == nil || durable.Index != 31078 {
				t.Fatal("failed processing advanced cursor")
			}
			fail = false
			err = followSettledInvoices(context.Background(), client, durable, save, process, func(string, ...any) {})
			if !errors.Is(err, io.EOF) || durable.Index != 1 || len(stored) != 1 || mirrors != 0 {
				t.Fatal("restart lost/duplicated recovery")
			}
		})
	}
}

func TestInvoiceCursorLegacyWithoutAnchorDoesNotResetFromHistory(t *testing.T) {
	checkpoint := invoiceCheckpoint{Index: 31078}
	client := &invoiceCursorClient{page: []*lnrpc.Invoice{cursorInvoice(1, 50000, 1000, 1)}}
	processed := 0
	_ = followSettledInvoices(context.Background(), client, checkpoint,
		func(_ context.Context, c invoiceCheckpoint) error { checkpoint = c; return nil },
		func(context.Context, *lnrpc.Invoice, bool) error { processed++; return nil }, func(string, ...any) {})
	if checkpoint.Index != 31078 || checkpoint.SettledAt == 0 || processed != 0 {
		t.Fatal("unanchored historical event reset cursor")
	}
}

func TestChatInvoiceCheckpointSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	service := &ChatService{logger: log.New(io.Discard, "", 0), legacy: newChatFileStore(filepath.Join(dir, "messages.jsonl"), filepath.Join(dir, "cursor.txt"), filepath.Join(dir, "read.json"))}
	service.legacy.saveCursor(31078)
	checkpoint, err := service.loadInvoiceCheckpoint()
	if err != nil || checkpoint.Index != 31078 {
		t.Fatal("legacy cursor not imported")
	}
	checkpoint = invoiceCheckpoint{Index: 1, AddIndex: 40001, SettledAt: 1100, EpochAfter: 1000, Hash: "hash"}
	if err := service.saveInvoiceCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	service.legacy.saveCursor(31078) // a stale scalar must never override the epoch
	restored, err := service.loadInvoiceCheckpoint()
	if err != nil || restored != checkpoint {
		t.Fatalf("restored=%+v err=%v", restored, err)
	}
	checkpoint.Index = 2
	if err := service.saveInvoiceCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	restored, err = service.loadInvoiceCheckpoint()
	if err != nil || restored.Index != 2 {
		t.Fatal("atomic replacement failed")
	}
}

func TestChatFileStoreInvoiceReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.jsonl")
	store := newChatFileStore(path, filepath.Join(dir, "cursor.txt"), filepath.Join(dir, "read.json"))
	msg := ChatMessage{Timestamp: time.Now(), PeerPubkey: "peer", Direction: "in", PaymentHash: "hash", Message: "one payment"}
	for i := 0; i < 2; i++ {
		if err := store.append(msg); err != nil {
			t.Fatal(err)
		}
	}
	store = newChatFileStore(path, filepath.Join(dir, "cursor.txt"), filepath.Join(dir, "read.json"))
	if err := store.append(msg); err != nil {
		t.Fatal(err)
	}
	items, err := store.list("peer", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("replayed messages=%d err=%v", len(items), err)
	}
	msg.Direction = "out"
	if err := store.append(msg); err != nil {
		t.Fatal(err)
	}
	items, err = store.list("peer", 10)
	if err != nil || len(items) != 2 {
		t.Fatal("directional message identity lost")
	}
}

func TestInvoiceCursorInvalidCheckpointFailsClosed(t *testing.T) {
	for _, raw := range []string{"{", "null", "{}", `{"index":1}`, `{"index":1,"settled_at":10,"epoch_after":11}`} {
		if _, err := decodeInvoiceCheckpoint(raw, 31078); err == nil {
			t.Fatalf("accepted invalid checkpoint: %s", raw)
		}
	}
}
