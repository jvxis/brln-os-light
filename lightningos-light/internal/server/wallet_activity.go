package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	walletActivityPath       = "/var/lib/lightningos/wallet-activity.json"
	walletActivityLimit      = 200
	walletActivityFetchLimit = 20000
)

type walletActivityStore struct {
	Hashes []string `json:"hashes"`
}

func normalizeWalletHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
}

func (s *Server) recordWalletActivity(hash string) {
	normalized := normalizeWalletHash(hash)
	if normalized == "" {
		return
	}

	s.walletActivityMu.Lock()
	defer s.walletActivityMu.Unlock()

	store := s.readWalletActivityLocked()
	updated := make([]string, 0, walletActivityLimit)
	updated = append(updated, normalized)
	for _, existing := range store.Hashes {
		item := normalizeWalletHash(existing)
		if item == "" || item == normalized {
			continue
		}
		updated = append(updated, item)
		if len(updated) >= walletActivityLimit {
			break
		}
	}
	store.Hashes = updated
	if err := s.writeWalletActivityLocked(store); err != nil && s.logger != nil {
		s.logger.Printf("wallet activity: failed to persist: %v", err)
	}
}

func (s *Server) walletActivitySet() map[string]struct{} {
	s.walletActivityMu.Lock()
	defer s.walletActivityMu.Unlock()

	if !s.walletActivityWritable() {
		return map[string]struct{}{}
	}

	store := s.readWalletActivityLocked()
	hashes := make(map[string]struct{}, len(store.Hashes))
	for _, hash := range store.Hashes {
		normalized := normalizeWalletHash(hash)
		if normalized == "" {
			continue
		}
		hashes[normalized] = struct{}{}
	}
	return hashes
}

func (s *Server) walletActivityWritable() bool {
	f, err := os.OpenFile(walletActivityPath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func (s *Server) readWalletActivityLocked() walletActivityStore {
	content, err := os.ReadFile(walletActivityPath)
	if err != nil {
		return walletActivityStore{}
	}
	var store walletActivityStore
	if err := json.Unmarshal(content, &store); err != nil {
		return walletActivityStore{}
	}
	return store
}

func (s *Server) writeWalletActivityLocked(store walletActivityStore) error {
	if err := os.MkdirAll(filepath.Dir(walletActivityPath), 0750); err != nil {
		return err
	}
	payload, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(walletActivityPath, payload, 0660)
}
