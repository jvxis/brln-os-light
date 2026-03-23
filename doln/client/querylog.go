package main

import (
	"sync"
	"time"
)

type queryEntry struct {
	Timestamp    string `json:"timestamp"`
	Domain       string `json:"domain"`
	ServerPubkey string `json:"server_pubkey"`
	RequestID    string `json:"request_id"`
	Status       string `json:"status"`
	ResolvedIP   string `json:"resolved_ip,omitempty"`
	LatencyMs    int64  `json:"latency_ms"`
}

type queryLog struct {
	mu      sync.Mutex
	entries []queryEntry
	maxSize int
	total   int64
}

func newQueryLog(maxSize int) *queryLog {
	return &queryLog{
		entries: make([]queryEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

func (ql *queryLog) add(entry queryEntry) {
	ql.mu.Lock()
	defer ql.mu.Unlock()
	ql.total++
	if len(ql.entries) >= ql.maxSize {
		copy(ql.entries, ql.entries[1:])
		ql.entries[len(ql.entries)-1] = entry
	} else {
		ql.entries = append(ql.entries, entry)
	}
}

func (ql *queryLog) recent() []queryEntry {
	ql.mu.Lock()
	defer ql.mu.Unlock()
	result := make([]queryEntry, len(ql.entries))
	copy(result, ql.entries)
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func (ql *queryLog) totalCount() int64 {
	ql.mu.Lock()
	defer ql.mu.Unlock()
	return ql.total
}

func nowTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
