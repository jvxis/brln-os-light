package reports

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildUpsertDaily(t *testing.T) {
	reportDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.FixedZone("Local", -3*60*60))
	provenanceSyncAt := time.Date(2026, 1, 16, 2, 0, 0, 0, time.UTC)
	provenanceAgeHours := 3.5
	provenanceAlert := false
	provenanceLastError := ""
	row := Row{
		ReportDate: reportDate,
		Metrics: Metrics{
			ForwardFeeRevenueSat:       1200,
			ForwardFeeRevenueMsat:      1200000,
			RebalanceFeeCostSat:        300,
			RebalanceFeeCostMsat:       300000,
			PaymentFeeCostSat:          100,
			PaymentFeeCostMsat:         100000,
			OnchainFeeCostSat:          20,
			OnchainFeeCostMsat:         20000,
			OnchainCoopCloseCostSat:    5,
			OnchainCoopCloseCostMsat:   5000,
			OnchainLocalForceCostSat:   7,
			OnchainLocalForceCostMsat:  7000,
			OnchainRemoteForceCostSat:  8,
			OnchainRemoteForceCostMsat: 8000,
			KeysendReceivedSat:         50,
			KeysendReceivedMsat:        50000,
			KeysendReceivedCount:       1,
			NetRoutingProfitSat:        800,
			NetRoutingProfitMsat:       800000,
			NetWithKeysendSat:          850,
			NetWithKeysendMsat:         850000,
			ForwardCount:               4,
			RebalanceCount:             2,
			RebalanceVolumeSat:         64000,
			RebalanceVolumeMsat:        64000000,
			PaymentCount:               3,
			RoutedVolumeSat:            18000,
			RoutedVolumeMsat:           18000000,
			ProvenanceLastSyncAt:       &provenanceSyncAt,
			ProvenanceLastSyncAgeHours: &provenanceAgeHours,
			ProvenanceHealthAlert:      &provenanceAlert,
			ProvenanceLastError:        &provenanceLastError,
		},
	}

	query, args := buildUpsertDaily(row)
	if !strings.Contains(query, "on conflict (report_date) do update") {
		t.Fatalf("expected upsert query")
	}
	if !strings.Contains(query, "updated_at = now()") {
		t.Fatalf("expected updated_at update")
	}
	if !strings.Contains(query, "onchain_balance_sats = coalesce(excluded.onchain_balance_sats, reports_daily.onchain_balance_sats)") {
		t.Fatalf("expected onchain balance coalesce on upsert")
	}
	if !strings.Contains(query, "lightning_balance_sats = coalesce(excluded.lightning_balance_sats, reports_daily.lightning_balance_sats)") {
		t.Fatalf("expected lightning balance coalesce on upsert")
	}
	if !strings.Contains(query, "total_balance_sats = coalesce(excluded.total_balance_sats, reports_daily.total_balance_sats)") {
		t.Fatalf("expected total balance coalesce on upsert")
	}
	if len(args) != 36 {
		t.Fatalf("expected 36 args, got %d", len(args))
	}

	argDate, ok := args[0].(time.Time)
	if !ok {
		t.Fatalf("expected time arg for report date")
	}
	if argDate.Year() != 2026 || argDate.Month() != 1 || argDate.Day() != 15 {
		t.Fatalf("unexpected report date arg: %v", argDate)
	}
	if args[1] != int64(1200) || // forward_fee_revenue_sats
		args[2] != int64(1200000) || // forward_fee_revenue_msat
		args[3] != int64(300) || // rebalance_fee_cost_sats
		args[4] != int64(300000) || // rebalance_fee_cost_msat
		args[5] != int64(100) || // payment_fee_cost_sats
		args[6] != int64(100000) || // payment_fee_cost_msat
		args[7] != int64(20) || // onchain_fee_cost_sats
		args[8] != int64(20000) || // onchain_fee_cost_msat
		args[9] != int64(5) || // onchain_coop_close_cost_sats
		args[10] != int64(5000) || // onchain_coop_close_cost_msat
		args[11] != int64(7) || // onchain_local_force_cost_sats
		args[12] != int64(7000) || // onchain_local_force_cost_msat
		args[13] != int64(8) || // onchain_remote_force_cost_sats
		args[14] != int64(8000) || // onchain_remote_force_cost_msat
		args[15] != int64(50) || // keysend_received_sats
		args[16] != int64(50000) || // keysend_received_msat
		args[17] != int64(1) || // keysend_received_count
		args[18] != int64(800) || // net_routing_profit_sats
		args[19] != int64(800000) || // net_routing_profit_msat
		args[20] != int64(850) || // net_with_keysend_sats
		args[21] != int64(850000) || // net_with_keysend_msat
		args[22] != int64(4) || // forward_count
		args[23] != int64(2) || // rebalance_count
		args[24] != int64(64000) || // rebalance_volume_sats
		args[25] != int64(64000000) || // rebalance_volume_msat
		args[26] != int64(3) || // payment_count
		args[27] != int64(18000) || // routed_volume_sats
		args[28] != int64(18000000) || // routed_volume_msat
		args[29] != nil || // onchain_balance_sats
		args[30] != nil || // lightning_balance_sats
		args[31] != nil || // total_balance_sats
		args[32] != provenanceSyncAt || // provenance_last_sync_at
		args[33] != provenanceAgeHours || // provenance_last_sync_age_hours
		args[34] != false || // provenance_health_alert
		args[35] != "" { // provenance_last_error
		t.Fatalf("unexpected metrics args")
	}
}

func TestBuildProvenanceReportHealth(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	syncAt := now.Add(-6 * time.Hour)

	fresh := buildProvenanceReportHealth(&syncAt, "", now)
	if fresh.Alert {
		t.Fatalf("fresh sync should not alert")
	}
	if fresh.LastSyncAgeHours == nil || *fresh.LastSyncAgeHours != 6 {
		t.Fatalf("unexpected fresh age: %+v", fresh.LastSyncAgeHours)
	}

	staleAt := now.Add(-25 * time.Hour)
	stale := buildProvenanceReportHealth(&staleAt, "", now)
	if !stale.Alert {
		t.Fatalf("stale sync should alert")
	}

	withError := buildProvenanceReportHealth(&syncAt, "  list tx failed  ", now)
	if !withError.Alert || withError.LastError != "list tx failed" {
		t.Fatalf("sync error should alert with trimmed error, got %+v", withError)
	}

	never := buildProvenanceReportHealth(nil, "", now)
	if !never.Alert || never.LastSyncAgeHours != nil || never.LastSyncAt != nil {
		t.Fatalf("never synced should alert without age, got %+v", never)
	}
}

func TestBuildUpsertLiveSnapshot(t *testing.T) {
	loc := time.FixedZone("BRT", -3*60*60)
	snapshot := liveSnapshot{
		UpdatedAt: time.Date(2026, 4, 2, 9, 30, 0, 0, loc),
		Timezone:  "America/Sao_Paulo",
		Range: TimeRange{
			StartLocal: time.Date(2026, 4, 2, 0, 0, 0, 0, loc),
			EndLocal:   time.Date(2026, 4, 2, 9, 30, 0, 0, loc),
		},
		Metrics: Metrics{
			ForwardFeeRevenueSat: 200,
			RebalanceFeeCostSat:  50,
			RebalanceVolumeSat:   7500,
			RoutedVolumeSat:      12000,
		},
		LookbackHours: 0,
	}

	query, args, err := buildUpsertLiveSnapshot(snapshot)
	if err != nil {
		t.Fatalf("buildUpsertLiveSnapshot returned error: %v", err)
	}
	if !strings.Contains(query, "insert into reports_live_cache") {
		t.Fatalf("expected live cache insert query")
	}
	if !strings.Contains(query, "on conflict (timezone, lookback_hours) do update") {
		t.Fatalf("expected live cache upsert query")
	}
	if len(args) != 5 {
		t.Fatalf("expected 5 args, got %d", len(args))
	}
	if args[0] != "America/Sao_Paulo" {
		t.Fatalf("unexpected timezone arg: %v", args[0])
	}
	if args[1] != 0 {
		t.Fatalf("unexpected lookback arg: %v", args[1])
	}

	raw, ok := args[4].([]byte)
	if !ok {
		t.Fatalf("expected json payload []byte")
	}
	var parsed Metrics
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if parsed.ForwardFeeRevenueSat != 200 || parsed.RebalanceFeeCostSat != 50 || parsed.RebalanceVolumeSat != 7500 || parsed.RoutedVolumeSat != 12000 {
		t.Fatalf("unexpected metrics payload: %+v", parsed)
	}
}
