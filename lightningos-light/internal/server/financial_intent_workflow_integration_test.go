package server

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log"
	"strings"
	"testing"
	"time"
)

func financialExecutionContext(t *testing.T, s *Server, session authSessionSnapshot, record *financialIntentRecord) context.Context {
	t.Helper()
	claimed, started, err := s.financialIntents.claim(record.ID, session.ID, record.Path, record.Request, func(scope string) bool { return s.auth.consumeFinancialApproval(session.ID, scope) }, record.Method)
	if err != nil || !started {
		t.Fatal("fixture financial claim failed", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return context.WithValue(ctx, financialIntentContextKey{}, &financialIntentExecution{store: s.financialIntents, record: claimed})
}

func TestFinancialIntentRetirementChecksLockedTermsAndUsesSingleConnection(t *testing.T) {
	pool := balancedSecurityPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := log.New(io.Discard, "", 0)
	service := NewNodeRetirementService(pool, logger, nil, nil, nil)
	if err := service.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `truncate node_retirement_sessions cascade`); err != nil {
		t.Fatal(err)
	}
	config := json.RawMessage(`{"destination_address":"fixture-owner-destination","sweep_sat_per_vbyte":2}`)
	retirement, err := service.CreateSession(ctx, NodeRetirementCreateParams{Source: nodeRetirementSourceManual, DryRun: false, DisclaimerAccepted: true, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update node_retirement_sessions set state=$2 where session_id=$1`, retirement.SessionID, nodeRetirementStateReadyForConfirm); err != nil {
		t.Fatal(err)
	}
	retirement, err = service.GetSession(ctx, retirement.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// One connection catches accidental pool queries made while the control
	// transaction already owns its sole connection.
	limitedConfig := pool.Config()
	limitedConfig.MaxConns = 1
	limited, err := pgxpool.NewWithConfig(ctx, limitedConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	service = NewNodeRetirementService(limited, logger, nil, nil, nil)
	s, old, session := financialHTTPFixture(t)
	if _, err := s.financialIntents.cancelDraft(old.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/lnops/node-retirement/sessions/" + retirement.SessionID + "/confirm-coop"
	binding := financialIntentBinding{Kind: "node_retirement", Snapshot: financialSnapshot(retirementFinancialTerms(retirement))}
	intent := financialHandlerApproval(t, s, session, path, []byte(`{}`), binding)
	execution := financialExecutionContext(t, s, session, intent)
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err := blocker.Exec(ctx, `update node_retirement_sessions set config_json='{"destination_address":"changed-destination","sweep_sat_per_vbyte":20}'::jsonb where session_id=$1`, retirement.SessionID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := service.ConfirmCoopClose(execution, retirement.SessionID); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query like '%node_retirement_sessions%' and pid<>pg_backend_pid())`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retirement control never waited for its row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stale financial terms authorized a retirement command")
		}
	case <-ctx.Done():
		t.Fatal("locked retirement control did not finish")
	}
	current, err := service.GetSession(ctx, retirement.SessionID)
	if err != nil || current.State != nodeRetirementStateReadyForConfirm || !strings.Contains(string(current.Config), "changed-destination") {
		t.Fatal("rejected command overwrote the locked current state", err)
	}
	stored, _, _ := s.financialIntents.get(intent.ID)
	if len(stored.Proof) != 0 {
		t.Fatal("changed terms crossed the command dispatch barrier")
	}
	if err := s.financialIntents.finish(intent.ID, 409, []byte(`{"error":"terms changed"}`), false); err != nil {
		t.Fatal(err)
	}
	// A newly reviewed snapshot succeeds and persists exactly one event.
	fresh := financialHandlerApproval(t, s, session, path, []byte(`{}`), financialIntentBinding{Kind: "node_retirement", Snapshot: financialSnapshot(retirementFinancialTerms(current))})
	freshCtx := financialExecutionContext(t, s, session, fresh)
	confirmed, err := service.ConfirmCoopClose(freshCtx, retirement.SessionID)
	if err != nil || confirmed.State != nodeRetirementStateClosingCoop {
		t.Fatal("new exact approval did not preserve coop confirmation", err)
	}
	var events int
	if err := pool.QueryRow(ctx, `select count(*) from node_retirement_events where session_id=$1 and event_type='coop_close_confirmed'`, retirement.SessionID).Scan(&events); err != nil || events != 1 {
		t.Fatal("control event was not committed once", err)
	}
	if _, err := service.ConfirmCoopClose(freshCtx, retirement.SessionID); err == nil {
		t.Fatal("already-confirmed state accepted the command again")
	}
	point := strings.Repeat("ab", 32) + ":0"
	if _, err := pool.Exec(ctx, `update node_retirement_sessions set state=$2 where session_id=$1`, retirement.SessionID, nodeRetirementStateAwaitingDecision); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into node_retirement_channels(session_id,channel_point)values($1,$2)`, retirement.SessionID, point); err != nil {
		t.Fatal(err)
	}
	current, err = service.GetSession(ctx, retirement.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	decisionBody, _ := json.Marshal(nodeRetirementDecisionRequest{ChannelPoint: point, Decision: nodeRetirementDecisionForceClose})
	decision := financialHandlerApproval(t, s, session, "/api/lnops/node-retirement/sessions/"+retirement.SessionID+"/decision", decisionBody, financialIntentBinding{Kind: "node_retirement", Snapshot: financialSnapshot(retirementFinancialTerms(current))})
	if err := service.SetChannelDecision(financialExecutionContext(t, s, session, decision), retirement.SessionID, point, nodeRetirementDecisionForceClose); err != nil {
		t.Fatal("single-connection force decision failed", err)
	}
	current, err = service.GetSession(ctx, retirement.SessionID)
	if err != nil || current.State != nodeRetirementStateForceClosing {
		t.Fatal("force decision/state were not committed together", err)
	}
}

func TestFinancialIntentSuccessionFreezesOmittedPolicyFields(t *testing.T) {
	pool := balancedSecurityPool(t)
	ctx := context.Background()
	service := NewSuccessionService(pool, log.New(io.Discard, "", 0))
	if err := service.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update succession_config set enabled=false,dry_run=true,destination_address='owner-before',sweep_sat_per_vbyte=2 where id=1`); err != nil {
		t.Fatal(err)
	}
	current, err := service.GetConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	destination := "reviewed-destination"
	frozen := freezeSuccessionConfigRequest(current, successionConfigPostRequest{DestinationAddress: &destination})
	if _, err := pool.Exec(ctx, `update succession_config set enabled=true,dry_run=false,destination_address='concurrent-destination',sweep_sat_per_vbyte=200 where id=1`); err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateConfig(ctx, SuccessionConfigUpdate{Enabled: frozen.Enabled, DryRun: frozen.DryRun, DestinationAddress: frozen.DestinationAddress, PreapproveFCOffline: frozen.PreapproveFCOffline, PreapproveFCStuckHTLC: frozen.PreapproveFCStuckHTLC, StuckHTLCThresholdSec: frozen.StuckHTLCThresholdSec, SweepMinConfs: frozen.SweepMinConfs, SweepSatPerVbyte: frozen.SweepSatPerVbyte, CheckPeriodDays: frozen.CheckPeriodDays, ReminderPeriodDays: frozen.ReminderPeriodDays})
	if err != nil || updated.Enabled || !updated.DryRun || updated.DestinationAddress != destination || updated.SweepSatPerVbyte != 2 {
		t.Fatal("a partial save inherited an unreviewed live policy", err)
	}
}
