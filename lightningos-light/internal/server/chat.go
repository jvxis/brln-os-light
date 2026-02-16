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
  "strconv"
  "strings"
  "sync"
  "time"
  "unicode/utf8"

  "lightningos-light/internal/lndclient"
  "lightningos-light/lnrpc"
)

const (
  chatMessagesPath = "/var/lib/lightningos/chat/messages.jsonl"
  chatCursorPath = "/var/lib/lightningos/chat/cursor.txt"
  chatRetentionDays = 30
  chatCleanupInterval = 6 * time.Hour
  chatMessageLimitDefault = 200
  chatMessageMaxLength = 500
)

type ChatMessage struct {
  Timestamp time.Time `json:"timestamp"`
  PeerPubkey string `json:"peer_pubkey"`
  Direction string `json:"direction"`
  Message string `json:"message"`
  Status string `json:"status"`
  PaymentHash string `json:"payment_hash,omitempty"`
}

type ChatService struct {
  lnd *lndclient.Client
  logger *log.Logger
  store *chatStore
  mu sync.Mutex
  started bool
  stop chan struct{}
  notifier *Notifier
}

func NewChatService(lnd *lndclient.Client, logger *log.Logger) *ChatService {
  return &ChatService{
    lnd: lnd,
    logger: logger,
    store: newChatStore(chatMessagesPath, chatCursorPath),
  }
}

func (c *ChatService) AttachNotifier(notifier *Notifier) {
  c.mu.Lock()
  c.notifier = notifier
  c.mu.Unlock()
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
  return c.store.list(peerPubkey, limit)
}

type ChatInboxItem struct {
  PeerPubkey string `json:"peer_pubkey"`
  LastInboundAt time.Time `json:"last_inbound_at"`
}

func (c *ChatService) Inbox() ([]ChatInboxItem, error) {
  latest, err := c.store.latestInbound()
  if err != nil {
    return nil, err
  }
  items := make([]ChatInboxItem, 0, len(latest))
  for peer, ts := range latest {
    items = append(items, ChatInboxItem{
      PeerPubkey: peer,
      LastInboundAt: ts,
    })
  }
  return items, nil
}

func (c *ChatService) SendMessage(ctx context.Context, peerPubkey string, message string) (ChatMessage, error) {
  paymentHash, err := c.lnd.SendKeysendMessage(ctx, peerPubkey, 1, message)
  if err != nil {
    return ChatMessage{}, err
  }

  msg := ChatMessage{
    Timestamp: time.Now().UTC(),
    PeerPubkey: strings.TrimSpace(peerPubkey),
    Direction: "out",
    Message: message,
    Status: "sent",
    PaymentHash: paymentHash,
  }
  if err := c.store.append(msg); err != nil {
    c.logger.Printf("chat: failed to append outbound message: %v", err)
  }
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

  peerAlias := ""
  if msg.PeerPubkey != "" {
    peerAlias = notifier.lookupNodeAlias(msg.PeerPubkey)
  }

  evt := Notification{
    OccurredAt: msg.Timestamp,
    Type: "keysend",
    Action: "sent",
    Direction: "out",
    Status: "SUCCEEDED",
    AmountSat: 1,
    PeerPubkey: msg.PeerPubkey,
    PeerAlias: peerAlias,
    PaymentHash: hash,
  }

  ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
  _, _ = notifier.upsertNotification(ctx, fmt.Sprintf("payment:%s", hash), evt)
  cancel()
}

func (c *ChatService) runInvoices() {
  for {
    select {
    case <-c.stop:
      return
    default:
    }

    settleIndex := c.store.loadCursor()

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
      conn.Close()
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
      c.store.saveCursor(settleIndex)

      if !invoice.IsKeysend {
        continue
      }

      message, chanID, senderPubkey := extractKeysendMessage(invoice)
      if message == "" {
        continue
      }

      peerPubkey := ""
      if senderPubkey != "" && isValidPubkeyHex(senderPubkey) {
        peerPubkey = senderPubkey
      }
      if peerPubkey == "" && chanID != 0 {
        peerPubkey, _ = c.lookupPeerByChanID(chanID)
      }
      if peerPubkey == "" {
        continue
      }

      msg := ChatMessage{
        Timestamp: time.Unix(invoice.SettleDate, 0).UTC(),
        PeerPubkey: peerPubkey,
        Direction: "in",
        Message: message,
        Status: "received",
        PaymentHash: strings.ToLower(hex.EncodeToString(invoice.RHash)),
      }
      if err := c.store.append(msg); err != nil {
        c.logger.Printf("chat: failed to append inbound message: %v", err)
      }
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

type chatStore struct {
  path string
  cursorPath string
  mu sync.Mutex
  lastCleanup time.Time
}

func newChatStore(path string, cursorPath string) *chatStore {
  return &chatStore{
    path: path,
    cursorPath: cursorPath,
  }
}

func (s *chatStore) append(msg ChatMessage) error {
  s.mu.Lock()
  defer s.mu.Unlock()

  if err := s.ensureDir(); err != nil {
    return err
  }
  s.cleanupLocked()

  data, err := json.Marshal(msg)
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

func (s *chatStore) list(peerPubkey string, limit int) ([]ChatMessage, error) {
  s.mu.Lock()
  defer s.mu.Unlock()

  s.cleanupLocked()

  trimmed := strings.TrimSpace(peerPubkey)
  if trimmed == "" {
    return nil, errors.New("peer_pubkey required")
  }

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
  messages := []ChatMessage{}
  for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text())
    if line == "" {
      continue
    }
    var msg ChatMessage
    if err := json.Unmarshal([]byte(line), &msg); err != nil {
      continue
    }
    if msg.Timestamp.Before(cutoff) {
      continue
    }
    if strings.TrimSpace(msg.PeerPubkey) != trimmed {
      continue
    }
    messages = append(messages, msg)
  }
  if err := scanner.Err(); err != nil {
    return nil, err
  }
  if len(messages) > limit {
    messages = messages[len(messages)-limit:]
  }
  return messages, nil
}

func (s *chatStore) latestInbound() (map[string]time.Time, error) {
  s.mu.Lock()
  defer s.mu.Unlock()

  s.cleanupLocked()

  f, err := os.Open(s.path)
  if err != nil {
    if errors.Is(err, os.ErrNotExist) {
      return map[string]time.Time{}, nil
    }
    return nil, err
  }
  defer f.Close()

  scanner := bufio.NewScanner(f)
  buf := make([]byte, 0, 64*1024)
  scanner.Buffer(buf, 256*1024)

  cutoff := time.Now().AddDate(0, 0, -chatRetentionDays)
  latest := map[string]time.Time{}
  for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text())
    if line == "" {
      continue
    }
    var msg ChatMessage
    if err := json.Unmarshal([]byte(line), &msg); err != nil {
      continue
    }
    if msg.Direction != "in" {
      continue
    }
    if msg.Timestamp.Before(cutoff) {
      continue
    }
    peer := strings.TrimSpace(msg.PeerPubkey)
    if peer == "" {
      continue
    }
    if prev, ok := latest[peer]; !ok || msg.Timestamp.After(prev) {
      latest[peer] = msg.Timestamp
    }
  }
  if err := scanner.Err(); err != nil {
    return nil, err
  }
  return latest, nil
}

func (s *chatStore) loadCursor() uint64 {
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

func (s *chatStore) saveCursor(val uint64) {
  s.mu.Lock()
  defer s.mu.Unlock()

  if err := s.ensureDir(); err != nil {
    return
  }
  _ = os.WriteFile(s.cursorPath, []byte(strconv.FormatUint(val, 10)), 0640)
}

func (s *chatStore) ensureDir() error {
  return os.MkdirAll(filepath.Dir(s.path), 0750)
}

func (s *chatStore) cleanupLocked() {
  if !s.lastCleanup.IsZero() && time.Since(s.lastCleanup) < chatCleanupInterval {
    return
  }
  s.lastCleanup = time.Now()

  f, err := os.Open(s.path)
  if err != nil {
    return
  }
  defer f.Close()

  scanner := bufio.NewScanner(f)
  buf := make([]byte, 0, 64*1024)
  scanner.Buffer(buf, 256*1024)

  cutoff := time.Now().AddDate(0, 0, -chatRetentionDays)
  kept := []ChatMessage{}
  for scanner.Scan() {
    line := strings.TrimSpace(scanner.Text())
    if line == "" {
      continue
    }
    var msg ChatMessage
    if err := json.Unmarshal([]byte(line), &msg); err != nil {
      continue
    }
    if msg.Timestamp.Before(cutoff) {
      continue
    }
    kept = append(kept, msg)
  }

  tmpPath := s.path + ".tmp"
  tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0640)
  if err != nil {
    return
  }
  enc := json.NewEncoder(tmp)
  for _, msg := range kept {
    _ = enc.Encode(msg)
  }
  _ = tmp.Close()
  _ = os.Rename(tmpPath, s.path)
}
