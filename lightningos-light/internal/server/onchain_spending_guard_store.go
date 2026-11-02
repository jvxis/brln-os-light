package server

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const onchainGuardFile = "onchain-spending-guard.state"
const onchainGuardMaxEntries = 4096
const onchainGuardMaxBytes = 16 << 20
const onchainGuardMaxSat int64 = 21_000_000 * 100_000_000

var errOnchainGuardUnavailable = errors.New("on-chain spending guard unavailable; no new send was submitted")

type onchainGuardSettings struct {
	Enabled            bool      `json:"enabled"`
	MaxSendSat         int64     `json:"max_send_sat"`
	Rolling24hLimitSat int64     `json:"rolling_24h_limit_sat"`
	UpdatedAt          time.Time `json:"updated_at,omitempty"`
}

type onchainGuardStatus struct {
	onchainGuardSettings
	UsedSat      int64 `json:"used_sat"`
	ReservedSat  int64 `json:"reserved_sat"`
	RemainingSat int64 `json:"remaining_sat"`
}

type onchainGuardEntry struct {
	ID            string                 `json:"id"`
	ReservedSat   int64                  `json:"reserved_sat"`
	SpentSat      int64                  `json:"spent_sat"`
	State         string                 `json:"state"`
	CreatedAt     time.Time              `json:"created_at"`
	SettledAt     time.Time              `json:"settled_at,omitempty"`
	TxID          string                 `json:"txid,omitempty"`
	Binding       financialIntentBinding `json:"binding"`
	OriginalLabel string                 `json:"original_label,omitempty"`
}

type onchainGuardState struct {
	Version  int                          `json:"version"`
	Settings onchainGuardSettings         `json:"settings"`
	Entries  map[string]onchainGuardEntry `json:"entries"`
}

type onchainGuardLimitError struct {
	Reason               string
	Requested, Remaining int64
}

func (e *onchainGuardLimitError) Error() string {
	return "on-chain send exceeds the configured spending limit; no transaction was submitted"
}

func validateOnchainGuardSettings(s onchainGuardSettings) error {
	if s.MaxSendSat < 0 || s.Rolling24hLimitSat < 0 || s.MaxSendSat > onchainGuardMaxSat || s.Rolling24hLimitSat > onchainGuardMaxSat {
		return errors.New("on-chain limits must be between zero and 2100000000000000 sats")
	}
	if s.Enabled && s.MaxSendSat == 0 && s.Rolling24hLimitSat == 0 {
		return errors.New("at least one on-chain spending limit is required")
	}
	return nil
}

func onchainGuardNeedsReauth(current, next onchainGuardSettings) bool {
	return spendingGuardNeedsReauth(SpendingGuardSettings{Enabled: current.Enabled, MaxPaymentSat: current.MaxSendSat, Rolling24hLimitSat: current.Rolling24hLimitSat}, SpendingGuardSettings{Enabled: next.Enabled, MaxPaymentSat: next.MaxSendSat, Rolling24hLimitSat: next.Rolling24hLimitSat})
}

// The existing financial-intent process lock and mutex also serialize this
// separate ledger. A private .state file is deliberately not an intent .json.
// It contains no seed, macaroon, password or signed transaction.
func (s *financialIntentStore) loadOnchainGuardLocked() (*onchainGuardState, error) {
	if s.lock == nil || s.onchainGuardErr != nil {
		return nil, errOnchainGuardUnavailable
	}
	if s.onchainGuard != nil {
		return s.onchainGuard, nil
	}
	path := filepath.Join(s.dir, onchainGuardFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		s.onchainGuard = &onchainGuardState{Version: 1, Entries: make(map[string]onchainGuardEntry)}
		return s.onchainGuard, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > onchainGuardMaxBytes || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errOnchainGuardUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errOnchainGuardUnavailable
	}
	defer file.Close()
	var state onchainGuardState
	decoder := json.NewDecoder(io.LimitReader(file, onchainGuardMaxBytes+1))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF || state.Version != 1 || state.Entries == nil || len(state.Entries) > onchainGuardMaxEntries || validateOnchainGuardSettings(state.Settings) != nil {
		return nil, errOnchainGuardUnavailable
	}
	for id, entry := range state.Entries {
		if !financialIntentIDValid(id) || entry.ID != id || entry.ReservedSat <= 0 || entry.ReservedSat > onchainGuardMaxSat || entry.SpentSat < 0 || entry.SpentSat > entry.ReservedSat || entry.CreatedAt.IsZero() || entry.Binding.Kind != "onchain_send" || entry.Binding.NativeLabel != "los-intent:"+id || (entry.State != "reserved" && entry.State != "settled" && entry.State != "released") || entry.State == "settled" && (entry.SettledAt.IsZero() || entry.SpentSat <= 0) {
			return nil, errOnchainGuardUnavailable
		}
	}
	s.onchainGuard = &state
	return s.onchainGuard, nil
}

func cloneOnchainGuard(state *onchainGuardState) *onchainGuardState {
	copy := *state
	copy.Entries = make(map[string]onchainGuardEntry, len(state.Entries))
	for id, entry := range state.Entries {
		copy.Entries[id] = entry
	}
	return &copy
}

func (s *financialIntentStore) persistOnchainGuardLocked(state *onchainGuardState) (err error) {
	// Even an error after rename/directory fsync must poison the in-memory
	// cache: never continue spending using a potentially older policy/balance.
	defer func() {
		if err != nil {
			s.onchainGuardErr = errOnchainGuardUnavailable
		}
	}()
	if s.lock == nil {
		return errOnchainGuardUnavailable
	}
	if s.onchainGuardPersistHook != nil {
		if err := s.onchainGuardPersistHook(); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > onchainGuardMaxBytes {
		return errOnchainGuardUnavailable
	}
	file, err := os.CreateTemp(s.dir, ".onchain-guard-*")
	if err != nil {
		return errOnchainGuardUnavailable
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if file.Chmod(0600) != nil {
		return errOnchainGuardUnavailable
	}
	if _, err := file.Write(raw); err != nil {
		return errOnchainGuardUnavailable
	}
	if file.Sync() != nil || file.Close() != nil {
		return errOnchainGuardUnavailable
	}
	if os.Rename(file.Name(), filepath.Join(s.dir, onchainGuardFile)) != nil || syncFinancialIntentDir(s.dir) != nil {
		return errOnchainGuardUnavailable
	}
	s.onchainGuard = state
	return nil
}

func (s *financialIntentStore) onchainGuardStatus() (onchainGuardStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadOnchainGuardLocked()
	if err != nil {
		return onchainGuardStatus{}, err
	}
	status := onchainGuardStatus{onchainGuardSettings: state.Settings}
	cutoff := s.now().Add(-24 * time.Hour)
	for _, entry := range state.Entries {
		if entry.State == "reserved" {
			status.ReservedSat += entry.ReservedSat
		}
		if entry.State == "settled" && entry.SettledAt.After(cutoff) {
			status.UsedSat += entry.SpentSat
		}
	}
	if status.Rolling24hLimitSat > 0 {
		status.RemainingSat = max(int64(0), status.Rolling24hLimitSat-status.UsedSat-status.ReservedSat)
	}
	return status, nil
}

func (s *financialIntentStore) updateOnchainGuard(next onchainGuardSettings, authorized bool) error {
	if err := validateOnchainGuardSettings(next); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadOnchainGuardLocked()
	if err != nil {
		return err
	}
	if onchainGuardNeedsReauth(state.Settings, next) && !authorized {
		return ErrSpendingGuardReauth
	}
	copy := cloneOnchainGuard(state)
	next.UpdatedAt = s.now().UTC()
	copy.Settings = next
	return s.persistOnchainGuardLocked(copy)
}

func (s *financialIntentStore) reserveOnchainGuard(id string, binding financialIntentBinding, label string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadOnchainGuardLocked()
	if err != nil {
		return false, err
	}
	if !state.Settings.Enabled {
		return false, nil
	}
	amount := binding.Summary.AmountSat
	// SendCoins computes the final sweep fee internally. Until its result is
	// known, reserve the selected input total, never an optimistic fee estimate.
	if binding.Summary.AmountMode == "selected_maximum" {
		amount = binding.Summary.SelectedInputSat
	}
	if !financialIntentIDValid(id) || binding.NativeLabel != "los-intent:"+id || binding.Kind != "onchain_send" || amount <= 0 || amount > onchainGuardMaxSat {
		return false, errOnchainGuardUnavailable
	}
	if _, exists := state.Entries[id]; exists {
		return false, errors.New("on-chain operation already reserved; reconcile before retrying")
	}
	settings := state.Settings
	if settings.MaxSendSat > 0 && amount > settings.MaxSendSat {
		return false, &onchainGuardLimitError{Reason: "per_send", Requested: amount, Remaining: settings.MaxSendSat}
	}
	cutoff := s.now().Add(-24 * time.Hour)
	var consumed int64
	copy := cloneOnchainGuard(state)
	for key, entry := range state.Entries {
		if entry.State == "reserved" {
			consumed += entry.ReservedSat
		}
		if entry.State == "settled" && entry.SettledAt.After(cutoff) {
			consumed += entry.SpentSat
		}
		if entry.State != "reserved" && entry.CreatedAt.Before(s.now().Add(-7*24*time.Hour)) {
			delete(copy.Entries, key)
		}
	}
	if settings.Rolling24hLimitSat > 0 && amount > max(int64(0), settings.Rolling24hLimitSat-consumed) {
		return false, &onchainGuardLimitError{Reason: "rolling_24h", Requested: amount, Remaining: max(int64(0), settings.Rolling24hLimitSat-consumed)}
	}
	if len(copy.Entries) >= onchainGuardMaxEntries {
		return false, errFinancialIntentCapacity
	}
	copy.Entries[id] = onchainGuardEntry{ID: id, ReservedSat: amount, State: "reserved", CreatedAt: s.now().UTC(), Binding: binding, OriginalLabel: label}
	if err := s.persistOnchainGuardLocked(copy); err != nil {
		return false, err
	}
	return true, nil
}

func (s *financialIntentStore) finishOnchainGuard(id, txid string, amount int64, release bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadOnchainGuardLocked()
	if err != nil {
		return err
	}
	entry, exists := state.Entries[id]
	if !exists || entry.State != "reserved" {
		return nil
	} // replay/reconciliation is idempotent
	copy := cloneOnchainGuard(state)
	if release {
		entry.State = "released"
	} else {
		if amount <= 0 || amount > entry.ReservedSat || txid == "" {
			return errOnchainGuardUnavailable
		}
		entry.State = "settled"
		entry.SpentSat = amount
		entry.TxID = txid
		entry.SettledAt = s.now().UTC()
	}
	copy.Entries[id] = entry
	return s.persistOnchainGuardLocked(copy)
}

func (s *financialIntentStore) pendingOnchainGuard() ([]onchainGuardEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadOnchainGuardLocked()
	if err != nil {
		return nil, err
	}
	var entries []onchainGuardEntry
	for _, entry := range state.Entries {
		if entry.State == "reserved" {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
