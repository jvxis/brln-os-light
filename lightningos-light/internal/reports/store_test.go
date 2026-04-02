package reports

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildUpsertDaily(t *testing.T) {
	reportDate := time.Date(2026, 1, 15, 0, 0, 0, 0, time.FixedZone("Local", -3*60*60))
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
			PaymentCount:               3,
			RoutedVolumeSat:            18000,
			RoutedVolumeMsat:           18000000,
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
	if len(args) != 30 {
		t.Fatalf("expected 30 args, got %d", len(args))
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
		args[24] != int64(3) || // payment_count
		args[25] != int64(18000) || // routed_volume_sats
		args[26] != int64(18000000) || // routed_volume_msat
		args[27] != nil || // onchain_balance_sats
		args[28] != nil || // lightning_balance_sats
		args[29] != nil { // total_balance_sats
		t.Fatalf("unexpected metrics args")
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
	if parsed.ForwardFeeRevenueSat != 200 || parsed.RebalanceFeeCostSat != 50 || parsed.RoutedVolumeSat != 12000 {
		t.Fatalf("unexpected metrics payload: %+v", parsed)
	}
}
