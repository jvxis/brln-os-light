package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	financialIntentScopePrefix    = "financial_intent:"
	financialIntentTTL            = 3 * time.Minute
	financialIntentDraftState     = "draft"
	financialIntentExecutingState = "executing"
	financialIntentUncertainState = "uncertain"
	financialIntentCompletedState = "completed"
	financialIntentRejectedState  = "rejected"
	financialIntentCancelledState = "cancelled"
	financialIntentMaxRecordBytes = 20 << 20
)

var (
	errFinancialIntentMissing   = errors.New("financial intent not found")
	errFinancialIntentMismatch  = errors.New("financial intent does not match this operation and session")
	errFinancialIntentExpired   = errors.New("financial intent expired; review and prepare the operation again")
	errFinancialIntentApproval  = errors.New("password confirmation required for this financial intent")
	errFinancialIntentPending   = errors.New("financial intent result requires reconciliation; do not create another transfer")
	errFinancialIntentCapacity  = errors.New("too many financial intents; finish pending operations before retrying")
	errFinancialIntentCancelled = errors.New("financial intent was cancelled; review the operation before preparing another")
)

// Private durable execution metadata. Never serialize this struct directly to
// an API or log: Request/Proof may contain invoices, signed transactions or
// channel recovery metadata. Session is an audit correlation, never a cookie.
type financialIntentRecord struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	Session        string          `json:"session"`
	Path           string          `json:"path"`
	Method         string          `json:"method"`
	BaseScope      string          `json:"base_scope"`
	Request        json.RawMessage `json:"request"`
	Binding        json.RawMessage `json:"binding,omitempty"`
	Fingerprint    string          `json:"fingerprint"`
	CreatedAt      time.Time       `json:"created_at"`
	ExpiresAt      time.Time       `json:"expires_at"`
	State          string          `json:"state"`
	Proof          json.RawMessage `json:"proof,omitempty"`
	ResponseStatus int             `json:"response_status,omitempty"`
	ResponseBody   json.RawMessage `json:"response_body,omitempty"`
}

type financialIntentStore struct {
	mu          sync.Mutex
	dir         string
	lock        *os.File
	now         func() time.Time
	records     map[string]*financialIntentRecord
	running     map[string]bool
	maxPending  int
	maxRecords  int
	maxBytes    int64
	totalBytes  int64
	sizes       map[string]int64
	persistHook func(*financialIntentRecord) error // unit fault injection only
}

func financialIntentIDValid(id string) bool {
	if len(id) != 32 || id != strings.ToLower(id) {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func newFinancialIntentID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", errors.New("financial intent identifier unavailable")
	}
	return hex.EncodeToString(random[:]), nil
}

func financialIntentFingerprint(path string, request, binding []byte, methods ...string) string {
	method := "POST"
	if len(methods) > 0 {
		method = methods[0]
	}
	encoded, _ := json.Marshal([]any{method, path, json.RawMessage(request), json.RawMessage(binding)})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func cloneFinancialIntent(record *financialIntentRecord) *financialIntentRecord {
	copy := *record
	copy.Request = bytes.Clone(record.Request)
	copy.Binding = bytes.Clone(record.Binding)
	copy.Proof = bytes.Clone(record.Proof)
	copy.ResponseBody = bytes.Clone(record.ResponseBody)
	return &copy
}

func openFinancialIntentStore(dir string) (*financialIntentStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("financial intent storage unavailable")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("financial intent storage must be a private directory")
	}
	lockPath := filepath.Join(dir, ".lock")
	if info, err := os.Lstat(lockPath); err == nil && (!info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("invalid financial intent lock file")
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("financial intent storage lock unavailable")
	}
	if err := lockFinancialIntentFile(lock); err != nil {
		lock.Close()
		return nil, errors.New("financial intent storage already owned by another Manager")
	}
	store := &financialIntentStore{dir: dir, lock: lock, now: time.Now, records: make(map[string]*financialIntentRecord), running: make(map[string]bool), maxPending: 128, maxRecords: 4096, maxBytes: 64 << 20, sizes: make(map[string]int64)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		store.Close()
		return nil, errors.New("financial intent storage unreadable")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !financialIntentIDValid(id) || len(store.records) >= store.maxRecords {
			store.Close()
			return nil, errors.New("invalid financial intent storage inventory")
		}
		info, err := os.Lstat(filepath.Join(dir, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Size() > financialIntentMaxRecordBytes || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			store.Close()
			return nil, errors.New("invalid financial intent record file")
		}
		file, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			store.Close()
			return nil, errors.New("financial intent record unreadable")
		}
		var record financialIntentRecord
		decoder := json.NewDecoder(io.LimitReader(file, financialIntentMaxRecordBytes+1))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&record)
		var trailing any
		endErr := decoder.Decode(&trailing)
		file.Close()
		validPayload := record.State == financialIntentCancelledState || json.Valid(record.Request) && record.Fingerprint == financialIntentFingerprint(record.Path, record.Request, record.Binding, record.Method)
		if err != nil || endErr != io.EOF || record.ID != id || record.Version != 1 || (record.Method != "POST" && record.Method != "PUT") || !validPayload {
			store.Close()
			return nil, errors.New("financial intent record is corrupt; execution remains blocked")
		}
		switch record.State {
		case financialIntentExecutingState:
			// A prior process may have dispatched an RPC before dying. Never
			// turn its unfinished operation back into an executable draft.
			record.State = financialIntentUncertainState
		case financialIntentDraftState, financialIntentUncertainState, financialIntentCompletedState, financialIntentRejectedState, financialIntentCancelledState:
		default:
			store.Close()
			return nil, errors.New("invalid financial intent state")
		}
		store.records[id] = &record
		store.sizes[id] = info.Size()
		store.totalBytes += info.Size()
		if store.totalBytes > store.maxBytes {
			store.Close()
			return nil, errors.New("financial intent storage exceeds its capacity")
		}
	}
	return store, nil
}

func (s *financialIntentStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

// A successful return includes file fsync and, on supported Unix production
// hosts, directory fsync. Failure is fail-closed: callers must not dispatch.
func (s *financialIntentStore) persist(record *financialIntentRecord) error {
	if s.lock == nil {
		return errors.New("financial intent storage closed")
	}
	if s.persistHook != nil {
		return s.persistHook(record)
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > financialIntentMaxRecordBytes {
		return errors.New("financial intent record exceeds storage limits")
	}
	if s.totalBytes-s.sizes[record.ID]+int64(len(raw)) > s.maxBytes {
		return errFinancialIntentCapacity
	}
	file, err := os.CreateTemp(s.dir, ".intent-*")
	if err != nil {
		return errors.New("financial intent record could not be written")
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return errors.New("financial intent record permissions unavailable")
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return errors.New("financial intent record write failed")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return errors.New("financial intent record sync failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("financial intent record close failed")
	}
	if err := os.Rename(name, filepath.Join(s.dir, record.ID+".json")); err != nil {
		return errors.New("financial intent record commit failed")
	}
	if err := syncFinancialIntentDir(s.dir); err != nil {
		return errors.New("financial intent directory sync failed")
	}
	s.totalBytes += int64(len(raw)) - s.sizes[record.ID]
	s.sizes[record.ID] = int64(len(raw))
	return nil
}

func (s *financialIntentStore) create(id, session, path, scope string, request, binding []byte, methods ...string) (*financialIntentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !financialIntentIDValid(id) || !json.Valid(request) || len(binding) > 0 && !json.Valid(binding) {
		return nil, errors.New("invalid financial intent draft")
	}
	canonical, err := canonicalFinancialPayload(request)
	if err != nil {
		return nil, errors.New("invalid financial intent payload")
	}
	request = canonical
	method := "POST"
	if len(methods) > 0 {
		method = methods[0]
	}
	if method != "POST" && method != "PUT" {
		return nil, errors.New("unsupported financial intent method")
	}
	if _, exists := s.records[id]; exists {
		return nil, errors.New("financial intent identifier already exists")
	}
	now := s.now()
	pending := 0
	for key, record := range s.records {
		if (record.State == financialIntentDraftState || record.State == financialIntentCancelledState) && !now.Before(record.ExpiresAt) || (record.State == financialIntentCompletedState || record.State == financialIntentRejectedState) && now.After(record.CreatedAt.Add(7*24*time.Hour)) {
			if err := os.Remove(filepath.Join(s.dir, key+".json")); err != nil {
				return nil, errors.New("expired financial intent cleanup failed")
			}
			delete(s.records, key)
			s.totalBytes -= s.sizes[key]
			delete(s.sizes, key)
			continue
		}
		if record.State == financialIntentDraftState || record.State == financialIntentExecutingState || record.State == financialIntentUncertainState {
			pending++
		}
	}
	if pending >= s.maxPending || len(s.records) >= s.maxRecords {
		return nil, errFinancialIntentCapacity
	}
	record := &financialIntentRecord{Version: 1, ID: id, Session: auditCorrelationID(session), Path: path, Method: method, BaseScope: scope, Request: bytes.Clone(request), Binding: bytes.Clone(binding), CreatedAt: now, ExpiresAt: now.Add(financialIntentTTL), State: financialIntentDraftState, Fingerprint: financialIntentFingerprint(path, request, binding, method)}
	if err := s.persist(record); err != nil {
		return nil, err
	}
	s.records[id] = record
	return cloneFinancialIntent(record), nil
}

func (s *financialIntentStore) get(id string) (*financialIntentRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return nil, false, errFinancialIntentMissing
	}
	return cloneFinancialIntent(record), s.running[id], nil
}

func (s *financialIntentStore) approvalAllowed(id, session string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	return record != nil && record.Session == auditCorrelationID(session) && record.State == financialIntentDraftState && s.now().Before(record.ExpiresAt)
}

// claim serializes concurrent execution and consumes approval only for the
// exact session and payload. The durable executing record precedes every RPC.
// Existing results are returned without consuming another approval.
func (s *financialIntentStore) claim(id, session, path string, request []byte, consume func(string) bool, methods ...string) (*financialIntentRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil {
		return nil, false, errFinancialIntentMissing
	}
	method := "POST"
	if len(methods) > 0 {
		method = methods[0]
	}
	if record.Session != auditCorrelationID(session) || record.Path != path || record.Method != method {
		return nil, false, errFinancialIntentMismatch
	}
	if record.State == financialIntentCancelledState {
		return cloneFinancialIntent(record), false, errFinancialIntentCancelled
	}
	if record.Fingerprint != financialIntentFingerprint(path, request, record.Binding, method) {
		return nil, false, errFinancialIntentMismatch
	}
	if record.State == financialIntentCompletedState || record.State == financialIntentRejectedState {
		return cloneFinancialIntent(record), false, nil
	}
	if record.State != financialIntentDraftState {
		return cloneFinancialIntent(record), false, errFinancialIntentPending
	}
	if path == "/api/wallet/send" {
		for otherID, other := range s.records {
			if otherID != id && other.Path == path && (other.State == financialIntentExecutingState || other.State == financialIntentUncertainState) {
				return cloneFinancialIntent(record), false, errFinancialIntentPending
			}
		}
	}
	if !s.now().Before(record.ExpiresAt) {
		return nil, false, errFinancialIntentExpired
	}
	if consume == nil || !consume(financialIntentScopePrefix+id) {
		return nil, false, errFinancialIntentApproval
	}
	updated := cloneFinancialIntent(record)
	updated.State = financialIntentExecutingState
	if err := s.persist(updated); err != nil {
		return nil, false, err
	}
	s.records[id] = updated
	s.running[id] = true
	return cloneFinancialIntent(updated), true, nil
}

func (s *financialIntentStore) saveProof(id string, proof []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil || !s.running[id] || record.State != financialIntentExecutingState || !json.Valid(proof) {
		return errors.New("financial execution proof cannot be updated")
	}
	updated := cloneFinancialIntent(record)
	updated.Proof = bytes.Clone(proof)
	if err := s.persist(updated); err != nil {
		return err
	}
	s.records[id] = updated
	return nil
}

func (s *financialIntentStore) cancelDraft(id string) (*financialIntentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil {
		return nil, errFinancialIntentMissing
	}
	if record.State == financialIntentCancelledState {
		return cloneFinancialIntent(record), nil
	}
	if record.State != financialIntentDraftState {
		return nil, errFinancialIntentPending
	}
	updated := cloneFinancialIntent(record)
	updated.State = financialIntentCancelledState
	updated.Request = nil
	updated.Binding = nil
	updated.Proof = nil
	updated.ResponseStatus = 409
	updated.ResponseBody = []byte(`{"error":"financial intent was cancelled","code":"financial_intent_cancelled"}`)
	if err := s.persist(updated); err != nil {
		// A failed cancellation can never allow a later live worker to
		// execute this draft. A restart also revokes its in-memory session.
		s.records[id] = updated
		return nil, err
	}
	s.records[id] = updated
	return cloneFinancialIntent(updated), nil
}

func (s *financialIntentStore) finish(id string, status int, body []byte, uncertain bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil || !s.running[id] {
		return errors.New("financial execution is not owned by this worker")
	}
	updated := cloneFinancialIntent(record)
	updated.ResponseStatus = status
	updated.ResponseBody = bytes.Clone(body)
	updated.State = financialIntentCompletedState
	if uncertain {
		updated.State = financialIntentUncertainState
	} else if status >= 400 {
		updated.State = financialIntentRejectedState
	}
	if err := s.persist(updated); err != nil {
		// Even a failed final fsync can never reopen execution in this process.
		updated.State = financialIntentUncertainState
		s.records[id] = updated
		delete(s.running, id)
		return err
	}
	s.records[id] = updated
	delete(s.running, id)
	return nil
}

// reconcile commits an independently observed native result. It never claims
// execution or consumes approval, and cannot race a still-running dispatcher.
func (s *financialIntentStore) reconcile(id string, status int, body []byte) (*financialIntentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil {
		return nil, errFinancialIntentMissing
	}
	if record.State == financialIntentCompletedState || record.State == financialIntentRejectedState {
		return cloneFinancialIntent(record), nil
	}
	if s.running[id] || record.State != financialIntentUncertainState || !json.Valid(body) || status < 200 || status >= 500 {
		return nil, errFinancialIntentPending
	}
	updated := cloneFinancialIntent(record)
	updated.ResponseStatus, updated.ResponseBody = status, bytes.Clone(body)
	updated.State = financialIntentCompletedState
	if status >= 400 {
		updated.State = financialIntentRejectedState
	}
	if err := s.persist(updated); err != nil {
		return nil, err
	}
	s.records[id] = updated
	return cloneFinancialIntent(updated), nil
}

func (s *financialIntentStore) hasUncertainOperation(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Path == path && (record.State == financialIntentUncertainState || record.State == financialIntentExecutingState) {
			return true
		}
	}
	return false
}
