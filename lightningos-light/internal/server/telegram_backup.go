package server

import (
  "bytes"
  "context"
  "errors"
  "fmt"
  "io"
  "mime/multipart"
  "net/http"
  "os"
  "strings"
  "time"

  "lightningos-light/internal/lndclient"
  "lightningos-light/lnrpc"
)

const (
  telegramBotTokenKey = "NOTIFICATIONS_TG_BOT_TOKEN"
  telegramChatIDKey = "NOTIFICATIONS_TG_CHAT_ID"
)

type telegramBackupConfig struct {
  BotToken string
  ChatID string
}

func (cfg telegramBackupConfig) configured() bool {
  return cfg.BotToken != "" && cfg.ChatID != ""
}

func readTelegramBackupConfig() telegramBackupConfig {
  token := strings.TrimSpace(os.Getenv(telegramBotTokenKey))
  chatID := strings.TrimSpace(os.Getenv(telegramChatIDKey))
  if token == "" {
    if stored, err := readEnvFileValue(notificationsSecretsPath, telegramBotTokenKey); err == nil {
      token = strings.TrimSpace(stored)
      if token != "" {
        _ = os.Setenv(telegramBotTokenKey, token)
      }
    }
  }
  if chatID == "" {
    if stored, err := readEnvFileValue(notificationsSecretsPath, telegramChatIDKey); err == nil {
      chatID = strings.TrimSpace(stored)
      if chatID != "" {
        _ = os.Setenv(telegramChatIDKey, chatID)
      }
    }
  }
  return telegramBackupConfig{BotToken: token, ChatID: chatID}
}

func storeTelegramBackupConfig(token, chatID string) error {
  if err := ensureSecretsDir(); err != nil {
    return err
  }
  if err := writeEnvFileValue(notificationsSecretsPath, telegramBotTokenKey, token); err != nil {
    return err
  }
  if err := writeEnvFileValue(notificationsSecretsPath, telegramChatIDKey, chatID); err != nil {
    return err
  }
  _ = os.Setenv(telegramBotTokenKey, token)
  _ = os.Setenv(telegramChatIDKey, chatID)
  return nil
}

func (s *Server) handleTelegramBackupGet(w http.ResponseWriter, r *http.Request) {
  cfg := readTelegramBackupConfig()
  writeJSON(w, http.StatusOK, map[string]any{
    "chat_id": cfg.ChatID,
    "bot_token_set": cfg.BotToken != "",
  })
}

func (s *Server) handleTelegramBackupPost(w http.ResponseWriter, r *http.Request) {
  var req struct {
    BotToken string `json:"bot_token"`
    ChatID string `json:"chat_id"`
  }
  if err := readJSON(r, &req); err != nil {
    writeError(w, http.StatusBadRequest, "invalid json")
    return
  }

  token := strings.TrimSpace(req.BotToken)
  chatID := strings.TrimSpace(req.ChatID)

  if token == "" && chatID == "" {
    if err := storeTelegramBackupConfig("", ""); err != nil {
      writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store telegram config: %v", err))
      return
    }
    writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
    return
  }

  existing := readTelegramBackupConfig()
  if token == "" {
    if existing.BotToken == "" {
      writeError(w, http.StatusBadRequest, "bot_token required")
      return
    }
    token = existing.BotToken
  }
  if chatID == "" {
    if existing.ChatID == "" {
      writeError(w, http.StatusBadRequest, "chat_id required")
      return
    }
    chatID = existing.ChatID
  }

  if err := storeTelegramBackupConfig(token, chatID); err != nil {
    writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store telegram config: %v", err))
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTelegramBackupTest(w http.ResponseWriter, r *http.Request) {
  cfg := readTelegramBackupConfig()
  if !cfg.configured() {
    writeError(w, http.StatusBadRequest, "telegram backup not configured")
    return
  }

  ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
  defer cancel()

  data, err := s.lnd.ExportAllChannelBackups(ctx)
  if err != nil {
    msg := lndRPCErrorMessage(err)
    if msg == "" {
      msg = "failed to export channel backup"
    }
    writeError(w, http.StatusInternalServerError, msg)
    return
  }

  alias := getNodeAlias(ctx, s.lnd)
  filename, caption := telegramBackupPayload("test", "", alias, time.Now().UTC())
  if err := sendTelegramDocument(ctx, cfg.BotToken, cfg.ChatID, filename, data, caption); err != nil {
    writeError(w, http.StatusInternalServerError, err.Error())
    return
  }

  writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (n *Notifier) maybeSendTelegramBackup(update *lnrpc.ChannelEventUpdate) {
  if n == nil || n.lnd == nil || update == nil {
    return
  }

  reason := ""
  channelPoint := ""
  switch update.Type {
  case lnrpc.ChannelEventUpdate_OPEN_CHANNEL:
    reason = "open"
    if ch := update.GetOpenChannel(); ch != nil {
      channelPoint = ch.ChannelPoint
    }
  case lnrpc.ChannelEventUpdate_CLOSED_CHANNEL:
    reason = "close"
    if ch := update.GetClosedChannel(); ch != nil {
      channelPoint = ch.ChannelPoint
    }
  case lnrpc.ChannelEventUpdate_PENDING_OPEN_CHANNEL:
    reason = "opening"
    if ch := update.GetPendingOpenChannel(); ch != nil {
      txid := txidFromBytes(ch.Txid)
      if txid != "" {
        channelPoint = fmt.Sprintf("%s:%d", txid, ch.OutputIndex)
      }
    }
  default:
    return
  }

  n.triggerTelegramBackup(reason, channelPoint)
}

func (n *Notifier) triggerTelegramBackup(reason, channelPoint string) {
  if !n.shouldSendTelegramBackup(reason, channelPoint) {
    return
  }
  cfg := readTelegramBackupConfig()
  if !cfg.configured() {
    return
  }

  go func() {
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    if err := n.sendTelegramBackup(ctx, cfg, reason, channelPoint); err != nil {
      n.logger.Printf("notifications: telegram backup failed: %v", err)
    }
  }()
}

func (n *Notifier) shouldSendTelegramBackup(reason, channelPoint string) bool {
  if n == nil {
    return false
  }
  if strings.TrimSpace(channelPoint) == "" {
    return true
  }
  key := strings.TrimSpace(reason) + ":" + strings.TrimSpace(channelPoint)
  n.backupMu.Lock()
  defer n.backupMu.Unlock()
  if _, ok := n.backupSent[key]; ok {
    return false
  }
  n.backupSent[key] = time.Now().UTC()
  return true
}

func (n *Notifier) sendTelegramBackup(ctx context.Context, cfg telegramBackupConfig, reason, channelPoint string) error {
  if !cfg.configured() {
    return errors.New("telegram config missing")
  }
  data, err := n.lnd.ExportAllChannelBackups(ctx)
  if err != nil {
    return err
  }
  if len(data) == 0 {
    return errors.New("empty channel backup")
  }

  alias := getNodeAlias(ctx, n.lnd)
  filename, caption := telegramBackupPayload(reason, channelPoint, alias, time.Now().UTC())

  return sendTelegramDocument(ctx, cfg.BotToken, cfg.ChatID, filename, data, caption)
}

func getNodeAlias(ctx context.Context, lnd *lndclient.Client) string {
  if lnd == nil {
    return ""
  }
  infoCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
  defer cancel()
  conn, err := lnd.DialLightning(infoCtx)
  if err != nil {
    return ""
  }
  defer conn.Close()
  client := lnrpc.NewLightningClient(conn)
  info, err := client.GetInfo(infoCtx, &lnrpc.GetInfoRequest{})
  if err != nil || info == nil {
    return ""
  }
  return strings.TrimSpace(info.Alias)
}

func telegramReasonEmoji(tag string) string {
  switch tag {
  case "opening":
    return "🟡"
  case "open":
    return "🟢"
  case "closing":
    return "🟠"
  case "close":
    return "🔴"
  case "onchain_receive_confirmed":
    return "✅"
  case "test":
    return "🧪"
  default:
    return ""
  }
}

func telegramBackupSubjectLabel(tag string) string {
  if strings.HasPrefix(tag, "onchain_") {
    return "tx"
  }
  return "channel"
}

func telegramBackupPayload(reason, channelPoint, alias string, when time.Time) (string, string) {
  tag := strings.TrimSpace(reason)
  if tag == "" {
    tag = "update"
  }
  filename := fmt.Sprintf("scb-%s-%s.scb", tag, when.Format("20060102-150405"))
  displayTag := tag
  if emoji := telegramReasonEmoji(tag); emoji != "" {
    displayTag = fmt.Sprintf("%s %s", emoji, tag)
  }
  caption := fmt.Sprintf("LightningOS SCB All Channels Backup - %s", when.Format("2006-01-02 15:04:05 UTC"))
  if strings.TrimSpace(alias) != "" {
    caption = fmt.Sprintf("%s - %s", caption, strings.TrimSpace(alias))
  }
  caption = fmt.Sprintf("%s - (%s)", caption, displayTag)
  if strings.TrimSpace(channelPoint) != "" {
    caption = fmt.Sprintf("%s %s %s", caption, telegramBackupSubjectLabel(tag), strings.TrimSpace(channelPoint))
  }
  return filename, caption
}

func sendTelegramDocument(ctx context.Context, token, chatID, filename string, data []byte, caption string) error {
  if strings.TrimSpace(token) == "" || strings.TrimSpace(chatID) == "" {
    return errors.New("telegram config missing")
  }
  if len(data) == 0 {
    return errors.New("empty file")
  }

  var payload bytes.Buffer
  writer := multipart.NewWriter(&payload)
  if err := writer.WriteField("chat_id", chatID); err != nil {
    return err
  }
  if strings.TrimSpace(caption) != "" {
    if err := writer.WriteField("caption", caption); err != nil {
      return err
    }
  }

  part, err := writer.CreateFormFile("document", filename)
  if err != nil {
    return err
  }
  if _, err := part.Write(data); err != nil {
    return err
  }
  if err := writer.Close(); err != nil {
    return err
  }

  endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
  req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &payload)
  if err != nil {
    return err
  }
  req.Header.Set("Content-Type", writer.FormDataContentType())

  resp, err := http.DefaultClient.Do(req)
  if err != nil {
    return err
  }
  defer resp.Body.Close()
  if resp.StatusCode < 200 || resp.StatusCode > 299 {
    body, _ := io.ReadAll(resp.Body)
    return fmt.Errorf("telegram api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
  }
  return nil
}
