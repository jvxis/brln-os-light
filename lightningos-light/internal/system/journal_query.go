package system

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// These are scan budgets, not result limits. Consumers filter before limiting
// results and must disclose Partial instead of claiming an exhaustive search.
const JournalQueryScanLimit = 50000
const journalQueryByteLimit = 16 * 1024 * 1024

type JournalEntry struct {
	Time     time.Time
	Message  string
	Priority string
}

type JournalQueryResult struct {
	Entries []JournalEntry
	Partial bool
	Reason  string
}

// QueryJournal reads the newest retained records in the requested interval.
// Only allowlisted unit names supplied by the server may reach this function.
// It is separate from JournalTailSince so existing upgrade callers keep their
// original formatting, relative-since semantics and bounded tail behavior.
func QueryJournal(ctx context.Context, unit string, since, until time.Time) (JournalQueryResult, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, "journalctl", journalQueryArgs(unit, since, until)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return JournalQueryResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return JournalQueryResult{}, errors.New("journal query unavailable")
	}
	result := decodeJournalQuery(stdout)
	if result.Partial {
		cancel()
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		result.Partial, result.Reason = true, "timeout"
	} else if err != nil && !result.Partial {
		return JournalQueryResult{}, errors.New("journal query failed")
	}
	return result, nil
}

func journalQueryArgs(unit string, since, until time.Time) []string {
	args := []string{"-u", unit, "--no-pager", "--quiet", "--reverse", "--output=json", "--all", "-n", strconv.Itoa(JournalQueryScanLimit + 1)}
	for _, bound := range []struct {
		flag  string
		value time.Time
	}{{"--since", since}, {"--until", until}} {
		if !bound.value.IsZero() {
			args = append(args, bound.flag, bound.value.UTC().Format("2006-01-02 15:04:05.999999 UTC"))
		}
	}
	return args
}

func decodeJournalQuery(reader io.Reader) JournalQueryResult {
	result := JournalQueryResult{Entries: []JournalEntry{}}
	limited := &io.LimitedReader{R: reader, N: journalQueryByteLimit}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if len(result.Entries) >= JournalQueryScanLimit {
			result.Partial, result.Reason = true, "scan_limit"
			break
		}
		var row struct {
			Timestamp string          `json:"__REALTIME_TIMESTAMP"`
			Message   json.RawMessage `json:"MESSAGE"`
			Priority  string          `json:"PRIORITY"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			result.Partial, result.Reason = true, "unreadable_record"
			continue
		}
		micros, err := strconv.ParseInt(row.Timestamp, 10, 64)
		if err != nil {
			result.Partial, result.Reason = true, "unreadable_record"
			continue
		}
		var message string
		if json.Unmarshal(row.Message, &message) != nil {
			// journald represents non-UTF8 fields as arrays of bytes.
			var bytes []byte
			if json.Unmarshal(row.Message, &bytes) != nil {
				result.Partial, result.Reason = true, "unreadable_record"
				continue
			}
			message = strings.ToValidUTF8(string(bytes), "�")
		}
		if len(message) > 8192 {
			message = strings.ToValidUTF8(message[:8192], "") + " [truncated]"
			result.Partial, result.Reason = true, "record_limit"
		}
		result.Entries = append(result.Entries, JournalEntry{Time: time.UnixMicro(micros).UTC(), Message: message, Priority: row.Priority})
	}
	if limited.N == 0 {
		result.Partial, result.Reason = true, "byte_limit"
	} else if scanner.Err() != nil {
		result.Partial, result.Reason = true, "record_limit"
	}
	// journalctl returns newest first to make scan limits useful; display and
	// context use chronological order without changing equal-timestamp ordering.
	for i, j := 0, len(result.Entries)-1; i < j; i, j = i+1, j-1 {
		result.Entries[i], result.Entries[j] = result.Entries[j], result.Entries[i]
	}
	return result
}
