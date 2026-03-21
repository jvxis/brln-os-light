package server

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"lightningos-light/internal/lndclient"
	"lightningos-light/lnrpc"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	chatMessagesPath        = "/var/lib/lightningos/chat/messages.jsonl"
	chatCursorPath          = "/var/lib/lightningos/chat/cursor.txt"
	chatRetentionDays       = 30
	chatCleanupInterval     = 6 * time.Hour
	chatMessageLimitDefault = 200
	chatMessageMaxLength    = 500
	chatPreviewMaxRunes     = 140
	chatCursorStateKey      = "chat_invoices_settle_index"
)

type ChatMessage struct {
	Timestamp   time.Time `json:"timestamp"`
	PeerPubkey  string    `json:"peer_pubkey"`
	PeerAlias   string    `json:"peer_alias,omitempty"`
	Direction   string    `json:"direction"`
	Message     string    `json:"message"`
	Status      string    `json:"status"`
	PaymentHash string    `json:"payment_hash,omitempty"`
}

type ChatInboxItem struct {
	PeerPubkey           string    `json:"peer_pubkey"`
	PeerAlias            string    `json:"peer_alias,omitempty"`
	LastInboundAt        time.Time `json:"last_inbound_at"`
	LastMessageAt        time.Time `json:"last_message_at"`
	LastMessage          string    `json:"last_message,omitempty"`
	LastMessageDirection string    `json:"last_message_direction,omitempty"`
}

type ChatService struct {
	lnd           *lndclient.Client
	logger        *log.Logger
	legacy        *chatFileStore
	mu            sync.Mutex
	started       bool
	stop          chan struct{}
	notifier      *Notifier
	db            *pgxpool.Pool
	lastDBCleanup time.Time
}

func NewChatService(lnd *lndclient.Client, logger *log.Logger) *ChatService {
	return &ChatService{
		lnd:    lnd,
		logger: logger,
		legacy: newChatFileStore(chatMessagesPath, chatCursorPath),
	}
}

func (c *ChatService) AttachNotifier(notifier *Notifier) {
	c.mu.Lock()
	c.notifier = notifier
	c.mu.Unlock()
}

func (c *ChatService) AttachDB(ctx context.Context, db *pgxpool.Pool) error {
	if c == nil || db == nil {
		return nil
	}

	if err := c.ensureSchema(ctx, db); err != nil {
		return err
	}
	if err := c.importLegacyData(ctx, db); err != nil {
		return err
	}

	c.mu.Lock()
	c.db = db
	c.mu.Unlock()
	return nil
}

func (c *ChatService) Start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.stop = make(chan struct{})
	c.mu.Unlock()

	go c.runInvoices()
}

func (c *ChatService) Messages(peerPubkey string, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = chatMessageLimitDefault
	}

	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		items, err := c.dbListMessages(ctx, db, peerPubkey, limit)
		cancel()
		if err == nil {
			return items, nil
		}
		c.logger.Printf("chat: db messages fallback: %v", err)
	}

	return c.legacy.list(peerPubkey, limit)
}

func (c *ChatService) Inbox() ([]ChatInboxItem, error) {
	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		items, err := c.dbInbox(ctx, db)
		cancel()
		if err == nil {
			return items, nil
		}
		c.logger.Printf("chat: db inbox fallback: %v", err)
	}

	return c.legacy.inbox()
}

func (c *ChatService) SendMessage(ctx context.Context, peerPubkey string, message string) (ChatMessage, error) {
	paymentHash, err := c.lnd.SendKeysendMessage(ctx, peerPubkey, 1, message)
	if err != nil {
		return ChatMessage{}, err
	}

	msg := ChatMessage{
		Timestamp:   time.Now().UTC(),
		PeerPubkey:  strings.TrimSpace(peerPubkey),
		PeerAlias:   c.resolvePeerAlias(peerPubkey),
		Direction:   "out",
		Message:     message,
		Status:      "sent",
		PaymentHash: paymentHash,
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := c.persistMessage(persistCtx, msg); err != nil {
		c.logger.Printf("chat: failed to persist outbound message: %v", err)
	}
	cancel()
	c.recordKeysendNotification(msg)
	return msg, nil
}

func (c *ChatService) recordKeysendNotification(msg ChatMessage) {
	hash := normalizeHash(msg.PaymentHash)
	if hash == "" {
		return
	}

	c.mu.Lock()
	notifier := c.notifier
	c.mu.Unlock()
	if notifier == nil {
		return
	}

	evt := buildSentKeysendNotification(msg, hash, notifier.lookupNodeAlias)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	_, _ = notifier.upsertNotification(ctx, fmt.Sprintf("payment:%s", hash), evt)
	cancel()
}

func buildSentKeysendNotification(msg ChatMessage, hash string, aliasLookup func(string) string) Notification {
	alias := strings.TrimSpace(msg.PeerAlias)
	if alias == "" && aliasLookup != nil {
		alias = strings.TrimSpace(aliasLookup(msg.PeerPubkey))
	}
	return Notification{
		OccurredAt:  msg.Timestamp,
		Type:        "keysend",
		Action:      "sent",
		Direction:   "out",
		Status:      "SUCCEEDED",
		AmountSat:   1,
		PeerPubkey:  strings.TrimSpace(msg.PeerPubkey),
		PeerAlias:   alias,
		PaymentHash: hash,
		Memo:        strings.TrimSpace(msg.Message),
	}
}

func (c *ChatService) runInvoices() {
	for {
		select {
		case <-c.stop:
			return
		default:
		}

		settleIndex := c.loadCursor()

		conn, err := c.lnd.DialLightning(context.Background())
		if err != nil {
			c.logger.Printf("chat: invoice stream dial failed: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		client := lnrpc.NewLightningClient(conn)
		stream, err := client.SubscribeInvoices(context.Background(), &lnrpc.InvoiceSubscription{
			SettleIndex: settleIndex,
		})
		if err != nil {
			c.logger.Printf("chat: invoice stream subscribe failed: %v", err)
			_ = conn.Close()
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			invoice, err := stream.Recv()
			if err != nil {
				c.logger.Printf("chat: invoice stream ended: %v", err)
				_ = conn.Close()
				break
			}

			if invoice.State != lnrpc.Invoice_SETTLED {
				continue
			}
			if invoice.SettleIndex <= settleIndex {
				continue
			}
			settleIndex = invoice.SettleIndex
			c.saveCursor(settleIndex)

			if !invoice.IsKeysend {
				continue
			}

			message, chanID, senderPubkey := extractKeysendMessage(invoice)
			if message == "" {
				continue
			}

			peerPubkey := ""
			peerAlias := ""
			if senderPubkey != "" && isValidPubkeyHex(senderPubkey) {
				peerPubkey = senderPubkey
				peerAlias = c.resolvePeerAlias(peerPubkey)
			}
			if peerPubkey == "" && chanID != 0 {
				peerPubkey, peerAlias = c.lookupPeerByChanID(chanID)
			}
			if peerPubkey == "" {
				continue
			}
			if peerAlias == "" {
				peerAlias = c.resolvePeerAlias(peerPubkey)
			}

			msg := ChatMessage{
				Timestamp:   time.Unix(invoice.SettleDate, 0).UTC(),
				PeerPubkey:  peerPubkey,
				PeerAlias:   peerAlias,
				Direction:   "in",
				Message:     message,
				Status:      "received",
				PaymentHash: strings.ToLower(hex.EncodeToString(invoice.RHash)),
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := c.persistMessage(persistCtx, msg); err != nil {
				c.logger.Printf("chat: failed to persist inbound message: %v", err)
			}
			cancel()
		}

		time.Sleep(2 * time.Second)
	}
}

func (c *ChatService) lookupPeerByChanID(chanID uint64) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	channels, err := c.lnd.ListChannels(ctx)
	if err != nil {
		return "", ""
	}
	for _, ch := range channels {
		if ch.ChannelID == chanID {
			return ch.RemotePubkey, ch.PeerAlias
		}
	}
	return "", ""
}

func (c *ChatService) resolvePeerAlias(pubkey string) string {
	trimmed := strings.TrimSpace(pubkey)
	if trimmed == "" {
		return ""
	}

	c.mu.Lock()
	notifier := c.notifier
	c.mu.Unlock()
	if notifier != nil {
		if alias := strings.TrimSpace(notifier.lookupNodeAlias(trimmed)); alias != "" {
			return alias
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return strings.TrimSpace(c.lnd.LookupNodeAlias(ctx, trimmed))
}

func (c *ChatService) persistMessage(ctx context.Context, msg ChatMessage) error {
	normalized := normalizeChatMessage(msg)
	var errs []string

	if db := c.dbPool(); db != nil {
		if err := c.dbAppendMessage(ctx, db, normalized); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if err := c.legacy.append(normalized); err != nil {
		errs = append(errs, err.Error())
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (c *ChatService) loadCursor() uint64 {
	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		val, err := c.dbLoadCursor(ctx, db)
		cancel()
		if err == nil && val > 0 {
			return val
		}
		if err != nil {
			c.logger.Printf("chat: db cursor fallback: %v", err)
		}
	}
	return c.legacy.loadCursor()
}

func (c *ChatService) saveCursor(val uint64) {
	if db := c.dbPool(); db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		if err := c.dbSaveCursor(ctx, db, val); err != nil {
			c.logger.Printf("chat: failed to persist cursor in db: %v", err)
		}
		cancel()
	}
	c.legacy.saveCursor(val)
}

func (c *ChatService) dbPool() *pgxpool.Pool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.db
}

func (c *ChatService) ensureSchema(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
create table if not exists chat_messages (
  id bigserial primary key,
  timestamp timestamptz not null,
  peer_pubkey text not null,
  peer_alias text not null default '',
  direction text not null check (direction in ('in','out')),
  message text not null,
  status text not null default '',
  payment_hash text not null default '',
  created_at timestamptz not null default now()
);

create index if not exists idx_chat_messages_peer_time_desc on chat_messages (peer_pubkey, timestamp desc, id desc);
create index if not exists idx_chat_messages_direction_time_desc on chat_messages (direction, timestamp desc, id desc);
create index if not exists idx_chat_messages_time_desc on chat_messages (timestamp desc, id desc);
create unique index if not exists idx_chat_messages_direction_hash on chat_messages (direction, payment_hash) where payment_hash <> '';

create table if not exists chat_state (
  key text primary key,
  value text not null,
  updated_at timestamptz not null default now()
);
`)
	return err
}

func (c *ChatService) importLegacyData(ctx context.Context, db *pgxpool.Pool) error {
	messages, err := c.legacy.allRetained()
	if err != nil {
		return err
	}
	if len(messages) > 0 {
		tx, err := db.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)

		batch := &pgx.Batch{}
		for _, msg := range messages {
			item := normalizeChatMessage(msg)
			batch.Queue(`
insert into chat_messages (timestamp, peer_pubkey, peer_alias, direction, message, status, payment_hash)
values ($1, $2, $3, $4, $5, $6, $7)
on conflict do nothing
`, item.Timestamp, item.PeerPubkey, item.PeerAlias, item.Direction, item.Message, item.Status, item.PaymentHash)
		}

		br := tx.SendBatch(ctx, batch)
		for range messages {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return err
			}
		}
		if err := br.Close(); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}

	legacyCursor := c.legacy.loadCursor()
	if legacyCursor == 0 {
		return nil
	}

	storedCursor, err := c.dbLoadCursor(ctx, db)
	if err != nil {
		return err
	}
	if legacyCursor > storedCursor {
		return c.dbSaveCursor(ctx, db, legacyCursor)
	}
	return nil
}

func (c *ChatService) cleanupDBIfNeeded(ctx context.Context, db *pgxpool.Pool) error {
	c.mu.Lock()
	if !c.lastDBCleanup.IsZero() && time.Since(c.lastDBCleanup) < chatCleanupInterval {
		c.mu.Unlock()
		return nil
	}
	c.lastDBCleanup = time.Now()
	c.mu.Unlock()

	_, err := db.Exec(ctx, `delete from chat_messages where timestamp < now() - interval '30 day'`)
	return err
}

func (c *ChatService) dbAppendMessage(ctx context.Context, db *pgxpool.Pool, msg ChatMessage) error {
	if err := c.cleanupDBIfNeeded(ctx, db); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
insert into chat_messages (timestamp, peer_pubkey, peer_alias, direction, message, status, payment_hash)
values ($1, $2, $3, $4, $5, $6, $7)
on conflict do nothing
`, msg.Timestamp, msg.PeerPubkey, msg.PeerAlias, msg.Direction, msg.Message, msg.Status, msg.PaymentHash)
	return err
}

func (c *ChatService) dbListMessages(ctx context.Context, db *pgxpool.Pool, peerPubkey string, limit int) ([]ChatMessage, error) {
	if err := c.cleanupDBIfNeeded(ctx, db); err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(peerPubkey)
	if trimmed == "" {
		return nil, errors.New("peer_pubkey required")
	}

	rows, err := db.Query(ctx, `
select timestamp, peer_pubkey, peer_alias, direction, message, status, payment_hash
from chat_messages
where peer_pubkey = $1
  and timestamp >= now() - interval '30 day'
order by timestamp desc, id desc
limit $2
`, trimmed, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ChatMessage{}
	for rows.Next() {
		var item ChatMessage
		if err := rows.Scan(
			&item.Timestamp,
			&item.PeerPubkey,
			&item.PeerAlias,
			&item.Direction,
			&item.Message,
			&item.Status,
			&item.PaymentHash,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

func (c *ChatService) dbInbox(ctx context.Context, db *pgxpool.Pool) ([]ChatInboxItem, error) {
	if err := c.cleanupDBIfNeeded(ctx, db); err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, `
with latest_inbound as (
  select peer_pubkey, max(timestamp) as last_inbound_at
  from chat_messages
  where direction = 'in'
    and timestamp >= now() - interval '30 day'
  group by peer_pubkey
),
latest_message as (
  select distinct on (peer_pubkey)
    peer_pubkey,
    timestamp as last_message_at,
    message as last_message,
    direction as last_message_direction
  from chat_messages
  where timestamp >= now() - interval '30 day'
  order by peer_pubkey, timestamp desc, id desc
),
latest_alias as (
  select distinct on (peer_pubkey)
    peer_pubkey,
    peer_alias
  from chat_messages
  where timestamp >= now() - interval '30 day'
    and peer_alias <> ''
  order by peer_pubkey, timestamp desc, id desc
)
select
  inbound.peer_pubkey,
  coalesce(alias.peer_alias, '') as peer_alias,
  inbound.last_inbound_at,
  coalesce(msg.last_message_at, inbound.last_inbound_at) as last_message_at,
  coalesce(msg.last_message, '') as last_message,
  coalesce(msg.last_message_direction, '') as last_message_direction
from latest_inbound inbound
left join latest_message msg on msg.peer_pubkey = inbound.peer_pubkey
left join latest_alias alias on alias.peer_pubkey = inbound.peer_pubkey
order by coalesce(msg.last_message_at, inbound.last_inbound_at) desc, inbound.peer_pubkey asc
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ChatInboxItem{}
	for rows.Next() {
		var item ChatInboxItem
		if err := rows.Scan(
			&item.PeerPubkey,
			&item.PeerAlias,
			&item.LastInboundAt,
			&item.LastMessageAt,
			&item.LastMessage,
			&item.LastMessageDirection,
		); err != nil {
			return nil, err
		}
		item.PeerAlias = strings.TrimSpace(item.PeerAlias)
		item.LastMessage = chatPreview(item.LastMessage)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *ChatService) dbLoadCursor(ctx context.Context, db *pgxpool.Pool) (uint64, error) {
	var raw string
	err := db.QueryRow(ctx, `select value from chat_state where key = $1`, chatCursorStateKey).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, nil
	}
	return parsed, nil
}

func (c *ChatService) dbSaveCursor(ctx context.Context, db *pgxpool.Pool, val uint64) error {
	_, err := db.Exec(ctx, `
insert into chat_state (key, value, updated_at)
values ($1, $2, now())
on conflict (key) do update
set value = excluded.value,
    updated_at = now()
`, chatCursorStateKey, strconv.FormatUint(val, 10))
	return err
}

func extractKeysendMessage(invoice *lnrpc.Invoice) (string, uint64, string) {
	if invoice == nil {
		return "", 0, ""
	}
	for _, htlc := range invoice.Htlcs {
		if htlc == nil {
			continue
		}
		payload, ok := htlc.CustomRecords[lndclient.KeysendMessageRecord]
		if !ok || len(payload) == 0 {
			continue
		}
		if !utf8.Valid(payload) {
			continue
		}
		sender := keysendSenderFromRecords(htlc.CustomRecords)
		return string(payload), htlc.ChanId, sender
	}
	return "", 0, ""
}

func keysendSenderFromRecords(records map[uint64][]byte) string {
	if len(records) == 0 {
		return ""
	}
	raw, ok := records[lndclient.KeysendSenderRecord]
	if !ok || len(raw) == 0 {
		return ""
	}
	if len(raw) == 33 {
		return strings.ToLower(hex.EncodeToString(raw))
	}
	if len(raw) == 66 && isValidPubkeyHex(string(raw)) {
		return strings.ToLower(string(raw))
	}
	return ""
}

func validateChatMessage(message string) error {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return errors.New("message required")
	}
	if utf8.RuneCountInString(trimmed) > chatMessageMaxLength {
		return fmt.Errorf("message exceeds %d characters", chatMessageMaxLength)
	}
	return nil
}

func isValidPubkeyHex(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	decoded, err := hex.DecodeString(trimmed)
	if err != nil {
		return false
	}
	return len(decoded) == 33
}

func normalizeChatMessage(msg ChatMessage) ChatMessage {
	normalized := msg
	normalized.Timestamp = msg.Timestamp.UTC()
	normalized.PeerPubkey = strings.TrimSpace(msg.PeerPubkey)
	normalized.PeerAlias = strings.TrimSpace(msg.PeerAlias)
	normalized.Direction = strings.TrimSpace(msg.Direction)
	normalized.Message = strings.TrimSpace(msg.Message)
	normalized.Status = strings.TrimSpace(msg.Status)
	normalized.PaymentHash = normalizeHash(msg.PaymentHash)
	return normalized
}

func chatFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func chatPreview(message string) string {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= chatPreviewMaxRunes {
		return trimmed
	}
	return string(runes[:chatPreviewMaxRunes]) + "..."
}

type chatFileStore struct {
	path        string
	cursorPath  string
	mu          sync.Mutex
	lastCleanup time.Time
}

func newChatFileStore(path string, cursorPath string) *chatFileStore {
	return &chatFileStore{
		path:       path,
		cursorPath: cursorPath,
	}
}

func (s *chatFileStore) append(msg ChatMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDir(); err != nil {
		return err
	}
	s.cleanupLocked()

	data, err := json.Marshal(normalizeChatMessage(msg))
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func (s *chatFileStore) list(peerPubkey string, limit int) ([]ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()

	trimmed := strings.TrimSpace(peerPubkey)
	if trimmed == "" {
		return nil, errors.New("peer_pubkey required")
	}

	all, err := s.readRetainedLocked()
	if err != nil {
		return nil, err
	}

	items := make([]ChatMessage, 0, len(all))
	for _, msg := range all {
		if strings.TrimSpace(msg.PeerPubkey) != trimmed {
			continue
		}
		items = append(items, msg)
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

func (s *chatFileStore) inbox() ([]ChatInboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()

	all, err := s.readRetainedLocked()
	if err != nil {
		return nil, err
	}

	latestInbound := map[string]time.Time{}
	latestMessage := map[string]ChatInboxItem{}
	latestAlias := map[string]string{}
	for _, msg := range all {
		peer := strings.TrimSpace(msg.PeerPubkey)
		if peer == "" {
			continue
		}
		if msg.Direction == "in" {
			if prev, ok := latestInbound[peer]; !ok || msg.Timestamp.After(prev) {
				latestInbound[peer] = msg.Timestamp
			}
		}
		prevItem, ok := latestMessage[peer]
		if !ok || msg.Timestamp.After(prevItem.LastMessageAt) {
			latestMessage[peer] = ChatInboxItem{
				PeerPubkey:           peer,
				PeerAlias:            strings.TrimSpace(msg.PeerAlias),
				LastMessageAt:        msg.Timestamp,
				LastMessage:          chatPreview(msg.Message),
				LastMessageDirection: msg.Direction,
			}
		}
		if alias := strings.TrimSpace(msg.PeerAlias); alias != "" {
			latestAlias[peer] = alias
		}
	}

	items := make([]ChatInboxItem, 0, len(latestInbound))
	for peer, inboundAt := range latestInbound {
		item := latestMessage[peer]
		item.PeerPubkey = peer
		item.LastInboundAt = inboundAt
		item.PeerAlias = chatFirstNonEmpty(latestAlias[peer], item.PeerAlias)
		items = append(items, item)
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastMessageAt.Equal(items[j].LastMessageAt) {
			return items[i].PeerPubkey < items[j].PeerPubkey
		}
		return items[i].LastMessageAt.After(items[j].LastMessageAt)
	})
	return items, nil
}

func (s *chatFileStore) allRetained() ([]ChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()
	return s.readRetainedLocked()
}

func (s *chatFileStore) loadCursor() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.cursorPath)
	if err != nil {
		return 0
	}
	val := strings.TrimSpace(string(raw))
	if val == "" {
		return 0
	}
	parsed, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func (s *chatFileStore) saveCursor(val uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureDir(); err != nil {
		return
	}
	_ = os.WriteFile(s.cursorPath, []byte(strconv.FormatUint(val, 10)), 0640)
}

func (s *chatFileStore) ensureDir() error {
	return os.MkdirAll(filepath.Dir(s.path), 0750)
}

func (s *chatFileStore) readRetainedLocked() ([]ChatMessage, error) {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ChatMessage{}, nil
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 256*1024)

	cutoff := time.Now().AddDate(0, 0, -chatRetentionDays)
	items := []ChatMessage{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg ChatMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		msg = normalizeChatMessage(msg)
		if msg.Timestamp.Before(cutoff) {
			continue
		}
		items = append(items, msg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *chatFileStore) cleanupLocked() {
	if !s.lastCleanup.IsZero() && time.Since(s.lastCleanup) < chatCleanupInterval {
		return
	}
	s.lastCleanup = time.Now()

	items, err := s.readRetainedLocked()
	if err != nil {
		return
	}

	if err := s.ensureDir(); err != nil {
		return
	}

	tmpPath := s.path + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
	if err != nil {
		return
	}
	enc := json.NewEncoder(tmp)
	for _, msg := range items {
		_ = enc.Encode(msg)
	}
	_ = tmp.Close()
	_ = os.Rename(tmpPath, s.path)
}
