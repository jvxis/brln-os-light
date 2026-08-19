package reports

import "time"

type Metrics struct {
	ForwardFeeRevenueSat       int64
	ForwardFeeRevenueMsat      int64
	RebalanceFeeCostSat        int64
	RebalanceFeeCostMsat       int64
	PaymentFeeCostSat          int64
	PaymentFeeCostMsat         int64
	OnchainFeeCostSat          int64
	OnchainFeeCostMsat         int64
	OnchainCoopCloseCostSat    int64
	OnchainCoopCloseCostMsat   int64
	OnchainLocalForceCostSat   int64
	OnchainLocalForceCostMsat  int64
	OnchainRemoteForceCostSat  int64
	OnchainRemoteForceCostMsat int64
	KeysendReceivedSat         int64
	KeysendReceivedMsat        int64
	KeysendReceivedCount       int64
	KeysendSentSat             int64
	KeysendSentMsat            int64
	KeysendSentCount           int64
	// Channel sales (Magma). Revenue only: the funding transaction fee is
	// already inside OnchainFeeCost, so counting it here too would double it.
	SalesRevenueSat      int64
	SalesRevenueMsat     int64
	SalesCount           int64
	NetRoutingProfitSat  int64
	NetRoutingProfitMsat int64
	NetWithKeysendSat    int64
	NetWithKeysendMsat   int64
	// NetTotal adds every non-routing income stream on top of the routing net.
	// NetRoutingProfit keeps meaning routing alone.
	NetTotalSat                int64
	NetTotalMsat               int64
	ForwardCount               int64
	RebalanceCount             int64
	RebalanceVolumeSat         int64
	RebalanceVolumeMsat        int64
	PaymentCount               int64
	RoutedVolumeSat            int64
	RoutedVolumeMsat           int64
	OnchainBalanceSat          *int64
	LightningBalanceSat        *int64
	TotalBalanceSat            *int64
	ProvenanceLastSyncAt       *time.Time `json:"provenance_last_sync_at,omitempty"`
	ProvenanceLastSyncAgeHours *float64   `json:"provenance_last_sync_age_hours,omitempty"`
	ProvenanceHealthAlert      *bool      `json:"provenance_health_alert,omitempty"`
	ProvenanceLastError        *string    `json:"provenance_last_error,omitempty"`
}

func (m Metrics) OffchainFeeCostSat() int64 {
	return m.RebalanceFeeCostSat + m.PaymentFeeCostSat
}

func (m Metrics) OffchainFeeCostMsat() int64 {
	return m.RebalanceFeeCostMsat + m.PaymentFeeCostMsat
}

// WithMagmaSales folds a channel-sale contribution into the metrics and keeps
// the derived net figures consistent.
func (m Metrics) WithMagmaSales(sales MagmaSalesRevenue) Metrics {
	m.SalesRevenueMsat = sales.RevenueMsat
	m.SalesRevenueSat = sales.RevenueMsat / 1000
	m.SalesCount = sales.Count
	return m.withNetTotal()
}

// TotalRevenueMsat is everything the node earned in the period.
func (m Metrics) TotalRevenueMsat() int64 {
	return m.ForwardFeeRevenueMsat + m.KeysendReceivedMsat + m.SalesRevenueMsat
}

func (m Metrics) TotalRevenueSat() int64 { return m.TotalRevenueMsat() / 1000 }

// TotalCostMsat is everything it cost to run it. On-chain used to be collected,
// displayed, and then left out of the bottom line, which made the total read
// better than reality: opening and closing channels were free in the one number
// meant to say whether the node made money.
func (m Metrics) TotalCostMsat() int64 {
	return m.RebalanceFeeCostMsat + m.PaymentFeeCostMsat + m.OnchainFeeCostMsat +
		m.KeysendSentMsat
}

func (m Metrics) TotalCostSat() int64 { return m.TotalCostMsat() / 1000 }

func (m Metrics) withNetTotal() Metrics {
	m.NetTotalMsat = m.TotalRevenueMsat() - m.TotalCostMsat()
	m.NetTotalSat = m.NetTotalMsat / 1000
	return m
}

func (m Metrics) TotalFeeCostSat() int64 {
	return m.OffchainFeeCostSat()
}

func (m Metrics) TotalFeeCostMsat() int64 {
	return m.OffchainFeeCostMsat()
}

func (m Metrics) TotalFeeCostWithOnchainSat() int64 {
	return m.OffchainFeeCostSat() + m.OnchainFeeCostSat
}

func (m Metrics) TotalFeeCostWithOnchainMsat() int64 {
	return m.OffchainFeeCostMsat() + m.OnchainFeeCostMsat
}

func (m Metrics) OnchainCloseCostSat() int64 {
	return m.OnchainCoopCloseCostSat + m.OnchainLocalForceCostSat + m.OnchainRemoteForceCostSat
}

func (m Metrics) OnchainCloseCostMsat() int64 {
	return m.OnchainCoopCloseCostMsat + m.OnchainLocalForceCostMsat + m.OnchainRemoteForceCostMsat
}

type Row struct {
	ReportDate time.Time
	Metrics    Metrics
}

type Summary struct {
	Days              int64
	Totals            Metrics
	Averages          Metrics
	MovementTargetSat int64
	MovementPct       float64
}

type TimeRange struct {
	StartLocal time.Time
	EndLocal   time.Time
	StartUTC   time.Time
	EndUTC     time.Time
}

func (tr TimeRange) StartUnix() uint64 {
	return uint64(tr.StartUTC.Unix())
}

func (tr TimeRange) EndUnixInclusive() uint64 {
	end := tr.EndUTC
	if isMidnight(tr.EndLocal) {
		end = end.Add(-time.Second)
	}
	if end.Before(tr.StartUTC) {
		end = tr.StartUTC
	}
	return uint64(end.Unix())
}

func isMidnight(t time.Time) bool {
	return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0
}

type MovementLive struct {
	Date            time.Time
	Start           time.Time
	End             time.Time
	Timezone        string
	TargetSat       int64
	RoutedVolumeSat float64
	MovementPct     float64
}
