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
	NetRoutingProfitSat        int64
	NetRoutingProfitMsat       int64
	NetWithKeysendSat          int64
	NetWithKeysendMsat         int64
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
}

func (m Metrics) OffchainFeeCostSat() int64 {
	return m.RebalanceFeeCostSat + m.PaymentFeeCostSat
}

func (m Metrics) OffchainFeeCostMsat() int64 {
	return m.RebalanceFeeCostMsat + m.PaymentFeeCostMsat
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
