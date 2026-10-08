package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

type logQuery struct {
	Service string    `json:"service"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
	Limit   int       `json:"limit"`
	Level   string    `json:"level"`
	Event   string    `json:"event"`
	Text    string    `json:"q"`
}

type logEntry struct {
	Time    string     `json:"time,omitempty"`
	Message string     `json:"message"`
	Level   string     `json:"level"`
	Event   string     `json:"event"`
	Context []logEntry `json:"context,omitempty"`
}

type logCapabilities struct {
	Period  bool `json:"period"`
	Filters bool `json:"filters"`
	Events  bool `json:"events"`
	Context bool `json:"context"`
}

type logQueryResponse struct {
	Query         logQuery        `json:"query"`
	Source        string          `json:"source"`
	Entries       []logEntry      `json:"entries"`
	Capabilities  logCapabilities `json:"capabilities"`
	Scanned       int             `json:"scanned"`
	Matched       int             `json:"matched"`
	Partial       bool            `json:"partial"`
	Reason        string          `json:"reason,omitempty"`
	ResultLimited bool            `json:"result_limited"`
	QueriedAt     string          `json:"queried_at"`
}

func parseLogQuery(values url.Values, now time.Time) (logQuery, error) {
	q := logQuery{Service: strings.TrimSpace(values.Get("service")), Until: now.UTC(), Limit: 200, Level: values.Get("level"), Event: values.Get("event"), Text: strings.TrimSpace(values.Get("q"))}
	if mapService(q.Service) == "" && !isBitcoinLogService(q.Service) && !isFedimintLogService(q.Service) {
		return q, errors.New("unsupported service")
	}
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 1000 {
			return q, errors.New("limit must be between 1 and 1000")
		}
		q.Limit = limit
	}
	if len(q.Text) > 256 {
		return q, errors.New("search text exceeds 256 bytes")
	}
	if !oneOf(q.Level, "", "error", "warning", "info", "debug", "unknown") {
		return q, errors.New("unsupported log level")
	}
	if !oneOf(q.Event, "", "connection", "channel_open", "cooperative_close", "force_close", "lifecycle", "sync", "other") {
		return q, errors.New("unsupported log event")
	}
	if q.Event != "" && q.Service != "lnd" {
		return q, errors.New("event filters are only available for LND")
	}
	if values.Get("since") != "" || values.Get("until") != "" {
		var err error
		q.Since, err = time.Parse(time.RFC3339Nano, values.Get("since"))
		if err != nil {
			return q, errors.New("since must be an RFC3339 timestamp with timezone")
		}
		q.Until, err = time.Parse(time.RFC3339Nano, values.Get("until"))
		if err != nil {
			return q, errors.New("until must be an RFC3339 timestamp with timezone")
		}
		if q.Until.Before(q.Since) || q.Until.Sub(q.Since) > 7*24*time.Hour {
			return q, errors.New("period must be ordered and no longer than 7 days")
		}
		q.Since, q.Until = q.Since.UTC(), q.Until.UTC()
	}
	return q, nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func (s *Server) handleLogsQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q, err := parseLogQuery(r.URL.Query(), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	result, err := s.queryLogs(ctx, q)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, errLogQueryUnsupported) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

var errLogQueryUnsupported = errors.New("this source supports only recent logs without filters")

func (s *Server) queryLogs(ctx context.Context, q logQuery) (logQueryResponse, error) {
	caps := logCapabilities{Period: true, Filters: true, Events: q.Service == "lnd", Context: q.Service == "lnd"}
	unit := mapService(q.Service)
	var batch system.JournalQueryResult
	var err error
	source := "systemd:" + unit
	docker := isFedimintLogService(q.Service)
	if isBitcoinLogService(q.Service) {
		docker = fileExists(bitcoinCoreAppPaths().ComposePath)
		if !docker {
			unit = bitcoinSystemdLogService(ctx)
			if unit == "" {
				unit = "bitcoind"
			}
			source = "systemd:" + unit
		}
	}
	if docker {
		if !q.Since.IsZero() || q.Level != "" || q.Event != "" || q.Text != "" {
			return logQueryResponse{}, errLogQueryUnsupported
		}
		var lines []string
		if isBitcoinLogService(q.Service) {
			lines, source, err = s.readBitcoinLocalLogLines(ctx, min(q.Limit, 500), "")
		} else {
			lines, source, err = readFedimintComposeLogLines(ctx, q.Service, min(q.Limit, 500), "")
		}
		if err != nil {
			return logQueryResponse{}, errors.New("recent app logs unavailable")
		}
		for _, line := range lines {
			batch.Entries = append(batch.Entries, system.JournalEntry{Message: line})
		}
		batch.Partial, batch.Reason = true, "source_tail"
		caps = logCapabilities{}
	} else if unit == "autofee" {
		source = "database:autofee"
		batch, err = s.queryAutofeeLogs(ctx, q)
	} else {
		batch, err = system.QueryJournal(ctx, unit, q.Since, q.Until)
	}
	if err != nil {
		return logQueryResponse{}, errors.New("log query unavailable")
	}
	return selectLogEntries(q, source, caps, batch), nil
}

func (s *Server) queryAutofeeLogs(ctx context.Context, q logQuery) (system.JournalQueryResult, error) {
	result := system.JournalQueryResult{Entries: []system.JournalEntry{}}
	if s.db == nil {
		return result, errors.New("database unavailable")
	}
	var since any
	if !q.Since.IsZero() {
		since = q.Since
	}
	// Filter grouped runs, preserving the summary+seed meaning of the existing
	// Autofee log view. The output limit is applied later, after text/level filters.
	rows, err := s.db.Query(ctx, `
select max(occurred_at),
  left(max(case when seq = 1 then line else '' end), 8193),
  left(max(case when seq = 2 then line else '' end), 8193)
from autofee_logs where seq in (1,2)
group by run_id
having ($1::timestamptz is null or max(occurred_at) >= $1) and max(occurred_at) <= $2
order by max(occurred_at) desc, max(id) desc limit $3`, since, q.Until, system.JournalQueryScanLimit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		if len(result.Entries) == system.JournalQueryScanLimit {
			result.Partial, result.Reason = true, "scan_limit"
			break
		}
		var stamp time.Time
		var summary, seed string
		if err := rows.Scan(&stamp, &summary, &seed); err != nil {
			return result, err
		}
		message := strings.TrimSpace(summary)
		if strings.TrimSpace(seed) != "" {
			message += " | " + strings.TrimSpace(seed)
		}
		if len(message) > 8192 {
			message = strings.ToValidUTF8(message[:8192], "") + " [truncated]"
			result.Partial, result.Reason = true, "record_limit"
		}
		total += len(message)
		if total > 16*1024*1024 {
			result.Partial, result.Reason = true, "byte_limit"
			break
		}
		result.Entries = append(result.Entries, system.JournalEntry{Time: stamp, Message: message})
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	for i, j := 0, len(result.Entries)-1; i < j; i, j = i+1, j-1 {
		result.Entries[i], result.Entries[j] = result.Entries[j], result.Entries[i]
	}
	return result, nil
}

func selectLogEntries(q logQuery, source string, caps logCapabilities, batch system.JournalQueryResult) logQueryResponse {
	result := logQueryResponse{Query: q, Source: source, Entries: []logEntry{}, Capabilities: caps, Scanned: len(batch.Entries), Partial: batch.Partial, Reason: batch.Reason, QueriedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	entries := make([]logEntry, 0, len(batch.Entries))
	for _, raw := range batch.Entries {
		if !raw.Time.IsZero() && ((!q.Since.IsZero() && raw.Time.Before(q.Since)) || raw.Time.After(q.Until)) {
			continue
		}
		message := safeQueryLogMessage(raw.Message)
		if mapService(q.Service) == appUpgradeUnitName && len(filterExpectedAppUpgradeJournalLines([]string{message})) == 0 {
			continue
		}
		entry := logEntry{Message: message, Level: classifyLogLevel(message, raw.Priority), Event: "other"}
		if !raw.Time.IsZero() {
			entry.Time = raw.Time.UTC().Format(time.RFC3339Nano)
		}
		if caps.Events {
			entry.Event = classifyLNDEvent(message)
		}
		entries = append(entries, entry)
	}
	indexes := []int{}
	for i, entry := range entries {
		if q.Level != "" && entry.Level != q.Level || q.Event != "" && entry.Event != q.Event || q.Text != "" && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(q.Text)) {
			continue
		}
		indexes = append(indexes, i)
	}
	result.Matched = len(indexes)
	if len(indexes) > q.Limit {
		result.ResultLimited = true
		indexes = indexes[len(indexes)-q.Limit:]
	}
	bytes := 0
	for n := len(indexes) - 1; n >= 0; n-- {
		i := indexes[n]
		entry := entries[i]
		if caps.Context {
			// Context comes from the same sanitized snapshot, not a second query
			// that could drift after rotation. It remains inside the chosen period.
			entry.Context = append([]logEntry(nil), entries[max(0, i-3):min(len(entries), i+4)]...)
		}
		bytes += len(entry.Message) + 256
		for _, line := range entry.Context {
			bytes += len(line.Message) + 256
		}
		if bytes > 4*1024*1024 {
			result.ResultLimited = true
			break
		}
		result.Entries = append(result.Entries, entry)
	}
	for i, j := 0, len(result.Entries)-1; i < j; i, j = i+1, j-1 {
		result.Entries[i], result.Entries[j] = result.Entries[j], result.Entries[i]
	}
	return result
}

var queryLogLevelPattern = regexp.MustCompile(`(?i)(?:\[(ERR|ERROR|WRN|WARN|WARNING|INF|INFO|DBG|DEBUG|TRC|TRACE|FTL|FATAL)\]|\b(ERROR|WARNING|WARN|INFO|DEBUG|TRACE|FATAL|PANIC):)`)
var queryBearerPattern = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/=-]+`)
var querySensitiveLinePattern = regexp.MustCompile(`(?i)(seed\s*(words|phrase)|\bseed\s*[=:]|mnemonic|private[ _-]?key|BEGIN .*PRIVATE KEY|secrets\.env|\b(?:rpcuser|rpcpass|rpcauth)\b)`)
var queryTelegramPattern = regexp.MustCompile(`\b(?:bot)?[0-9]{6,}:[A-Za-z0-9_-]{20,}\b`)
var queryJSONSecretPattern = regexp.MustCompile(`(?i)("[^"\n]*(?:password|passwd|token|secret|macaroon|credential|adminpw)[^"\n]*"\s*:\s*)"(?:\\.|[^"\\])*"`)

func safeQueryLogMessage(message string) string {
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	// Redact the whole record when it might carry key/seed material, including
	// continuations. Never export those lines as a diagnostic artifact.
	if querySensitiveLinePattern.MatchString(message) {
		return "[redacted sensitive log record]"
	}
	lines = redactSystemCheckLogLines(sanitizeLogLines(lines))
	for i := range lines {
		lines[i] = queryJSONSecretPattern.ReplaceAllString(lines[i], `$1"[redacted]"`)
		lines[i] = queryBearerPattern.ReplaceAllString(lines[i], "Bearer [redacted]")
		lines[i] = queryTelegramPattern.ReplaceAllString(lines[i], "[redacted]")
	}
	return strings.Join(lines, "\n")
}

func classifyLogLevel(message, priority string) string {
	if match := queryLogLevelPattern.FindStringSubmatch(message); len(match) > 0 {
		switch strings.ToUpper(match[1] + match[2]) {
		case "ERR", "ERROR", "FTL", "FATAL", "PANIC":
			return "error"
		case "WRN", "WARN", "WARNING":
			return "warning"
		case "INF", "INFO":
			return "info"
		case "DBG", "DEBUG", "TRC", "TRACE":
			return "debug"
		}
	}
	switch priority {
	case "0", "1", "2", "3":
		return "error"
	case "4":
		return "warning"
	case "5", "6":
		return "info"
	case "7":
		return "debug"
	}
	return "unknown"
}

// Categories describe log messages (including attempts/failures), never prove
// that a channel operation succeeded. Keep specific close patterns first.
func classifyLNDEvent(message string) string {
	lower := strings.ToLower(message)
	for _, rule := range []struct {
		event   string
		phrases []string
	}{
		{"force_close", []string{"force clos", "force-clos", "forceclos", "unilateral clos", "breach"}},
		{"cooperative_close", []string{"cooperative clos", "cooperatively clos", "coop clos", "coopclos", "negotiat", "shutdown message"}},
		{"channel_open", []string{"funding_locked", "channel_ready", "channel ready", "funding transaction", "funding txn", "funding workflow", "opening channel", "open channel", "channel open", "new channel", "pending channel"}},
		{"connection", []string{"unable to connect", "failed to connect", "connection refused", "connection reset", "connection timeout", "connection timed out", "dial tcp", "dial proxy", "disconnecting", "peer disconnected", "peer connected", "reconnecting", "established connection", "lost connection"}},
		{"lifecycle", []string{"starting lnd", "version:", "shutdown complete", "shutting down", "gracefully shutting", "server is now active"}},
		{"sync", []string{"syncing", "synchroniz", "fully synced", "waiting for chain backend", "caught up to chain", "rescan"}},
	} {
		for _, phrase := range rule.phrases {
			if strings.Contains(lower, phrase) {
				if phrase == "negotiat" && !strings.Contains(lower, "clos") {
					continue
				}
				return rule.event
			}
		}
	}
	return "other"
}
