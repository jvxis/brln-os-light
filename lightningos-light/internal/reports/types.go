package reports

import "time"

type Metrics struct {
  ForwardFeeRevenueSat int64
  ForwardFeeRevenueMsat int64
  RebalanceFeeCostSat int64
  RebalanceFeeCostMsat int64
  NetRoutingProfitSat int64
  NetRoutingProfitMsat int64
  ForwardCount int64
  RebalanceCount int64
  RoutedVolumeSat int64
  RoutedVolumeMsat int64
  OnchainBalanceSat *int64
  LightningBalanceSat *int64
  TotalBalanceSat *int64
}

type Row struct {
  ReportDate time.Time
  Metrics Metrics
}

type Summary struct {
  Days int64
  Totals Metrics
  Averages Metrics
}

type TimeRange struct {
  StartLocal time.Time
  EndLocal time.Time
  StartUTC time.Time
  EndUTC time.Time
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
