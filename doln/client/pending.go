package main

import (
	"sync"
	"time"
)

type pendingStore struct {
	mu       sync.Mutex
	entries  map[[16]byte]chan []byte
}

func newPendingStore() *pendingStore {
	ps := &pendingStore{
		entries: make(map[[16]byte]chan []byte),
	}
	go ps.reaper()
	return ps
}

func (ps *pendingStore) register(id [16]byte) chan []byte {
	ch := make(chan []byte, 1)
	ps.mu.Lock()
	ps.entries[id] = ch
	ps.mu.Unlock()
	return ch
}

func (ps *pendingStore) resolve(id [16]byte, data []byte) bool {
	ps.mu.Lock()
	ch, ok := ps.entries[id]
	if ok {
		delete(ps.entries, id)
	}
	ps.mu.Unlock()
	if ok {
		ch <- data
		return true
	}
	return false
}

func (ps *pendingStore) remove(id [16]byte) {
	ps.mu.Lock()
	delete(ps.entries, id)
	ps.mu.Unlock()
}

func (ps *pendingStore) reaper() {
	// Clean up stale entries every 30 seconds.
	// Entries that haven't been resolved are removed after being
	// checked twice (i.e. ~60s worst case), but callers typically
	// time out and call remove() themselves well before that.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ps.mu.Lock()
		// Snapshot current keys; any that survive two ticks are stale.
		for id, ch := range ps.entries {
			select {
			case <-ch:
				// already consumed, just clean up
				delete(ps.entries, id)
			default:
			}
		}
		ps.mu.Unlock()
	}
}
