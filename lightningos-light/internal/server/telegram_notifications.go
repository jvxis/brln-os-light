package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"lightningos-light/internal/reports"
	sysinfo "lightningos-light/internal/system"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	telegramSettingsID             = 1
	telegramSummaryIntervalMin     = 60
	telegramSummaryIntervalMax     = 720
	telegramSummaryIntervalDefault = 720
	telegramGoalBarSegments        = 14
	telegramMessageMaxChars        = 4000
)

func telegramBalanceSummaryTimeout() time.Duration {
	live := reportsLiveTimeout()
	if live <= 0 {
		live = 20 * time.Second
	}
	// Budget covers two live calls plus balance/summary reads.
	budget := (2 * live) + (25 * time.Second)
	if budget < 25*time.Second {
		return 25 * time.Second
	}
	return budget
}

type telegramNotificationSettings struct {
	ScbBackupEnabled         bool
	ActivityMirrorEnabled    bool
	AutofeeSummaryEnabled    bool
	SummaryEnabled           bool
	SummaryIntervalMin       int
	SummaryLastSentAt        *time.Time
	SystemSummaryEnabled     bool
	SystemSummaryIntervalMin int
	SystemSummaryLastSentAt  *time.Time
	LastUpdateID             int64
}

type telegramNotificationUpdate struct {
	ScbBackupEnabled         *bool
	ActivityMirrorEnabled    *bool
	AutofeeSummaryEnabled    *bool
	SummaryEnabled           *bool
	SummaryIntervalMin       *int
	SystemSummaryEnabled     *bool
	SystemSummaryIntervalMin *int
}

func defaultTelegramNotificationSettings() telegramNotificationSettings {
	return telegramNotificationSettings{
		ScbBackupEnabled:         true,
		SummaryEnabled:           false,
		SummaryIntervalMin:       telegramSummaryIntervalDefault,
		SystemSummaryEnabled:     false,
		SystemSummaryIntervalMin: telegramSummaryIntervalDefault,
	}
}

func normalizeTelegramSummaryInterval(value int) int {
	if value <= 0 {
		return telegramSummaryIntervalDefault
	}
	if value < telegramSummaryIntervalMin {
		return telegramSummaryIntervalMin
	}
	if value > telegramSummaryIntervalMax {
		return telegramSummaryIntervalMax
	}
	return value
}

func loadTelegramNotificationSettings(ctx context.Context, db *pgxpool.Pool) (telegramNotificationSettings, error) {
	settings := defaultTelegramNotificationSettings()
	if db == nil {
		return settings, errors.New("db unavailable")
	}

	var lastSent pgtype.Timestamptz
	var systemLastSent pgtype.Timestamptz
	err := db.QueryRow(ctx, `
select scb_backup_enabled,
  activity_mirror_enabled,
  autofee_summary_enabled,
  summary_enabled,
  summary_interval_min,
  summary_last_sent_at,
  system_summary_enabled,
  system_summary_interval_min,
  system_summary_last_sent_at,
  last_update_id
from telegram_notification_settings
where id=$1
`, telegramSettingsID).Scan(
		&settings.ScbBackupEnabled,
		&settings.ActivityMirrorEnabled,
		&settings.AutofeeSummaryEnabled,
		&settings.SummaryEnabled,
		&settings.SummaryIntervalMin,
		&lastSent,
		&settings.SystemSummaryEnabled,
		&settings.SystemSummaryIntervalMin,
		&systemLastSent,
		&settings.LastUpdateID,
	)
	if err != nil {
		return settings, err
	}
	settings.SummaryIntervalMin = normalizeTelegramSummaryInterval(settings.SummaryIntervalMin)
	settings.SystemSummaryIntervalMin = normalizeTelegramSummaryInterval(settings.SystemSummaryIntervalMin)
	if lastSent.Valid {
		ts := lastSent.Time
		settings.SummaryLastSentAt = &ts
	}
	if systemLastSent.Valid {
		ts := systemLastSent.Time
		settings.SystemSummaryLastSentAt = &ts
	}
	return settings, nil
}

func upsertTelegramNotificationSettings(ctx context.Context, db *pgxpool.Pool, settings telegramNotificationSettings) error {
	if db == nil {
		return errors.New("db unavailable")
	}
	settings.SummaryIntervalMin = normalizeTelegramSummaryInterval(settings.SummaryIntervalMin)
	_, err := db.Exec(ctx, `
insert into telegram_notification_settings (
  id,
  scb_backup_enabled,
  activity_mirror_enabled,
  autofee_summary_enabled,
  summary_enabled,
  summary_interval_min,
  summary_last_sent_at,
  system_summary_enabled,
  system_summary_interval_min,
  system_summary_last_sent_at,
  last_update_id,
  updated_at
 ) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,now())
on conflict (id) do update set
  scb_backup_enabled=excluded.scb_backup_enabled,
  activity_mirror_enabled=excluded.activity_mirror_enabled,
  autofee_summary_enabled=excluded.autofee_summary_enabled,
  summary_enabled=excluded.summary_enabled,
  summary_interval_min=excluded.summary_interval_min,
  summary_last_sent_at=excluded.summary_last_sent_at,
  system_summary_enabled=excluded.system_summary_enabled,
  system_summary_interval_min=excluded.system_summary_interval_min,
  system_summary_last_sent_at=excluded.system_summary_last_sent_at,
  last_update_id=excluded.last_update_id,
  updated_at=now()
	`, telegramSettingsID,
		settings.ScbBackupEnabled,
		settings.ActivityMirrorEnabled,
		settings.AutofeeSummaryEnabled,
		settings.SummaryEnabled,
		settings.SummaryIntervalMin,
		settings.SummaryLastSentAt,
		settings.SystemSummaryEnabled,
		settings.SystemSummaryIntervalMin,
		settings.SystemSummaryLastSentAt,
		settings.LastUpdateID,
	)
	return err
}

func updateTelegramNotificationSettings(ctx context.Context, db *pgxpool.Pool, update telegramNotificationUpdate) (telegramNotificationSettings, error) {
	settings, err := loadTelegramNotificationSettings(ctx, db)
	if err != nil {
		return settings, err
	}
	if update.ScbBackupEnabled != nil {
		settings.ScbBackupEnabled = *update.ScbBackupEnabled
	}
	if update.ActivityMirrorEnabled != nil {
		settings.ActivityMirrorEnabled = *update.ActivityMirrorEnabled
	}
	if update.AutofeeSummaryEnabled != nil {
		settings.AutofeeSummaryEnabled = *update.AutofeeSummaryEnabled
	}
	if update.SummaryEnabled != nil {
		settings.SummaryEnabled = *update.SummaryEnabled
	}
	if update.SummaryIntervalMin != nil {
		value := *update.SummaryIntervalMin
		if value < telegramSummaryIntervalMin || value > telegramSummaryIntervalMax {
			return settings, fmt.Errorf("summary_interval_min must be between %d and %d", telegramSummaryIntervalMin, telegramSummaryIntervalMax)
		}
		settings.SummaryIntervalMin = value
	}
	if update.SystemSummaryEnabled != nil {
		settings.SystemSummaryEnabled = *update.SystemSummaryEnabled
	}
	if update.SystemSummaryIntervalMin != nil {
		value := *update.SystemSummaryIntervalMin
		if value < telegramSummaryIntervalMin || value > telegramSummaryIntervalMax {
			return settings, fmt.Errorf("system_summary_interval_min must be between %d and %d", telegramSummaryIntervalMin, telegramSummaryIntervalMax)
		}
		settings.SystemSummaryIntervalMin = value
	}
	settings.SummaryIntervalMin = normalizeTelegramSummaryInterval(settings.SummaryIntervalMin)
	settings.SystemSummaryIntervalMin = normalizeTelegramSummaryInterval(settings.SystemSummaryIntervalMin)
	if err := upsertTelegramNotificationSettings(ctx, db, settings); err != nil {
		return settings, err
	}
	return settings, nil
}

func setTelegramSummaryLastSentAt(ctx context.Context, db *pgxpool.Pool, at time.Time) error {
	if db == nil {
		return errors.New("db unavailable")
	}
	_, err := db.Exec(ctx, `
update telegram_notification_settings
set summary_last_sent_at=$1, updated_at=now()
where id=$2
`, at, telegramSettingsID)
	return err
}

func setTelegramSystemSummaryLastSentAt(ctx context.Context, db *pgxpool.Pool, at time.Time) error {
	if db == nil {
		return errors.New("db unavailable")
	}
	_, err := db.Exec(ctx, `
update telegram_notification_settings
set system_summary_last_sent_at=$1, updated_at=now()
where id=$2
`, at, telegramSettingsID)
	return err
}

func setTelegramLastUpdateID(ctx context.Context, db *pgxpool.Pool, updateID int64) error {
	if db == nil {
		return errors.New("db unavailable")
	}
	_, err := db.Exec(ctx, `
update telegram_notification_settings
set last_update_id=$1, updated_at=now()
where id=$2
`, updateID, telegramSettingsID)
	return err
}

func (s *Server) handleTelegramNotificationsGet(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		msg := strings.TrimSpace(s.notifierErr)
		if msg == "" {
			msg = "notifications disabled"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	cfg := readTelegramBackupConfig()

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	settings, err := loadTelegramNotificationSettings(ctx, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to load telegram settings: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"chat_id":                     cfg.ChatID,
		"bot_token_set":               cfg.BotToken != "",
		"scb_backup_enabled":          settings.ScbBackupEnabled,
		"activity_mirror_enabled":     settings.ActivityMirrorEnabled,
		"autofee_summary_enabled":     settings.AutofeeSummaryEnabled,
		"summary_enabled":             settings.SummaryEnabled,
		"summary_interval_min":        settings.SummaryIntervalMin,
		"system_summary_enabled":      settings.SystemSummaryEnabled,
		"system_summary_interval_min": settings.SystemSummaryIntervalMin,
	})
}

func (s *Server) handleTelegramNotificationsPost(w http.ResponseWriter, r *http.Request) {
	if s.notifier == nil {
		msg := strings.TrimSpace(s.notifierErr)
		if msg == "" {
			msg = "notifications disabled"
		}
		writeError(w, http.StatusServiceUnavailable, msg)
		return
	}

	var req struct {
		BotToken                 *string `json:"bot_token"`
		ChatID                   *string `json:"chat_id"`
		ScbBackupEnabled         *bool   `json:"scb_backup_enabled"`
		ActivityMirrorEnabled    *bool   `json:"activity_mirror_enabled"`
		AutofeeSummaryEnabled    *bool   `json:"autofee_summary_enabled"`
		SummaryEnabled           *bool   `json:"summary_enabled"`
		SummaryIntervalMin       *int    `json:"summary_interval_min"`
		SystemSummaryEnabled     *bool   `json:"system_summary_enabled"`
		SystemSummaryIntervalMin *int    `json:"system_summary_interval_min"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if req.BotToken != nil || req.ChatID != nil {
		existing := readTelegramBackupConfig()
		token := existing.BotToken
		chatID := existing.ChatID
		if req.BotToken != nil {
			token = strings.TrimSpace(*req.BotToken)
		}
		if req.ChatID != nil {
			chatID = strings.TrimSpace(*req.ChatID)
		}
		credentialsChanged := token != existing.BotToken || chatID != existing.ChatID
		if token == "" && chatID == "" {
			if err := storeTelegramBackupConfig("", ""); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store telegram config: %v", err))
				return
			}
		} else {
			if token == "" {
				writeError(w, http.StatusBadRequest, "bot_token required")
				return
			}
			if chatID == "" {
				writeError(w, http.StatusBadRequest, "chat_id required")
				return
			}
			if err := storeTelegramBackupConfig(token, chatID); err != nil {
				writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to store telegram config: %v", err))
				return
			}
		}
		if credentialsChanged {
			resetCtx, resetCancel := context.WithTimeout(r.Context(), 3*time.Second)
			_ = setTelegramLastUpdateID(resetCtx, s.db, 0)
			resetCancel()
		}
		if strings.TrimSpace(token) != "" {
			go s.ensureTelegramBotCommands(token)
		}
	}

	if req.ScbBackupEnabled != nil || req.ActivityMirrorEnabled != nil || req.AutofeeSummaryEnabled != nil || req.SummaryEnabled != nil || req.SummaryIntervalMin != nil || req.SystemSummaryEnabled != nil || req.SystemSummaryIntervalMin != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		settings, err := updateTelegramNotificationSettings(ctx, s.db, telegramNotificationUpdate{
			ScbBackupEnabled:         req.ScbBackupEnabled,
			ActivityMirrorEnabled:    req.ActivityMirrorEnabled,
			AutofeeSummaryEnabled:    req.AutofeeSummaryEnabled,
			SummaryEnabled:           req.SummaryEnabled,
			SummaryIntervalMin:       req.SummaryIntervalMin,
			SystemSummaryEnabled:     req.SystemSummaryEnabled,
			SystemSummaryIntervalMin: req.SystemSummaryIntervalMin,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if s.notifier != nil {
			s.notifier.setTelegramActivityMirrorEnabled(settings.ActivityMirrorEnabled)
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) startTelegramNotifications() {
	if s == nil {
		return
	}
	go s.runTelegramCommandLoop()
	go s.runTelegramSummaryLoop()
	cfg := readTelegramBackupConfig()
	if strings.TrimSpace(cfg.BotToken) != "" {
		go s.ensureTelegramBotCommands(cfg.BotToken)
	}
}

func (s *Server) runTelegramSummaryLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		<-ticker.C
		if s == nil || s.notifier == nil {
			continue
		}
		cfg := readTelegramBackupConfig()
		if !cfg.configured() {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		settings, err := loadTelegramNotificationSettings(ctx, s.db)
		cancel()
		if err != nil {
			continue
		}

		if settings.SummaryEnabled {
			interval := time.Duration(normalizeTelegramSummaryInterval(settings.SummaryIntervalMin)) * time.Minute
			if interval <= 0 {
				interval = time.Duration(telegramSummaryIntervalDefault) * time.Minute
			}
			if settings.SummaryLastSentAt == nil || time.Since(*settings.SummaryLastSentAt) >= interval {
				summaryCtx, summaryCancel := context.WithTimeout(context.Background(), telegramBalanceSummaryTimeout())
				summary, summaryErr := s.buildTelegramBalanceSummary(summaryCtx)
				summaryCancel()
				if summaryErr != nil {
					if s.logger != nil {
						s.logger.Printf("notifications: telegram summary build failed: %v", summaryErr)
					}
				} else {
					sendCtx, sendCancel := context.WithTimeout(context.Background(), 20*time.Second)
					sendErr := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, summary)
					sendCancel()
					if sendErr != nil {
						if s.logger != nil {
							s.logger.Printf("notifications: telegram summary send failed: %v", sendErr)
						}
					} else {
						updateCtx, updateCancel := context.WithTimeout(context.Background(), 3*time.Second)
						_ = setTelegramSummaryLastSentAt(updateCtx, s.db, time.Now().UTC())
						updateCancel()
					}
				}
			}
		}

		if settings.SystemSummaryEnabled {
			interval := time.Duration(normalizeTelegramSummaryInterval(settings.SystemSummaryIntervalMin)) * time.Minute
			if interval <= 0 {
				interval = time.Duration(telegramSummaryIntervalDefault) * time.Minute
			}
			if settings.SystemSummaryLastSentAt == nil || time.Since(*settings.SystemSummaryLastSentAt) >= interval {
				sysCtx, sysCancel := context.WithTimeout(context.Background(), 20*time.Second)
				sysSummary, sysErr := s.buildTelegramSystemSummary(sysCtx)
				sysCancel()
				if sysErr != nil {
					if s.logger != nil {
						s.logger.Printf("notifications: telegram system summary build failed: %v", sysErr)
					}
				} else {
					sendCtx, sendCancel := context.WithTimeout(context.Background(), 20*time.Second)
					sendErr := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, sysSummary)
					sendCancel()
					if sendErr != nil {
						if s.logger != nil {
							s.logger.Printf("notifications: telegram system summary send failed: %v", sendErr)
						}
					} else {
						updateCtx, updateCancel := context.WithTimeout(context.Background(), 3*time.Second)
						_ = setTelegramSystemSummaryLastSentAt(updateCtx, s.db, time.Now().UTC())
						updateCancel()
					}
				}
			}
		}
	}
}

func (s *Server) runTelegramCommandLoop() {
	for {
		if s == nil || s.notifier == nil {
			time.Sleep(5 * time.Second)
			continue
		}
		cfg := readTelegramBackupConfig()
		if !cfg.configured() {
			time.Sleep(10 * time.Second)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		settings, err := loadTelegramNotificationSettings(ctx, s.db)
		cancel()
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		offset := settings.LastUpdateID + 1
		pollCtx, pollCancel := context.WithTimeout(context.Background(), 30*time.Second)
		updates, err := fetchTelegramUpdates(pollCtx, cfg.BotToken, offset)
		pollCancel()
		if err != nil {
			if s.logger != nil {
				s.logger.Printf("notifications: telegram updates failed: %v", err)
			}
			time.Sleep(5 * time.Second)
			continue
		}

		if len(updates) == 0 {
			continue
		}

		maxUpdateID := settings.LastUpdateID
		for _, update := range updates {
			if update.UpdateID > maxUpdateID {
				maxUpdateID = update.UpdateID
			}
			msg := update.Message
			if msg == nil {
				continue
			}
			if !telegramChatMatches(cfg.ChatID, msg.Chat) {
				continue
			}
			cmd := parseTelegramCommand(msg.Text)
			handled := false
			switch cmd {
			case "scb":
				s.handleTelegramScbCommand(cfg)
				handled = true
			case "balances":
				s.handleTelegramBalancesCommand(cfg)
				handled = true
			case "system":
				s.handleTelegramSystemCommand(cfg)
				handled = true
			case "alive":
				s.handleTelegramAliveCommand(cfg)
				handled = true
			}
			if !handled {
				switch parseTelegramLivenessText(msg.Text) {
				case "alive":
					s.handleTelegramAliveCommand(cfg)
				}
			}
		}

		if maxUpdateID > settings.LastUpdateID {
			updateCtx, updateCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = setTelegramLastUpdateID(updateCtx, s.db, maxUpdateID)
			updateCancel()
		}
	}
}

type telegramBotCommandsPayload struct {
	Commands []telegramBotCommand `json:"commands"`
}

type telegramBotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

func (s *Server) ensureTelegramBotCommands(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := setTelegramBotCommands(ctx, token); err != nil {
		if s != nil && s.logger != nil {
			s.logger.Printf("notifications: failed to set telegram commands: %v", err)
		}
	}
}

func setTelegramBotCommands(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("telegram token missing")
	}
	payload := telegramBotCommandsPayload{
		Commands: []telegramBotCommand{
			{Command: "scb", Description: "on-demand SCB backup"},
			{Command: "balances", Description: "on-demand financial summary"},
			{Command: "system", Description: "on-demand system status"},
			{Command: "alive", Description: "confirm succession proof-of-life"},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("telegram api status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

type telegramUpdateResponse struct {
	Ok     bool             `json:"ok"`
	Result []telegramUpdate `json:"result"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}

type telegramMessage struct {
	MessageID int64        `json:"message_id"`
	Text      string       `json:"text"`
	Chat      telegramChat `json:"chat"`
}

type telegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

func fetchTelegramUpdates(ctx context.Context, token string, offset int64) ([]telegramUpdate, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram token missing")
	}
	params := url.Values{}
	params.Set("timeout", "25")
	if offset > 0 {
		params.Set("offset", strconv.FormatInt(offset, 10))
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", token, params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("telegram api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload telegramUpdateResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if !payload.Ok {
		return nil, errors.New("telegram api error")
	}
	return payload.Result, nil
}

func telegramChatMatches(expectedChatID string, chat telegramChat) bool {
	if strings.TrimSpace(expectedChatID) == "" {
		return false
	}
	if strings.TrimSpace(chat.Type) != "private" {
		return false
	}
	return strconv.FormatInt(chat.ID, 10) == strings.TrimSpace(expectedChatID)
}

func parseTelegramCommand(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	cmd := strings.Fields(trimmed)[0]
	cmd = strings.TrimPrefix(cmd, "/")
	if idx := strings.Index(cmd, "@"); idx >= 0 {
		cmd = cmd[:idx]
	}
	return strings.ToLower(strings.TrimSpace(cmd))
}

func parseTelegramLivenessText(text string) string {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch normalized {
	case "estou vivo", "i am alive", "im alive", "alive", "vivo":
		return "alive"
	default:
		return ""
	}
}

func (s *Server) handleTelegramScbCommand(cfg telegramBackupConfig) {
	if s == nil || s.notifier == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.notifier.sendTelegramBackup(ctx, cfg, "command", "", ""); err != nil {
		if s.logger != nil {
			s.logger.Printf("notifications: telegram /scb failed: %v", err)
		}
	}
}

func (s *Server) handleTelegramBalancesCommand(cfg telegramBackupConfig) {
	if s == nil {
		return
	}
	buildCtx, buildCancel := context.WithTimeout(context.Background(), telegramBalanceSummaryTimeout())
	summary, err := s.buildTelegramBalanceSummary(buildCtx)
	buildCancel()
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("notifications: telegram /balances build failed: %v", err)
		}
		summary = "Unable to build balances summary."
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 20*time.Second)
	err = sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, summary)
	sendCancel()
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("notifications: telegram /balances send failed: %v", err)
		}
	}
}

func (s *Server) handleTelegramSystemCommand(cfg telegramBackupConfig) {
	if s == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	summary, err := s.buildTelegramSystemSummary(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("notifications: telegram /system build failed: %v", err)
		}
		summary = "Unable to build system summary."
	}
	if err := sendTelegramMessage(ctx, cfg.BotToken, cfg.ChatID, summary); err != nil {
		if s.logger != nil {
			s.logger.Printf("notifications: telegram /system send failed: %v", err)
		}
	}
}

func (s *Server) handleTelegramAliveCommand(cfg telegramBackupConfig) {
	if s == nil {
		return
	}
	svc, errMsg := s.successionService()
	if svc == nil {
		reply := "Succession mode unavailable."
		if strings.TrimSpace(errMsg) != "" {
			reply = "Succession mode unavailable: " + strings.TrimSpace(errMsg)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = sendTelegramMessage(ctx, cfg.BotToken, cfg.ChatID, reply)
		cancel()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	next, err := svc.MarkAlive(ctx, "telegram")
	cancel()
	reply := "Proof-of-life confirmed."
	if err != nil {
		reply = "Failed to confirm proof-of-life: " + err.Error()
	} else if next.NextCheckAt != nil {
		reply = "Proof-of-life confirmed. Next check: " + next.NextCheckAt.UTC().Format(time.RFC3339)
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, reply)
	sendCancel()
}

func (s *Server) buildTelegramBalanceSummary(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("server unavailable")
	}

	lines := []string{"⚡ LightningOS — Financial Summary"}

	alias := getNodeAlias(ctx, s.lnd)
	if strings.TrimSpace(alias) != "" {
		lines = append(lines, fmt.Sprintf("🔖 Node: %s", strings.TrimSpace(alias)))
	}

	if s.lnd == nil {
		lines = append(lines, "⚠️ Balances: unavailable (lnd unavailable)")
	} else {
		balancesCtx, balancesCancel := context.WithTimeout(ctx, 6*time.Second)
		balances, balErr := s.lnd.GetBalances(balancesCtx)
		balancesCancel()
		if balErr != nil {
			lines = append(lines, fmt.Sprintf("⚠️ Balances: unavailable (%s)", balErr.Error()))
		} else {
			onchainConfirmed := balances.OnchainConfirmedSat
			onchainUnconfirmed := balances.OnchainUnconfirmedSat
			lightningLocal := balances.LightningLocalSat
			lightningUnsettled := balances.LightningUnsettledLocalSat
			lightningTotal := balances.LightningLocalSat + balances.LightningUnsettledLocalSat

			lines = append(lines, fmt.Sprintf("⛓️ Onchain: %s sats", formatSats(onchainConfirmed)))
			if onchainUnconfirmed > 0 {
				lines = append(lines, fmt.Sprintf("⛓️ Onchain (unconfirmed): %s sats", formatSats(onchainUnconfirmed)))
			}
			lines = append(lines, fmt.Sprintf("⚡ Lightning: %s sats", formatSats(lightningLocal)))
			if lightningUnsettled > 0 {
				lines = append(lines, fmt.Sprintf("⚡ Lightning (unsettled): %s sats", formatSats(lightningUnsettled)))
				lines = append(lines, fmt.Sprintf("⚡ Lightning (total): %s sats", formatSats(lightningTotal)))
			}
			if len(balances.Warnings) > 0 {
				lines = append(lines, fmt.Sprintf("⚠️ Balances warning: %s", strings.Join(balances.Warnings, " ")))
			}
		}
	}

	svc, errMsg := s.reportsService()
	if svc == nil {
		msg := strings.TrimSpace(errMsg)
		if msg == "" {
			msg = "reports unavailable"
		}
		lines = append(lines, fmt.Sprintf("⚠️ Reports: %s", msg))
		return strings.Join(lines, "\n"), nil
	}

	loc := s.reportsLocation()
	now := time.Now()

	d1Line := "📅 D-1: unavailable"
	if metrics, err := reportSummaryMetrics(ctx, svc, reports.RangeD1, now, loc); err == nil {
		d1Line = "📅 " + formatReportLine("D-1", metrics)
	}
	liveLine := "📈 Live: unavailable"
	if metrics, err := reportLiveMetrics(ctx, svc, now, loc); err == nil {
		liveLine = "📈 " + formatReportLine("Live", metrics)
	}
	goalLine := "🎯 Goal: unavailable"
	if movement, err := reportLiveMovement(ctx, svc, now, loc); err == nil && movement.TargetSat > 0 {
		goalLine = fmt.Sprintf("🎯 Goal: %s %s %s",
			movementToneEmoji(movement.MovementPct),
			renderProgressBar(movement.MovementPct, telegramGoalBarSegments),
			formatProgressPercent(movement.MovementPct),
		)
	}
	monthLine := "🗓️ Month: unavailable"
	if metrics, err := reportSummaryMetrics(ctx, svc, reports.RangeMonth, now, loc); err == nil {
		monthLine = "🗓️ " + formatReportLine("Month", metrics)
	}
	prevMonthLine := "🗓️ Previous month: unavailable"
	if label, metrics, ok := reportMonthWindowMetrics(ctx, svc, now, loc, previousMonthWindow); ok {
		prevMonthLine = "🗓️ " + formatReportLine(label, metrics)
	}
	currentMonthLine := "🗓️ Current month: unavailable"
	if label, metrics, ok := reportMonthWindowMetrics(ctx, svc, now, loc, currentMonthWindow); ok {
		currentMonthLine = "🗓️ " + formatReportLine(label, metrics)
	}

	lines = append(lines, d1Line, liveLine, goalLine, monthLine, prevMonthLine, currentMonthLine)
	return strings.Join(lines, "\n"), nil
}

func (s *Server) buildTelegramSystemSummary(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("server unavailable")
	}

	lines := []string{"🖥️ LightningOS — System Status"}

	alias := getNodeAlias(ctx, s.lnd)
	if strings.TrimSpace(alias) != "" {
		lines = append(lines, fmt.Sprintf("🔖 Node: %s", strings.TrimSpace(alias)))
	}

	sysCtx, sysCancel := context.WithTimeout(ctx, 6*time.Second)
	stats, err := sysinfo.GetSystemStats(sysCtx)
	sysCancel()
	if err != nil {
		lines = append(lines, "⚠️ System: unavailable")
	} else {
		uptimeHours := int64(math.Round(float64(stats.UptimeSec) / 3600.0))
		lines = append(lines, fmt.Sprintf("🧠 CPU load: %.2f | CPU: %.1f%%", stats.CPULoad1, stats.CPUPercent))
		lines = append(lines, fmt.Sprintf("🧮 RAM: %s / %s MB", formatInt(stats.RAMUsedMB), formatInt(stats.RAMTotalMB)))
		lines = append(lines, fmt.Sprintf("⏱️ Uptime: %s hours", formatInt(uptimeHours)))
		if stats.TemperatureC > 0 {
			lines = append(lines, fmt.Sprintf("🌡️ Temp: %.1f C", stats.TemperatureC))
		} else {
			lines = append(lines, "🌡️ Temp: N/A")
		}
	}

	diskCtx, diskCancel := context.WithTimeout(ctx, 8*time.Second)
	disks, diskErr := sysinfo.ReadDiskSmart(diskCtx)
	diskCancel()
	if diskErr != nil {
		lines = append(lines, "💾 Disks: unavailable")
		return strings.Join(lines, "\n"), nil
	}
	if len(disks) == 0 {
		lines = append(lines, "💾 Disks: none detected")
		return strings.Join(lines, "\n"), nil
	}

	wearWarnThreshold := 75
	tempWarnThreshold := 70.0
	lines = append(lines, "💾 Disks:")
	for _, disk := range disks {
		header := fmt.Sprintf("💽 %s (%s) SMART %s", strings.TrimSpace(disk.Device), strings.TrimSpace(disk.Type), strings.TrimSpace(disk.SmartStatus))
		if header != "" {
			lines = append(lines, header)
		}
		lines = append(lines, fmt.Sprintf("  ⏳ Power on hours: %s", formatInt(disk.PowerOnHours)))
		lines = append(lines, fmt.Sprintf("  🧪 Wear: %d%% | Days left: %s", disk.WearPercentUsed, formatInt(disk.DaysLeftEstimate)))
		if disk.TemperatureC > 0 {
			lines = append(lines, fmt.Sprintf("  🌡️ Temp: %.1f C", disk.TemperatureC))
		} else {
			lines = append(lines, "  🌡️ Temp: N/A")
		}
		if disk.TotalGB > 0 {
			used := formatFloat(disk.UsedGB, 1)
			total := formatFloat(disk.TotalGB, 1)
			percent := formatFloat(disk.UsedPercent, 1)
			lines = append(lines, fmt.Sprintf("  📊 Usage: %s / %s GB (%s%%)", used, total, percent))
		} else {
			lines = append(lines, "  📊 Usage: N/A")
		}
		alerts := append([]string{}, disk.Alerts...)
		if disk.WearPercentUsed >= wearWarnThreshold && !containsAlert(alerts, "wear_warn") && !containsAlert(alerts, "wear_err") {
			alerts = append(alerts, "wear_warn")
		}
		if disk.TemperatureC >= tempWarnThreshold {
			alerts = append(alerts, "temp_warn")
		}
		if len(alerts) > 0 {
			lines = append(lines, fmt.Sprintf("  ⚠️ Alerts: %s", strings.Join(uniqueStrings(alerts), ", ")))
		}
		if len(disk.Partitions) > 0 {
			lines = append(lines, "  📦 Partitions:")
			for _, part := range disk.Partitions {
				partLabel := strings.TrimSpace(part.Device)
				if part.Mount != "" {
					partLabel = fmt.Sprintf("%s %s", partLabel, part.Mount)
				}
				total := formatFloat(part.TotalGB, 1)
				used := formatFloat(part.UsedGB, 1)
				percent := formatFloat(part.UsedPercent, 1)
				lines = append(lines, fmt.Sprintf("   • %s: %s / %s GB (%s%%)", partLabel, used, total, percent))
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}

func reportSummaryMetrics(ctx context.Context, svc *reports.Service, key string, now time.Time, loc *time.Location) (reports.Metrics, error) {
	summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	summary, _, err := svc.Summary(summaryCtx, key, now, loc)
	if err != nil {
		return reports.Metrics{}, err
	}
	return summary.Totals, nil
}

type monthWindowMode string

const (
	previousMonthWindow monthWindowMode = "previous"
	currentMonthWindow  monthWindowMode = "current"
)

func reportMonthWindowMetrics(ctx context.Context, svc *reports.Service, now time.Time, loc *time.Location, mode monthWindowMode) (string, reports.Metrics, bool) {
	if loc == nil {
		loc = time.Local
	}
	from, to, label, ok := resolveMonthWindow(now, loc, mode)
	if !ok {
		return "", reports.Metrics{}, false
	}
	summaryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	summary, err := svc.CustomSummary(summaryCtx, from, to)
	if err != nil {
		return "", reports.Metrics{}, false
	}
	return label, summary.Totals, true
}

func resolveMonthWindow(now time.Time, loc *time.Location, mode monthWindowMode) (time.Time, time.Time, string, bool) {
	localNow := now.In(loc)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	yesterday := today.AddDate(0, 0, -1)
	switch mode {
	case previousMonthWindow:
		monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
		start := monthStart.AddDate(0, -1, 0)
		end := monthStart.AddDate(0, 0, -1)
		return start, end, "Previous month (" + start.Format("Jan 2006") + ")", true
	case currentMonthWindow:
		start := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, loc)
		if yesterday.Before(start) {
			return time.Time{}, time.Time{}, "", false
		}
		return start, yesterday, "Current month (" + start.Format("Jan 2006") + ")", true
	default:
		return time.Time{}, time.Time{}, "", false
	}
}

func reportLiveMetrics(ctx context.Context, svc *reports.Service, now time.Time, loc *time.Location) (reports.Metrics, error) {
	liveCtx, cancel := context.WithTimeout(ctx, reportsLiveTimeout())
	defer cancel()
	_, metrics, err := svc.Live(liveCtx, now, loc, reportsLiveLookbackHours())
	if err != nil {
		return reports.Metrics{}, err
	}
	return metrics, nil
}

func reportLiveMovement(ctx context.Context, svc *reports.Service, now time.Time, loc *time.Location) (reports.MovementLive, error) {
	liveCtx, cancel := context.WithTimeout(ctx, reportsLiveTimeout())
	defer cancel()
	movement, err := svc.MovementLive(liveCtx, now, loc)
	if err != nil {
		return reports.MovementLive{}, err
	}
	return movement, nil
}

func formatReportLine(label string, metrics reports.Metrics) string {
	return fmt.Sprintf("%s: Forwards %s sats | Cost %s sats | Profit %s sats",
		label,
		formatSats(metrics.ForwardFeeRevenueSat),
		formatSats(metrics.TotalFeeCostSat()),
		formatSats(metrics.NetRoutingProfitSat),
	)
}

func formatSats(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	raw := strconv.FormatInt(value, 10)
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	prefix := len(raw) % 3
	if prefix == 0 {
		prefix = 3
	}
	b.WriteString(raw[:prefix])
	for i := prefix; i < len(raw); i += 3 {
		b.WriteByte(',')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

func formatInt(value int64) string {
	return formatIntWithSign(value)
}

func formatIntWithSign(value int64) string {
	negative := value < 0
	if negative {
		value = -value
	}
	raw := strconv.FormatInt(value, 10)
	if len(raw) <= 3 {
		if negative {
			return "-" + raw
		}
		return raw
	}
	var b strings.Builder
	if negative {
		b.WriteByte('-')
	}
	prefix := len(raw) % 3
	if prefix == 0 {
		prefix = 3
	}
	b.WriteString(raw[:prefix])
	for i := prefix; i < len(raw); i += 3 {
		b.WriteByte(',')
		b.WriteString(raw[i : i+3])
	}
	return b.String()
}

func formatFloat(value float64, decimals int) string {
	if value == 0 {
		return "0"
	}
	if decimals < 0 {
		decimals = 0
	}
	formatted := fmt.Sprintf("%.*f", decimals, value)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "" || formatted == "-" {
		return "0"
	}
	return formatted
}

func formatProgressPercent(value float64) string {
	if value < 0 {
		value = 0
	}
	return fmt.Sprintf("%.1f%%", value)
}

func renderProgressBar(value float64, segments int) string {
	if segments <= 0 {
		segments = 10
	}
	clamped := value
	if clamped < 0 {
		clamped = 0
	}
	if clamped > 100 {
		clamped = 100
	}
	filled := int(math.Round((clamped / 100) * float64(segments)))
	if filled < 0 {
		filled = 0
	}
	if filled > segments {
		filled = segments
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", segments-filled) + "]"
}

func movementToneEmoji(value float64) string {
	if value >= 75 {
		return "🟢"
	}
	if value >= 50 {
		return "🟡"
	}
	return "🔴"
}

func containsAlert(alerts []string, value string) bool {
	for _, alert := range alerts {
		if alert == value {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, val := range values {
		trimmed := strings.TrimSpace(val)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func telegramActivityMirrorMessage(evt Notification) string {
	typeLabel := telegramNotificationTypeLabel(evt.Type)
	header := typeLabel
	actionLabel := telegramNotificationActionLabel(evt.Action)
	if override := telegramNotificationStatusActionLabel(evt); override != "" {
		actionLabel = override
	}
	if evt.Type != "forward" && evt.Type != "rebalance" && actionLabel != "" {
		header = strings.TrimSpace(typeLabel + " " + actionLabel)
	}
	if header == "" {
		header = "Notification"
	}

	prefix := header
	if emoji := telegramNotificationTypeEmoji(evt.Type); emoji != "" {
		prefix = emoji + " " + prefix
	}
	if evt.Type == "onchain" {
		if state := telegramOnchainStateLabel(evt.Status); state != "" {
			prefix = fmt.Sprintf("%s (%s)", prefix, state)
		}
	}

	main := fmt.Sprintf("%s %s sats", prefix, formatIntWithSign(evt.AmountSat))
	if route := telegramNotificationRoute(evt); route != "" {
		main += " - " + route
	}
	if detail := telegramNotificationChannelDetail(evt); detail != "" {
		main += " | " + detail
	}
	if evt.Type == "keysend" {
		if memoDetail := telegramNotificationMemoDetail(evt); memoDetail != "" {
			main += " | " + memoDetail
		}
	}
	if feeDetail := telegramNotificationFeeDetail(evt); feeDetail != "" {
		main += " | " + feeDetail
	}

	msg := main
	if len(msg) > 3900 {
		return strings.TrimSpace(msg[:3897]) + "..."
	}
	return msg
}

func telegramNotificationStatusActionLabel(evt Notification) string {
	switch strings.TrimSpace(evt.Type) {
	case "lightning", "keysend":
		switch strings.ToUpper(strings.TrimSpace(evt.Status)) {
		case "FAILED":
			return "failed"
		case "IN_FLIGHT":
			return "pending"
		}
	}
	return ""
}

func telegramNotificationChannelDetail(evt Notification) string {
	if evt.Type == "lightning" || evt.Type == "keysend" {
		alias := strings.TrimSpace(evt.ChannelAlias)
		if alias == "" {
			return ""
		}
		return "Via " + telegramShortValue(alias, 48)
	}
	if evt.Type != "channel" {
		return ""
	}
	if evt.Action != "opening" {
		return ""
	}
	if strings.ToUpper(strings.TrimSpace(evt.Status)) != "BALANCED_START" {
		return ""
	}
	return strings.TrimSpace(evt.Memo)
}

func telegramNotificationTypeEmoji(value string) string {
	switch strings.TrimSpace(value) {
	case "onchain":
		return "⛓️"
	case "lightning":
		return "⚡"
	case "keysend":
		return "💬"
	case "channel":
		return "🔗"
	case "forward":
		return "💰"
	case "rebalance":
		return "♻️"
	default:
		return "🔔"
	}
}

func telegramNotificationTypeLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "onchain":
		return "On-chain"
	case "lightning":
		return "Lightning"
	case "channel":
		return "Channel"
	case "forward":
		return "Forwarded"
	case "rebalance":
		return "Rebalanced"
	case "keysend":
		return "Keysend"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return ""
		}
		return strings.ToUpper(trimmed[:1]) + trimmed[1:]
	}
}

func telegramNotificationActionLabel(value string) string {
	switch strings.TrimSpace(value) {
	case "receive":
		return "received"
	case "send":
		return "sent"
	case "open":
		return "opened"
	case "close":
		return "closed"
	case "opening":
		return "opening"
	case "closing":
		return "closing"
	case "forwarded":
		return "forwarded"
	case "rebalanced":
		return "rebalanced"
	default:
		return strings.TrimSpace(value)
	}
}

func telegramNotificationRoute(evt Notification) string {
	route := strings.TrimSpace(evt.PeerAlias)
	if route == "" {
		route = shortPubKey(evt.PeerPubkey)
	}
	if route == "" {
		return ""
	}
	if evt.Type == "forward" {
		left, right, ok := splitTelegramRoute(route)
		if ok {
			if left == "" {
				left = "?"
			}
			if right == "" {
				right = "?"
			}
			return fmt.Sprintf("In %s %s Out %s", left, telegramRouteArrowEmoji(), right)
		}
		return fmt.Sprintf("In %s", route)
	}
	route = strings.ReplaceAll(route, "->", " "+telegramRouteArrowEmoji()+" ")
	route = strings.Join(strings.Fields(route), " ")
	return strings.TrimSpace(route)
}

func telegramNotificationMemoDetail(evt Notification) string {
	if evt.Type != "lightning" && evt.Type != "keysend" {
		return ""
	}
	memo := strings.TrimSpace(evt.Memo)
	if memo == "" {
		return ""
	}
	return "Memo " + telegramTrimMemo(memo, 48)
}

func telegramFeeMsatTotal(feeSat, feeMsat int64) int64 {
	if feeMsat > 0 {
		return feeMsat
	}
	if feeSat > 0 {
		return feeSat * 1000
	}
	return 0
}

func telegramNotificationFeeDisplay(feeSat, feeMsat int64) string {
	totalMsat := telegramFeeMsatTotal(feeSat, feeMsat)
	if totalMsat <= 0 {
		return ""
	}
	return fmt.Sprintf("%.2f sats", float64(totalMsat)/1000.0)
}

func telegramNotificationFeeRate(amountSat, feeSat, feeMsat int64) string {
	if amountSat <= 0 {
		return ""
	}
	totalMsat := telegramFeeMsatTotal(feeSat, feeMsat)
	if totalMsat <= 0 {
		return ""
	}
	feeSatTotal := float64(totalMsat) / 1000.0
	ratio := feeSatTotal / float64(amountSat)
	percent := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", ratio*100), "0"), ".")
	ppm := int64(math.Round(ratio * 1_000_000))
	return fmt.Sprintf("%s%% %dppm", percent, ppm)
}

func telegramNotificationFeeDetail(evt Notification) string {
	fee := telegramNotificationFeeDisplay(evt.FeeSat, evt.FeeMsat)
	if fee == "" {
		return ""
	}
	rate := telegramNotificationFeeRate(evt.AmountSat, evt.FeeSat, evt.FeeMsat)
	if evt.Type == "forward" {
		if rate != "" {
			return fmt.Sprintf("Earned %s (%s)", fee, rate)
		}
		return fmt.Sprintf("Earned %s", fee)
	}
	if evt.Type == "rebalance" {
		if rate != "" {
			return fmt.Sprintf("Paid %s (%s)", fee, rate)
		}
		return fmt.Sprintf("Paid %s", fee)
	}
	if rate != "" {
		return fmt.Sprintf("fee %s (%s)", fee, rate)
	}
	return fmt.Sprintf("fee %s", fee)
}

func splitTelegramRoute(value string) (string, string, bool) {
	parts := strings.SplitN(value, "->", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func telegramRouteArrowEmoji() string {
	return "→"
}

func telegramOnchainStateLabel(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PENDING":
		return "Pending"
	case "CONFIRMED":
		return "Confirmed"
	default:
		return ""
	}
}

func telegramTrimMemo(value string, max int) string {
	if max <= 0 {
		max = 48
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= max {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:max]) + "..."
}

func telegramShortValue(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "..."
}

func sendTelegramMessage(ctx context.Context, token, chatID, text string) error {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(chatID) == "" {
		return errors.New("telegram config missing")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("empty message")
	}

	form := url.Values{}
	form.Set("chat_id", strings.TrimSpace(chatID))
	form.Set("text", text)

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("telegram api status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func splitTelegramMessage(text string, maxChars int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if maxChars <= 0 {
		maxChars = telegramMessageMaxChars
	}
	lines := strings.Split(text, "\n")
	chunks := make([]string, 0, 1)
	current := strings.Builder{}
	flush := func() {
		if current.Len() == 0 {
			return
		}
		chunks = append(chunks, current.String())
		current.Reset()
	}
	appendPiece := func(piece string) {
		if piece == "" {
			return
		}
		if current.Len() == 0 {
			current.WriteString(piece)
			return
		}
		if telegramTextLen(current.String())+1+telegramTextLen(piece) <= maxChars {
			current.WriteString("\n")
			current.WriteString(piece)
			return
		}
		flush()
		current.WriteString(piece)
	}
	splitLongLine := func(line string) []string {
		if telegramTextLen(line) <= maxChars {
			return []string{line}
		}
		runes := []rune(line)
		parts := make([]string, 0, (len(runes)/maxChars)+1)
		for len(runes) > 0 {
			take := maxChars
			if len(runes) < take {
				take = len(runes)
			}
			parts = append(parts, string(runes[:take]))
			runes = runes[take:]
		}
		return parts
	}
	for _, line := range lines {
		if line == "" {
			appendPiece("")
			continue
		}
		for _, part := range splitLongLine(line) {
			appendPiece(part)
		}
	}
	flush()
	return chunks
}

func telegramTextLen(value string) int {
	return utf8.RuneCountInString(value)
}

func sendTelegramMessages(ctx context.Context, token, chatID, text string) error {
	chunks := splitTelegramMessage(text, telegramMessageMaxChars)
	if len(chunks) == 0 {
		return nil
	}
	for _, chunk := range chunks {
		if err := sendTelegramMessage(ctx, token, chatID, chunk); err != nil {
			return err
		}
	}
	return nil
}
