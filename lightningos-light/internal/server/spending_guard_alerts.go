package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const spendingGuardAlertCooldown = 15 * time.Minute

type spendingGuardAlertState struct {
	LastSentAt time.Time
	Suppressed int
	Bucket     int64
	Attempts   int
}

type spendingGuardAlertDecision struct {
	SendTelegram bool
	Suppressed   int
	Bucket       int64
	Attempts     int
}

func (s *Server) spendingGuardAlertDecision(key string, now time.Time) spendingGuardAlertDecision {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	bucket := now.UTC().Unix() / int64(spendingGuardAlertCooldown/time.Second)

	s.spendingGuardAlertMu.Lock()
	defer s.spendingGuardAlertMu.Unlock()
	if s.spendingGuardAlerts == nil {
		s.spendingGuardAlerts = make(map[string]spendingGuardAlertState)
	}
	state := s.spendingGuardAlerts[key]
	if state.Bucket != bucket {
		state.Bucket = bucket
		state.Attempts = 0
	}
	state.Attempts++
	decision := spendingGuardAlertDecision{Bucket: bucket, Attempts: state.Attempts}
	if state.LastSentAt.IsZero() || now.Sub(state.LastSentAt) >= spendingGuardAlertCooldown {
		decision.SendTelegram = true
		decision.Suppressed = state.Suppressed
		state.LastSentAt = now
		state.Suppressed = 0
	} else {
		state.Suppressed++
	}
	s.spendingGuardAlerts[key] = state
	return decision
}

func (s *Server) handleSpendingGuardLimit(ctx context.Context, intent SpendingIntent, limitErr SpendingLimitError) {
	if s == nil {
		return
	}
	source := normalizeSpendingSource(intent.Source)
	if source == "" {
		source = "unknown"
	}
	reason := strings.TrimSpace(limitErr.Reason)
	if reason == "" {
		reason = "limit"
	}
	now := time.Now().UTC()
	decision := s.spendingGuardAlertDecision(source+":"+reason, now)
	sessionID := "system"
	if session, ok := authSessionFromContext(ctx); ok && strings.TrimSpace(session.ID) != "" {
		sessionID = session.ID
	}

	metadata := map[string]any{
		"source":            source,
		"reason":            reason,
		"amount_sat":        intent.AmountSat,
		"max_fee_sat":       intent.MaxFeeSat,
		"requested_sat":     limitErr.RequestedSat,
		"used_sat":          limitErr.UsedSat,
		"reserved_sat":      limitErr.ReservedSat,
		"limit_sat":         limitErr.LimitSat,
		"remaining_sat":     limitErr.RemainingSat,
		"payment_submitted": false,
	}

	go func() {
		s.insertAuditEvent(context.Background(), AuditEventInsert{
			SessionID: sessionID,
			Action:    "wallet.spending_guard.blocked",
			Target:    source,
			Metadata:  metadata,
		})

		if s.notifier != nil {
			nctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			eventKey := fmt.Sprintf("security:spending_guard:%s:%s:%d", source, reason, decision.Bucket)
			memo := spendingGuardNotificationMemo(source, limitErr, decision.Attempts)
			_, err := s.notifier.upsertNotificationWithOptions(nctx, eventKey, Notification{
				OccurredAt: now,
				Type:       "security",
				Action:     "spending_guard_blocked",
				Direction:  "out",
				Status:     "BLOCKED",
				AmountSat:  limitErr.RequestedSat,
				Memo:       memo,
			}, notificationUpsertOptions{suppressMirror: true})
			cancel()
			if err != nil && s.logger != nil {
				s.logger.Printf("spending guard: failed to persist blocked-payment notification: %v", err)
			}
		}

		if decision.SendTelegram {
			cfg := readTelegramBackupConfig()
			if cfg.configured() {
				sendCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				err := sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID, spendingGuardTelegramAlert(intent, limitErr, decision.Suppressed))
				cancel()
				if err != nil && s.logger != nil {
					s.logger.Printf("spending guard: telegram security alert failed: %v", err)
				}
			}
		}
	}()
}

func spendingGuardSourceLabel(source string) string {
	switch normalizeSpendingSource(source) {
	case "wallet":
		return "Wallet invoice"
	case "wallet_invoice":
		return "Wallet invoice"
	case "wallet_invoice_mpp":
		return "Wallet invoice (MPP)"
	case "wallet_validated_route":
		return "Wallet validated route"
	case "chat_keysend":
		return "Chat Keysend"
	case "loop_out_brln":
		return "Loop Out BRLN"
	default:
		label := strings.TrimSpace(strings.ReplaceAll(source, "_", " "))
		if label == "" {
			return "Unknown"
		}
		return label
	}
}

func spendingGuardReasonLabel(reason string) string {
	if strings.TrimSpace(reason) == "per_payment" {
		return "per-payment limit"
	}
	return "rolling 24-hour limit"
}

func spendingGuardNotificationMemo(source string, limitErr SpendingLimitError, attempts int) string {
	return fmt.Sprintf("%s blocked by %s; requested debit %d sats; limit %d sats; remaining %d sats; attempts in this 15-minute window: %d",
		spendingGuardSourceLabel(source), spendingGuardReasonLabel(limitErr.Reason), limitErr.RequestedSat, limitErr.LimitSat, limitErr.RemainingSat, attempts)
}

func spendingGuardTelegramAlert(intent SpendingIntent, limitErr SpendingLimitError, suppressed int) string {
	lines := []string{
		"🛡️ Lightning Spending Guard blocked a payment",
		"Source: " + spendingGuardSourceLabel(intent.Source),
		fmt.Sprintf("Requested debit: %d sats (amount %d + maximum fee %d)", limitErr.RequestedSat, intent.AmountSat, intent.MaxFeeSat),
		"Reason: " + spendingGuardReasonLabel(limitErr.Reason),
		fmt.Sprintf("Configured limit: %d sats", limitErr.LimitSat),
		fmt.Sprintf("Remaining allowance: %d sats", limitErr.RemainingSat),
		"Result: payment was not submitted to LND.",
	}
	if suppressed > 0 {
		lines = append(lines, fmt.Sprintf("Repeated attempts grouped since the previous alert: %d", suppressed))
	}
	return strings.Join(lines, "\n")
}
