package reports

import (
  "context"
  "fmt"
  "strings"

  "lightningos-light/internal/lndclient"
  "lightningos-light/lnrpc"
)

const (
  forwardingPageSize = 50000
)

type RebalanceOverride struct {
  FeeMsat int64
  Count int64
}

func ComputeMetrics(ctx context.Context, lnd *lndclient.Client, tr TimeRange, memoMatch bool, override *RebalanceOverride) (Metrics, error) {
  if lnd == nil {
    return Metrics{}, fmt.Errorf("lnd client unavailable")
  }
  forwardRevenueMsat, forwardCount, routedVolumeMsat, err := fetchForwardingMetrics(ctx, lnd, tr.StartUnix(), tr.EndUnixInclusive())
  if err != nil {
    return Metrics{}, err
  }

  rebalanceCostMsat := int64(0)
  rebalanceCount := int64(0)
  if override != nil {
    rebalanceCostMsat = override.FeeMsat
    rebalanceCount = override.Count
  } else {
    rebalance, err := FetchRebalanceMetrics(ctx, lnd, tr.StartUnix(), tr.EndUnixInclusive(), memoMatch)
    if err != nil {
      return Metrics{}, err
    }
    rebalanceCostMsat = rebalance.FeeMsat
    rebalanceCount = rebalance.Count
  }

  netMsat := forwardRevenueMsat - rebalanceCostMsat
  metrics := Metrics{
    ForwardFeeRevenueSat: forwardRevenueMsat / 1000,
    ForwardFeeRevenueMsat: forwardRevenueMsat,
    RebalanceFeeCostSat: rebalanceCostMsat / 1000,
    RebalanceFeeCostMsat: rebalanceCostMsat,
    NetRoutingProfitSat: netMsat / 1000,
    NetRoutingProfitMsat: netMsat,
    ForwardCount: forwardCount,
    RebalanceCount: rebalanceCount,
    RoutedVolumeSat: routedVolumeMsat / 1000,
    RoutedVolumeMsat: routedVolumeMsat,
  }
  return metrics, nil
}

func fetchForwardingMetrics(ctx context.Context, lnd *lndclient.Client, startUnix uint64, endUnix uint64) (int64, int64, int64, error) {
  conn, err := lnd.DialLightning(ctx)
  if err != nil {
    return 0, 0, 0, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  var offset uint32
  var revenueMsat int64
  var routedVolumeMsat int64
  var count int64

  for {
    resp, err := client.ForwardingHistory(ctx, &lnrpc.ForwardingHistoryRequest{
      StartTime: startUnix,
      EndTime: endUnix,
      IndexOffset: offset,
      NumMaxEvents: forwardingPageSize,
    })
    if err != nil {
      return 0, 0, 0, err
    }
    if resp == nil || len(resp.ForwardingEvents) == 0 {
      break
    }

    for _, evt := range resp.ForwardingEvents {
      if evt == nil {
        continue
      }
      revenueMsat += extractForwardFeeMsat(evt)
      routedVolumeMsat += extractForwardAmountMsat(evt)
      count++
    }

    if resp.LastOffsetIndex <= offset {
      break
    }
    offset = resp.LastOffsetIndex
    if len(resp.ForwardingEvents) < forwardingPageSize {
      break
    }
  }

  return revenueMsat, count, routedVolumeMsat, nil
}

func fetchNodePubkey(ctx context.Context, lnd *lndclient.Client) (string, error) {
  cached := strings.TrimSpace(lnd.CachedPubkey())
  if cached != "" {
    return cached, nil
  }
  status, err := lnd.GetStatus(ctx)
  if err != nil {
    return "", err
  }
  if strings.TrimSpace(status.Pubkey) == "" {
    return "", fmt.Errorf("lnd pubkey unavailable")
  }
  return status.Pubkey, nil
}

type decodedPayReq struct {
  Destination string
  Description string
  Ready bool
}

func extractDestinationAndDescription(ctx context.Context, lnd *lndclient.Client, pay *lnrpc.Payment, cache map[string]decodedPayReq) (string, string) {
  if pay == nil {
    return "", ""
  }

  dest := ""
  description := ""

  for _, htlc := range pay.Htlcs {
    if htlc == nil || htlc.Route == nil || len(htlc.Route.Hops) == 0 {
      continue
    }
    if htlc.Status == lnrpc.HTLCAttempt_SUCCEEDED {
      dest = htlc.Route.Hops[len(htlc.Route.Hops)-1].PubKey
      break
    }
  }

  payreq := strings.TrimSpace(pay.PaymentRequest)
  if payreq == "" {
    return dest, description
  }

  if cached, ok := cache[payreq]; ok && cached.Ready {
    if dest == "" {
      dest = cached.Destination
    }
    description = cached.Description
    return dest, description
  }

  decoded, err := lnd.DecodeInvoice(ctx, payreq)
  if err != nil {
    cache[payreq] = decodedPayReq{Ready: true}
    return dest, description
  }

  entry := decodedPayReq{
    Destination: decoded.Destination,
    Description: decoded.Memo,
    Ready: true,
  }
  cache[payreq] = entry
  if dest == "" {
    dest = entry.Destination
  }
  description = entry.Description
  return dest, description
}

func extractForwardFeeMsat(evt *lnrpc.ForwardingEvent) int64 {
  if evt == nil {
    return 0
  }
  if evt.FeeMsat != 0 {
    return int64(evt.FeeMsat)
  }
  if evt.Fee != 0 {
    return int64(evt.Fee) * 1000
  }
  return 0
}

func extractForwardAmountMsat(evt *lnrpc.ForwardingEvent) int64 {
  if evt == nil {
    return 0
  }
  if evt.AmtOutMsat != 0 {
    return int64(evt.AmtOutMsat)
  }
  if evt.AmtOut != 0 {
    return int64(evt.AmtOut) * 1000
  }
  return 0
}

func extractPaymentTimestamp(pay *lnrpc.Payment) int64 {
  if pay == nil {
    return 0
  }
  if pay.CreationDate != 0 {
    return int64(pay.CreationDate)
  }
  if pay.CreationTimeNs != 0 {
    return int64(pay.CreationTimeNs / 1_000_000_000)
  }
  return 0
}

func extractPaymentFeeMsat(pay *lnrpc.Payment) int64 {
  if pay == nil {
    return 0
  }
  if pay.FeeMsat != 0 {
    return int64(pay.FeeMsat)
  }
  if pay.FeeSat != 0 {
    return int64(pay.FeeSat) * 1000
  }
  if pay.Fee != 0 {
    return int64(pay.Fee) * 1000
  }
  if msat := paymentRouteFeeMsat(pay); msat != 0 {
    return msat
  }
  return 0
}

func paymentRouteFeeMsat(pay *lnrpc.Payment) int64 {
  if pay == nil {
    return 0
  }
  for _, attempt := range pay.Htlcs {
    if attempt == nil || attempt.Route == nil {
      continue
    }
    if attempt.Status != lnrpc.HTLCAttempt_SUCCEEDED {
      continue
    }
    if attempt.Route.TotalFeesMsat != 0 {
      return int64(attempt.Route.TotalFeesMsat)
    }
    if attempt.Route.TotalFees != 0 {
      return int64(attempt.Route.TotalFees) * 1000
    }
  }
  for _, attempt := range pay.Htlcs {
    if attempt == nil || attempt.Route == nil {
      continue
    }
    if attempt.Route.TotalFeesMsat != 0 {
      return int64(attempt.Route.TotalFeesMsat)
    }
    if attempt.Route.TotalFees != 0 {
      return int64(attempt.Route.TotalFees) * 1000
    }
  }
  return 0
}
