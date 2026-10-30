package server

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func financialPrivateTestDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "private-intents")
}

func financialStoreFixture(t *testing.T) (*financialIntentStore, *financialIntentRecord) {
	t.Helper()
	s, err := openFinancialIntentStore(financialPrivateTestDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	id, err := newFinancialIntentID()
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.create(id, "fictional-private-cookie", "/api/wallet/send", authScopeWalletSendExternal, []byte(`{"address":"fixture","amount_sat":1000,"sat_per_vbyte":2}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func TestFinancialIntentStoreBindsPayloadSessionAndSingleUse(t *testing.T) {
	s, r := financialStoreFixture(t)
	var consumed atomic.Int64
	consume := func(scope string) bool {
		if scope != financialIntentScopePrefix+r.ID {
			t.Error("wrong scope consumed")
		}
		consumed.Add(1)
		return true
	}
	for _, tc := range []struct{ session, path, body string }{
		{"other-session", r.Path, string(r.Request)},
		{"fictional-private-cookie", "/api/wallet/pay", string(r.Request)},
		{"fictional-private-cookie", r.Path, `{"address":"other","amount_sat":1000,"sat_per_vbyte":2}`},
		{"fictional-private-cookie", r.Path, `{"address":"fixture","amount_sat":9000,"sat_per_vbyte":2}`},
		{"fictional-private-cookie", r.Path, `{"address":"fixture","amount_sat":1000,"sat_per_vbyte":20}`},
	} {
		if _, started, err := s.claim(r.ID, tc.session, tc.path, []byte(tc.body), consume); started || !errors.Is(err, errFinancialIntentMismatch) {
			t.Fatal("changed operation reached execution")
		}
	}
	if consumed.Load() != 0 {
		t.Fatal("mismatched operation consumed approval")
	}
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { return false }); started || !errors.Is(err, errFinancialIntentApproval) {
		t.Fatal("missing approval started execution")
	}
	var starts atomic.Int64
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, consume)
			if started {
				starts.Add(1)
			} else if !errors.Is(err, errFinancialIntentPending) {
				t.Error("concurrent request was not held pending")
			}
		}()
	}
	group.Wait()
	if starts.Load() != 1 || consumed.Load() != 1 {
		t.Fatal("approval allowed duplicate execution")
	}
	if err := s.finish(r.ID, 200, []byte(`{"txid":"known-proof"}`), false); err != nil {
		t.Fatal(err)
	}
	got, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, consume)
	if err != nil || started || string(got.ResponseBody) != `{"txid":"known-proof"}` || consumed.Load() != 1 {
		t.Fatal("identical retry did not reuse result")
	}
	got.ResponseBody[0] = 'x'
	again, _, _ := s.get(r.ID)
	if string(again.ResponseBody) != `{"txid":"known-proof"}` {
		t.Fatal("caller mutated stored result")
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, r.ID+".json"))
	if err != nil || strings.Contains(string(raw), "fictional-private-cookie") {
		t.Fatal("durable record contains a bearer cookie")
	}
}

func TestFinancialIntentStoreCrashRecoveryNeverReexecutes(t *testing.T) {
	s, r := financialStoreFixture(t)
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { return true }); err != nil || !started {
		t.Fatal("claim failed")
	}
	proof := []byte(`{"txid":"immutable-proof","raw_transaction":"fictional-signed-bytes"}`)
	if err := s.saveProof(r.ID, proof); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := openFinancialIntentStore(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	got, running, err := restarted.get(r.ID)
	if err != nil || running || got.State != financialIntentUncertainState || string(got.Proof) != string(proof) {
		t.Fatal("unfinished proof was lost or misrepresented after restart")
	}
	if _, started, err := restarted.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { t.Error("recovery consumed new approval"); return true }); started || !errors.Is(err, errFinancialIntentPending) {
		t.Fatal("unfinished execution reopened after restart")
	}
}

func TestFinancialIntentStoreExpiryRevocationAndWriteFailure(t *testing.T) {
	s, r := financialStoreFixture(t)
	now := r.ExpiresAt
	s.now = func() time.Time { return now }
	if s.approvalAllowed(r.ID, "fictional-private-cookie") {
		t.Fatal("expired approval allowed")
	}
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { t.Error("expired intent consumed approval"); return true }); started || !errors.Is(err, errFinancialIntentExpired) {
		t.Fatal("expired intent started")
	}
	s.now = time.Now
	s.persistHook = func(*financialIntentRecord) error { return errors.New("fixture disk full") }
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { return true }); started || err == nil {
		t.Fatal("failed durable claim allowed execution")
	}
	s.persistHook = nil
	got, running, _ := s.get(r.ID)
	if running || got.State != financialIntentDraftState {
		t.Fatal("failed claim changed execution state")
	}
	// A revoked session/approval must not start even after storage recovers.
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { return false }); started || !errors.Is(err, errFinancialIntentApproval) {
		t.Fatal("revoked approval started")
	}
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, func(string) bool { return true }); err != nil || !started {
		t.Fatal("reapproved claim failed")
	}
	s.persistHook = func(*financialIntentRecord) error { return errors.New("fixture final sync failure") }
	if s.finish(r.ID, 200, []byte(`{"txid":"known"}`), false) == nil {
		t.Fatal("final storage failure hidden")
	}
	got, running, _ = s.get(r.ID)
	if running || got.State != financialIntentUncertainState {
		t.Fatal("failed result commit permitted replay")
	}
}

func TestFinancialIntentStoreExclusiveOwnershipAndCorruption(t *testing.T) {
	s, r := financialStoreFixture(t)
	if other, err := openFinancialIntentStore(s.dir); err == nil {
		other.Close()
		t.Fatal("second Manager acquired financial execution storage")
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{s.dir, filepath.Join(s.dir, r.ID+".json"), filepath.Join(s.dir, ".lock")} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatal("financial storage permissions expose private operation data")
			}
		}
	}
	s.Close()
	if err := os.WriteFile(filepath.Join(s.dir, r.ID+".json"), []byte("{partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openFinancialIntentStore(s.dir); err == nil {
		reopened.Close()
		t.Fatal("corrupt execution record was ignored")
	}
}

func TestFinancialIntentStoreRejectsReadableExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are enforced by the native Linux integration run")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if store, err := openFinancialIntentStore(dir); err == nil {
		store.Close()
		t.Fatal("existing public directory accepted private financial metadata")
	}
	private := filepath.Join(dir, "private")
	store, err := openFinancialIntentStore(private)
	if err != nil {
		t.Fatal("new private child could not be initialized", err)
	}
	defer store.Close()
	info, err := os.Stat(private)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("new financial directory did not enforce 0700")
	}
}

func TestFinancialIntentStoreBoundedDraftsAndExpiredCleanup(t *testing.T) {
	s, r := financialStoreFixture(t)
	s.maxPending = 1
	id, _ := newFinancialIntentID()
	if _, err := s.create(id, "fictional-private-cookie", r.Path, r.BaseScope, r.Request, nil); !errors.Is(err, errFinancialIntentCapacity) {
		t.Fatal("pending intent limit ignored")
	}
	s.now = func() time.Time { return r.ExpiresAt.Add(time.Second) }
	if _, err := s.create(id, "fictional-private-cookie", r.Path, r.BaseScope, r.Request, nil); err != nil {
		t.Fatal("expired drafts were not cleaned")
	}
	if _, err := os.Stat(filepath.Join(s.dir, r.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired draft file retained")
	}
	s.maxPending = 10
	s.maxBytes = s.totalBytes + 1
	next, _ := newFinancialIntentID()
	if _, err := s.create(next, "fictional-private-cookie", r.Path, r.BaseScope, r.Request, nil); !errors.Is(err, errFinancialIntentCapacity) {
		t.Fatal("intent byte budget ignored")
	}
}

func TestFinancialIntentStoreAbruptProcessExit(t *testing.T) {
	dir := financialPrivateTestDir(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestFinancialIntentStoreCrashWorker$")
	command.Env = append(os.Environ(), "LOS_FINANCIAL_INTENT_CRASH_DIR="+dir)
	if err := command.Run(); err == nil {
		t.Fatal("crash worker did not terminate abruptly")
	} else {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 42 {
			t.Fatal("crash worker failed before durable proof")
		}
	}
	recovered, err := openFinancialIntentStore(dir)
	if err != nil {
		t.Fatal("OS did not release lock after worker exit")
	}
	defer recovered.Close()
	if len(recovered.records) != 1 {
		t.Fatal("crash lost committed operation")
	}
	for _, record := range recovered.records {
		if record.State != financialIntentUncertainState || string(record.Proof) != `{"txid":"crash-proof"}` {
			t.Fatal("crash reopened execution or lost immutable proof")
		}
		if _, started, err := recovered.claim(record.ID, "crash-fixture-cookie", record.Path, record.Request, func(string) bool { return true }); started || !errors.Is(err, errFinancialIntentPending) {
			t.Fatal("crashed execution was dispatched again")
		}
	}
}

func TestFinancialIntentStoreCrashWorker(t *testing.T) {
	dir := os.Getenv("LOS_FINANCIAL_INTENT_CRASH_DIR")
	if dir == "" {
		t.Skip("subprocess fixture only")
	}
	store, err := openFinancialIntentStore(dir)
	if err != nil {
		os.Exit(43)
	}
	id, err := newFinancialIntentID()
	if err != nil {
		os.Exit(43)
	}
	record, err := store.create(id, "crash-fixture-cookie", "/api/wallet/send", authScopeWalletSendExternal, []byte(`{"address":"crash-fixture"}`), nil)
	if err != nil {
		os.Exit(43)
	}
	if _, started, err := store.claim(id, "crash-fixture-cookie", record.Path, record.Request, func(string) bool { return true }); err != nil || !started {
		os.Exit(43)
	}
	if store.saveProof(id, []byte(`{"txid":"crash-proof"}`)) != nil {
		os.Exit(43)
	}
	// Intentionally bypass Close, test cleanup and every defer.
	os.Exit(42)
}

func TestFinancialIntentStoreMethodBindingAndSafeCancellation(t *testing.T) {
	s, r := financialStoreFixture(t)
	id, _ := newFinancialIntentID()
	policy, err := s.create(id, "fictional-private-cookie", "/api/wallet/spending-guard", authScopeSpendingGuardPolicy, []byte(`{"enabled":false}`), nil, "PUT")
	if err != nil {
		t.Fatal(err)
	}
	consume := func(string) bool { return true }
	if _, started, err := s.claim(id, "fictional-private-cookie", policy.Path, policy.Request, consume); started || !errors.Is(err, errFinancialIntentMismatch) {
		t.Fatal("HTTP method changed an approved operation")
	}
	if _, err := s.cancelDraft(r.ID); err != nil {
		t.Fatal(err)
	}
	if s.approvalAllowed(r.ID, "fictional-private-cookie") {
		t.Fatal("cancelled draft remains approvable")
	}
	if _, started, err := s.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, consume); started || !errors.Is(err, errFinancialIntentCancelled) {
		t.Fatal("cancelled draft executed with its old approval")
	}
	if _, started, err := s.claim(id, "fictional-private-cookie", policy.Path, policy.Request, consume, "PUT"); err != nil || !started {
		t.Fatal("exact approved method could not execute")
	}
	if _, err := s.cancelDraft(id); !errors.Is(err, errFinancialIntentPending) {
		t.Fatal("cancel discarded an executing operation")
	}
	if err := s.finish(id, 500, []byte(`{"error":"ambiguous"}`), true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.cancelDraft(id); !errors.Is(err, errFinancialIntentPending) {
		t.Fatal("cancel discarded an uncertain operation")
	}
	dir := s.dir
	s.Close()
	reopened, err := openFinancialIntentStore(dir)
	if err != nil {
		t.Fatal("cancelled tombstone did not survive restart")
	}
	defer reopened.Close()
	if _, started, err := reopened.claim(r.ID, "fictional-private-cookie", r.Path, r.Request, consume); started || !errors.Is(err, errFinancialIntentCancelled) {
		t.Fatal("restart reopened a cancelled operation")
	}
}
