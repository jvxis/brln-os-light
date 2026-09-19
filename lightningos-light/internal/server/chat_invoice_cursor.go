package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const chatInvoiceCheckpointKey = "chat_invoice_checkpoint"

func (c *ChatService) loadInvoiceCheckpoint() (invoiceCheckpoint, error) {
	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var raw string
		err := db.QueryRow(ctx, "select value from chat_state where key=$1", chatInvoiceCheckpointKey).Scan(&raw)
		if err == nil {
			return decodeInvoiceCheckpoint(raw, 0)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return invoiceCheckpoint{}, err
		}
		// A database outage must not silently rewind to a stale file cursor.
		legacy, err := c.dbLoadCursor(ctx, db)
		if err != nil {
			return invoiceCheckpoint{}, err
		}
		return invoiceCheckpoint{Index: legacy}, nil
	}
	raw, err := os.ReadFile(c.legacy.cursorPath + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return invoiceCheckpoint{Index: c.legacy.loadCursor()}, nil
	}
	if err != nil {
		return invoiceCheckpoint{}, err
	}
	return decodeInvoiceCheckpoint(string(raw), 0)
}

func (c *ChatService) saveInvoiceCheckpoint(parent context.Context, checkpoint invoiceCheckpoint) error {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		_, err := db.Exec(ctx, `
insert into chat_state (key, value, updated_at)
values ($1, $2, now()), ($3, $4, now())
on conflict (key) do update set value=excluded.value, updated_at=excluded.updated_at
`, chatInvoiceCheckpointKey, string(raw), chatCursorStateKey, strconv.FormatUint(checkpoint.Index, 10))
		return err
	}
	// File-only installations use an atomic checkpoint replacement. The old
	// scalar file is just a compatibility mirror, never the new authority.
	if err := c.legacy.ensureDir(); err != nil {
		return err
	}
	path := c.legacy.cursorPath + ".json"
	f, err := os.CreateTemp(filepath.Dir(path), ".invoice-checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0640); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	c.legacy.saveCursor(checkpoint.Index)
	return nil
}
