package lndclient

import (
  "context"
  "crypto/rand"
  "encoding/hex"
  "errors"
  "fmt"
  "strings"
  "time"

  "lightningos-light/lnrpc"
  "lightningos-light/lnrpc/routerrpc"
)

type RouteFailureError struct {
  Code lnrpc.Failure_FailureCode
  FailureSourceIndex uint32
  Failure *lnrpc.Failure
}

func (e RouteFailureError) Error() string {
  if e.Failure != nil {
    return fmt.Sprintf("route failed: %s", e.Failure.Code.String())
  }
  if e.Code != lnrpc.Failure_UNKNOWN_FAILURE {
    return fmt.Sprintf("route failed: %s", e.Code.String())
  }
  return "route failed"
}

type ChannelPolicySnapshot struct {
  FeeRatePpm int64
  BaseFeeMsat int64
  InboundFeeRatePpm int64
  InboundBaseMsat int64
  TimeLockDelta int64
  Disabled bool
}

type ChannelPolicies struct {
  ChannelID uint64
  LocalPubkey string
  RemotePubkey string
  Local ChannelPolicySnapshot
  Remote ChannelPolicySnapshot
}

func (c *Client) SelfPubkey(ctx context.Context) (string, error) {
  if cached := strings.TrimSpace(c.CachedPubkey()); cached != "" {
    return cached, nil
  }
  status, err := c.GetStatus(ctx)
  if err == nil {
    if status.Pubkey != "" {
      return status.Pubkey, nil
    }
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return "", err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  info, err := client.GetInfo(ctx, &lnrpc.GetInfoRequest{})
  if err != nil {
    return "", err
  }
  return strings.TrimSpace(info.IdentityPubkey), nil
}

func (c *Client) GetChannelPolicies(ctx context.Context, channelID uint64) (ChannelPolicies, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return ChannelPolicies{}, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  edge, err := client.GetChanInfo(ctx, &lnrpc.ChanInfoRequest{ChanId: channelID})
  if err != nil {
    return ChannelPolicies{}, err
  }
  if edge == nil {
    return ChannelPolicies{}, errors.New("channel policy unavailable")
  }

  pubkey, err := c.SelfPubkey(ctx)
  if err != nil {
    return ChannelPolicies{}, err
  }
  pubkey = strings.TrimSpace(pubkey)
  if pubkey == "" {
    return ChannelPolicies{}, errors.New("local pubkey unavailable")
  }

  policyToSnapshot := func(policy *lnrpc.RoutingPolicy) ChannelPolicySnapshot {
    if policy == nil {
      return ChannelPolicySnapshot{}
    }
    return ChannelPolicySnapshot{
      FeeRatePpm: int64(policy.FeeRateMilliMsat),
      BaseFeeMsat: int64(policy.FeeBaseMsat),
      InboundFeeRatePpm: int64(policy.InboundFeeRateMilliMsat),
      InboundBaseMsat: int64(policy.InboundFeeBaseMsat),
      TimeLockDelta: int64(policy.TimeLockDelta),
      Disabled: policy.Disabled,
    }
  }

  local := ChannelPolicySnapshot{}
  remote := ChannelPolicySnapshot{}
  localPubkey := ""
  remotePubkey := ""

  if strings.EqualFold(edge.Node1Pub, pubkey) {
    local = policyToSnapshot(edge.Node1Policy)
    remote = policyToSnapshot(edge.Node2Policy)
    localPubkey = edge.Node1Pub
    remotePubkey = edge.Node2Pub
  } else if strings.EqualFold(edge.Node2Pub, pubkey) {
    local = policyToSnapshot(edge.Node2Policy)
    remote = policyToSnapshot(edge.Node1Policy)
    localPubkey = edge.Node2Pub
    remotePubkey = edge.Node1Pub
  } else {
    return ChannelPolicies{}, fmt.Errorf("local pubkey not found on channel %d", channelID)
  }

  return ChannelPolicies{
    ChannelID: channelID,
    LocalPubkey: localPubkey,
    RemotePubkey: remotePubkey,
    Local: local,
    Remote: remote,
  }, nil
}

func (c *Client) GetMaxHtlcMsat(ctx context.Context, channelID uint64, fromPubkey string, toPubkey string) (uint64, error) {
  conn, err := c.dial(ctx, true)
  if err != nil {
    return 0, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)
  edge, err := client.GetChanInfo(ctx, &lnrpc.ChanInfoRequest{ChanId: channelID})
  if err != nil {
    return 0, err
  }
  if edge == nil {
    return 0, errors.New("channel policy unavailable")
  }

  fromPubkey = strings.TrimSpace(fromPubkey)
  toPubkey = strings.TrimSpace(toPubkey)
  if fromPubkey == "" || toPubkey == "" {
    return 0, errors.New("route pubkeys required")
  }

  var policy *lnrpc.RoutingPolicy
  switch {
  case strings.EqualFold(edge.Node1Pub, fromPubkey) && strings.EqualFold(edge.Node2Pub, toPubkey):
    policy = edge.Node1Policy
  case strings.EqualFold(edge.Node2Pub, fromPubkey) && strings.EqualFold(edge.Node1Pub, toPubkey):
    policy = edge.Node2Policy
  default:
    return 0, fmt.Errorf("route pubkeys not found on channel %d", channelID)
  }
  if policy == nil {
    return 0, errors.New("channel policy unavailable")
  }

  maxMsat := policy.MaxHtlcMsat
  capMsat := uint64(edge.Capacity) * 1000
  if capMsat > 0 {
    if maxMsat == 0 || maxMsat > capMsat {
      maxMsat = capMsat
    }
  }
  return maxMsat, nil
}

func (c *Client) QueryRoute(ctx context.Context, destPubkey string, amtSat int64, outgoingChanID uint64, lastHopPubkey string, feeLimitMsat int64, ignoredEdges []*lnrpc.EdgeLocator, ignoredPairs []*lnrpc.NodePair) (*lnrpc.Route, error) {
  routes, err := c.QueryRoutes(ctx, destPubkey, amtSat, outgoingChanID, lastHopPubkey, feeLimitMsat, 1, ignoredEdges, ignoredPairs)
  if err != nil {
    return nil, err
  }
  if len(routes) == 0 {
    return nil, errors.New("no route")
  }
  return routes[0], nil
}

func (c *Client) QueryRoutes(ctx context.Context, destPubkey string, amtSat int64, outgoingChanID uint64, lastHopPubkey string, feeLimitMsat int64, numRoutes int32, ignoredEdges []*lnrpc.EdgeLocator, ignoredPairs []*lnrpc.NodePair) ([]*lnrpc.Route, error) {
  trimmedDest := strings.TrimSpace(destPubkey)
  if trimmedDest == "" {
    return nil, errors.New("dest pubkey required")
  }
  if amtSat <= 0 {
    return nil, errors.New("amount must be positive")
  }
  if numRoutes <= 0 {
    numRoutes = 1
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return nil, err
  }
  defer conn.Close()

  client := lnrpc.NewLightningClient(conn)

  routes := make([]*lnrpc.Route, 0, numRoutes)
  baseIgnoredEdges := make([]*lnrpc.EdgeLocator, 0, len(ignoredEdges))
  if len(ignoredEdges) > 0 {
    baseIgnoredEdges = append(baseIgnoredEdges, ignoredEdges...)
  }
  ignoredByRoute := make([]*lnrpc.EdgeLocator, 0)

  for i := int32(0); i < numRoutes; i++ {
    requestIgnoredEdges := baseIgnoredEdges
    if len(ignoredByRoute) > 0 {
      requestIgnoredEdges = append(requestIgnoredEdges, ignoredByRoute...)
    }
    req := &lnrpc.QueryRoutesRequest{
      PubKey: trimmedDest,
      Amt: amtSat,
      OutgoingChanId: outgoingChanID,
      IgnoredEdges: requestIgnoredEdges,
      IgnoredPairs: ignoredPairs,
      UseMissionControl: true,
    }
    if feeLimitMsat > 0 {
      req.FeeLimit = &lnrpc.FeeLimit{
        Limit: &lnrpc.FeeLimit_FixedMsat{FixedMsat: feeLimitMsat},
      }
    }
    if trimmedHop := strings.TrimSpace(lastHopPubkey); trimmedHop != "" {
      hopBytes, err := hex.DecodeString(trimmedHop)
      if err != nil {
        return nil, fmt.Errorf("invalid last hop pubkey")
      }
      req.LastHopPubkey = hopBytes
    }

    resp, err := client.QueryRoutes(ctx, req)
    if err != nil {
      if len(routes) > 0 {
        break
      }
      return nil, err
    }
    if resp == nil || len(resp.Routes) == 0 {
      break
    }
    route := resp.Routes[0]
    routes = append(routes, route)
    ignoredByRoute = append(ignoredByRoute, routeToEdgeLocators(route)...)
  }

  if len(routes) == 0 {
    return nil, errors.New("no route")
  }
  return routes, nil
}

func (c *Client) SendToRoute(ctx context.Context, paymentHash string, route *lnrpc.Route) (*lnrpc.HTLCAttempt, error) {
  if route == nil {
    return nil, errors.New("route required")
  }
  trimmed := strings.TrimSpace(paymentHash)
  if trimmed == "" {
    return nil, errors.New("payment hash required")
  }
  hashBytes, err := hex.DecodeString(trimmed)
  if err != nil {
    return nil, errors.New("invalid payment hash")
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return nil, err
  }
  defer conn.Close()

  router := routerrpc.NewRouterClient(conn)
  resp, err := router.SendToRouteV2(ctx, &routerrpc.SendToRouteRequest{
    PaymentHash: hashBytes,
    Route: route,
  })
  if err != nil {
    return nil, err
  }
  if resp == nil {
    return nil, errors.New("empty send response")
  }
  if resp.Status != lnrpc.HTLCAttempt_SUCCEEDED {
    if resp.Failure != nil {
      return resp, RouteFailureError{
        Code: resp.Failure.Code,
        FailureSourceIndex: resp.Failure.FailureSourceIndex,
        Failure: resp.Failure,
      }
    }
    return resp, RouteFailureError{Code: lnrpc.Failure_UNKNOWN_FAILURE}
  }
  return resp, nil
}

func (c *Client) BuildRoute(ctx context.Context, amountSat int64, outgoingChanID uint64, hopPubkeys []string) (*lnrpc.Route, error) {
  if amountSat <= 0 {
    return nil, errors.New("amount must be positive")
  }
  if outgoingChanID == 0 {
    return nil, errors.New("outgoing channel required")
  }
  if len(hopPubkeys) == 0 {
    return nil, errors.New("hop pubkeys required")
  }

  hopBytes := make([][]byte, 0, len(hopPubkeys))
  for _, pk := range hopPubkeys {
    trimmed := strings.TrimSpace(pk)
    if trimmed == "" {
      return nil, errors.New("invalid hop pubkey")
    }
    b, err := hex.DecodeString(trimmed)
    if err != nil {
      return nil, errors.New("invalid hop pubkey")
    }
    hopBytes = append(hopBytes, b)
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return nil, err
  }
  defer conn.Close()

  router := routerrpc.NewRouterClient(conn)
  resp, err := router.BuildRoute(ctx, &routerrpc.BuildRouteRequest{
    AmtMsat: amountSat * 1000,
    OutgoingChanId: outgoingChanID,
    HopPubkeys: hopBytes,
    FinalCltvDelta: 144,
  })
  if err != nil {
    return nil, err
  }
  if resp == nil || resp.Route == nil {
    return nil, errors.New("empty route response")
  }
  return resp.Route, nil
}

func RandomPaymentHash() string {
  buf := make([]byte, 32)
  _, _ = rand.Read(buf)
  return hex.EncodeToString(buf)
}

func routeToEdgeLocators(route *lnrpc.Route) []*lnrpc.EdgeLocator {
  if route == nil || len(route.Hops) == 0 {
    return nil
  }
  edges := make([]*lnrpc.EdgeLocator, 0, len(route.Hops)*2)
  for _, hop := range route.Hops {
    if hop == nil {
      continue
    }
    edges = append(edges, &lnrpc.EdgeLocator{
      ChannelId: hop.ChanId,
      DirectionReverse: false,
    }, &lnrpc.EdgeLocator{
      ChannelId: hop.ChanId,
      DirectionReverse: true,
    })
  }
  return edges
}

func (c *Client) SendPaymentWithConstraints(ctx context.Context, paymentRequest string, outgoingChanID uint64, lastHopPubkey string, feeLimitMsat int64, timeoutSec int32, maxParts uint32) (*lnrpc.Payment, error) {
  trimmed := strings.TrimSpace(paymentRequest)
  if trimmed == "" {
    return nil, errors.New("payment_request required")
  }

  if timeoutSec <= 0 {
    timeoutSec = 60
  }
  if maxParts == 0 {
    maxParts = 3
  }

  conn, err := c.dial(ctx, true)
  if err != nil {
    return nil, err
  }
  defer conn.Close()

  router := routerrpc.NewRouterClient(conn)
  req := &routerrpc.SendPaymentRequest{
    PaymentRequest: trimmed,
    TimeoutSeconds: timeoutSec,
    OutgoingChanId: outgoingChanID,
    AllowSelfPayment: true,
    MaxParts: maxParts,
    NoInflightUpdates: true,
  }
  if feeLimitMsat > 0 {
    req.FeeLimitMsat = feeLimitMsat
  }
  if trimmedHop := strings.TrimSpace(lastHopPubkey); trimmedHop != "" {
    hopBytes, err := hex.DecodeString(trimmedHop)
    if err != nil {
      return nil, fmt.Errorf("invalid last hop pubkey")
    }
    req.LastHopPubkey = hopBytes
  }

  stream, err := router.SendPaymentV2(ctx, req)
  if err != nil {
    return nil, err
  }

  deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
  for {
    if time.Now().After(deadline) {
      return nil, errors.New("payment timeout")
    }
    payment, err := stream.Recv()
    if err != nil {
      return nil, err
    }
    if payment == nil {
      continue
    }
    switch payment.Status {
    case lnrpc.Payment_SUCCEEDED:
      return payment, nil
    case lnrpc.Payment_FAILED:
      if payment.FailureReason != lnrpc.PaymentFailureReason_FAILURE_REASON_NONE {
        return nil, fmt.Errorf("payment failed: %s", payment.FailureReason.String())
      }
      return nil, errors.New("payment failed")
    default:
    }
  }
}
