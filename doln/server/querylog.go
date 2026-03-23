package main

import (
	"sync"
	"time"
)

type queryEntry struct {
	Timestamp    string `json:"timestamp"`
	SourcePubkey string `json:"source_pubkey"`
	Domain       string `json:"domain"`
	Status       string `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	ResponseSize int    `json:"response_size"`
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
	// Reverse so newest first
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
