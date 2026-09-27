package reports

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestDerivedTotalsIgnoreStoredNets(t *testing.T) {
	m := Metrics{ForwardFeeRevenueMsat: 100123, RebalanceFeeCostMsat: 20009, PaymentFeeCostMsat: 3003,
		OnchainFeeCostMsat: 4000, KeysendReceivedMsat: 6007, KeysendSentMsat: 7008, SalesRevenueMsat: 5000,
		NetRoutingProfitMsat: 999000, NetWithKeysendMsat: 999000, NetTotalMsat: 999000}
	got := m.WithActivityMarks(ActivityMarkTotals{RevenueMsat: 8001, CostMsat: 9002}).WithDerivedTotals()
	if got.NetRoutingProfitMsat != 80114 || got.NetWithKeysendMsat != 86121 || got.NetTotalMsat != 76109 {
		t.Fatalf("wrong nets %+v", got)
	}
	if got.NetTotalSat != 76 || m.NetTotalMsat != 999000 {
		t.Fatal("rounding or input mutation")
	}
	if again := got.WithDerivedTotals(); again != got {
		t.Fatal("not idempotent")
	}
	zero := Metrics{ForwardFeeRevenueMsat: 1000, RebalanceFeeCostMsat: 1000, NetTotalMsat: 9}.WithDerivedTotals()
	if zero.NetTotalMsat != 0 {
		t.Fatal("legitimate zero")
	}
	negative := Metrics{RebalanceFeeCostMsat: 1999}.WithDerivedTotals()
	if negative.NetTotalSat != -1 || negative.NetRoutingProfitMsat != -1999 {
		t.Fatal("negative rounding")
	}
}

func TestDerivedLegacySatsAndMarkReplacement(t *testing.T) {
	m := Metrics{ForwardFeeRevenueSat: 100, KeysendSentSat: 10, SalesRevenueSat: 20}
	first := m.WithActivityMarks(ActivityMarkTotals{RevenueMsat: 50000, RevenueUnit: 1}).WithDerivedTotals()
	if first.NetTotalMsat != 160000 {
		t.Fatal(first)
	}
	cleared := first.WithActivityMarks(ActivityMarkTotals{}).WithDerivedTotals()
	if cleared.NetTotalMsat != 110000 || cleared.MarkedRevenueCount != 0 {
		t.Fatal("mark added twice or not removed")
	}
	if m.KeysendSentMsat != 0 {
		t.Fatal("source modified")
	}
}

func TestReportCalendarDayAndDST(t *testing.T) {
	for _, tc := range []struct {
		name, date string
		hours      int
	}{
		{"America/Sao_Paulo", "2026-09-26", 24}, {"Asia/Tokyo", "2026-09-26", 24},
		{"America/New_York", "2026-03-08", 23}, {"America/New_York", "2026-11-01", 25},
	} {
		t.Run(tc.name+tc.date, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			date, _ := time.Parse("2006-01-02", tc.date)
			tr := reportDayRange(date, loc)
			if tr.StartLocal.Format("2006-01-02") != tc.date || tr.EndUTC.Sub(tr.StartUTC) != time.Duration(tc.hours)*time.Hour {
				t.Fatal(tr)
			}
		})
	}
}

func TestDerivedSummaryIncludesEveryComponent(t *testing.T) {
	m := Metrics{ForwardFeeRevenueMsat: 200000, KeysendSentMsat: 20000, KeysendSentCount: 2}.
		WithActivityMarks(ActivityMarkTotals{RevenueMsat: 40000, RevenueUnit: 2, CostMsat: 6000, CostUnit: 2}).WithDerivedTotals()
	summary := summarizeRows([]Row{{Metrics: m}, {Metrics: m}})
	if summary.Days != 2 || summary.Totals.NetTotalMsat != 428000 || summary.Averages.KeysendSentMsat != 20000 ||
		summary.Averages.MarkedRevenueMsat != 40000 || summary.Averages.MarkedCostMsat != 6000 {
		t.Fatal(summary)
	}
}
