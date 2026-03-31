package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	walletActivityPath       = "/var/lib/lightningos/wallet-activity.json"
	walletActivityLimit      = 200
	walletActivityFetchLimit = 20000
)

type walletActivityStore struct {
	Hashes []string `json:"hashes"`
}

func normalizeWalletHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
}

func (s *Server) recordWalletActivity(hash string) {
	normalized := normalizeWalletHash(hash)
	if normalized == "" {
		return
	}

	s.walletActivityMu.Lock()
	defer s.walletActivityMu.Unlock()

	store := s.readWalletActivityLocked()
	updated := make([]string, 0, walletActivityLimit)
	updated = append(updated, normalized)
	for _, existing := range store.Hashes {
		item := normalizeWalletHash(existing)
		if item == "" || item == normalized {
			continue
		}
		updated = append(updated, item)
		if len(updated) >= walletActivityLimit {
			break
		}
	}
	store.Hashes = updated
	if err := s.writeWalletActivityLocked(store); err != nil && s.logger != nil {
		s.logger.Printf("wallet activity: failed to persist: %v", err)
	}
}

func (s *Server) walletActivitySet() map[string]struct{} {
	s.walletActivityMu.Lock()
	defer s.walletActivityMu.Unlock()

	if !s.walletActivityWritable() {
		return map[string]struct{}{}
	}

	store := s.readWalletActivityLocked()
	hashes := make(map[string]struct{}, len(store.Hashes))
	for _, hash := range store.Hashes {
		normalized := normalizeWalletHash(hash)
		if normalized == "" {
			continue
		}
		hashes[normalized] = struct{}{}
	}
	return hashes
}

func (s *Server) walletActivityWritable() bool {
	f, err := os.OpenFile(walletActivityPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func (s *Server) readWalletActivityLocked() walletActivityStore {
	content, err := os.ReadFile(walletActivityPath)
	if err != nil {
		return walletActivityStore{}
	}
	var store walletActivityStore
	if err := json.Unmarshal(content, &store); err != nil {
		return walletActivityStore{}
	}
	return store
}

func (s *Server) writeWalletActivityLocked(store walletActivityStore) error {
	if err := os.MkdirAll(filepath.Dir(walletActivityPath), 0750); err != nil {
		return err
	}
	payload, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(walletActivityPath, payload, 0660)
}

func (s *Server) walletLightningActivity(ctx context.Context, start time.Time, end time.Time, limit int) ([]lndclient.RecentActivity, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > walletActivityFetchLimit {
		limit = walletActivityFetchLimit
	}

	rows, err := s.db.Query(ctx, `
select occurred_at, type, action, direction, status, amount_sat, fee_sat,
  coalesce(channel_id, 0) as channel_id,
  coalesce(channel_point, '') as channel_point,
  coalesce(channel_alias, '') as channel_alias,
  coalesce(payment_hash, '') as payment_hash,
  coalesce(memo, '') as memo
from notifications n
where n.occurred_at >= $1
  and n.occurred_at <= $2
  and n.type in ('lightning', 'keysend')
  and n.action in ('sent', 'received')
  and (
    btrim(coalesce(n.payment_hash, '')) = ''
    or not exists (
      select 1
      from notifications r
      where r.type = 'rebalance'
        and r.payment_hash = n.payment_hash
    )
  )
order by n.occurred_at desc, n.id desc
limit $3`, start, end, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]lndclient.RecentActivity, 0, limit)
	for rows.Next() {
		var (
			occurredAt   time.Time
			evtType      string
			action       string
			direction    string
			status       string
			amountSat    int64
			feeSat       int64
			channelID    int64
			channelPoint string
			channelAlias string
			paymentHash  string
			memo         string
		)
		if err := rows.Scan(
			&occurredAt,
			&evtType,
			&action,
			&direction,
			&status,
			&amountSat,
			&feeSat,
			&channelID,
			&channelPoint,
			&channelAlias,
			&paymentHash,
			&memo,
		); err != nil {
			return nil, err
		}

		itemType := "payment"
		if strings.EqualFold(strings.TrimSpace(action), "received") {
			itemType = "invoice"
		}

		item := lndclient.RecentActivity{
			Type:         itemType,
			Network:      "lightning",
			Direction:    strings.TrimSpace(direction),
			AmountSat:    amountSat,
			Memo:         strings.TrimSpace(memo),
			Timestamp:    occurredAt.UTC(),
			Status:       strings.TrimSpace(status),
			FeeSat:       feeSat,
			ChannelPoint: strings.TrimSpace(channelPoint),
			ChannelAlias: strings.TrimSpace(channelAlias),
			PaymentHash:  normalizeWalletHash(paymentHash),
			Keysend:      strings.EqualFold(strings.TrimSpace(evtType), "keysend"),
		}
		if channelID > 0 {
			item.ChannelID = uint64(channelID)
		}
		if item.Type == "invoice" {
			item.SettledAt = item.Timestamp
		}
		items = append(items, item)
	}

	return items, rows.Err()
}
