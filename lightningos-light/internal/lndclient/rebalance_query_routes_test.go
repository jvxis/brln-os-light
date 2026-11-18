package lndclient

import (
	"context"
	"errors"
	"testing"

	"lightningos-light/lnrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// queryRoutesFakeClient answers QueryRoutes from a script indexed by the
// request's UseMissionControl flag and records the flags it was asked with.
type queryRoutesFakeClient struct {
	lnrpc.LightningClient
	withMC    func() (*lnrpc.QueryRoutesResponse, error)
	withoutMC func() (*lnrpc.QueryRoutesResponse, error)
	flags     []bool
}

func (c *queryRoutesFakeClient) GetChanInfo(context.Context, *lnrpc.ChanInfoRequest, ...grpc.CallOption) (*lnrpc.ChannelEdge, error) {
	return nil, errors.New("edge unavailable")
}

func (c *queryRoutesFakeClient) QueryRoutes(_ context.Context, req *lnrpc.QueryRoutesRequest, _ ...grpc.CallOption) (*lnrpc.QueryRoutesResponse, error) {
	c.flags = append(c.flags, req.UseMissionControl)
	if req.UseMissionControl {
		return c.withMC()
	}
	return c.withoutMC()
}

func routeVia(chanID uint64) *lnrpc.QueryRoutesResponse {
	return &lnrpc.QueryRoutesResponse{Routes: []*lnrpc.Route{{Hops: []*lnrpc.Hop{{ChanId: 1}, {ChanId: chanID}}}}}
}

func noPath() (*lnrpc.QueryRoutesResponse, error) {
	return nil, status.Error(codes.Unknown, "unable to find a path to destination")
}

func TestQueryRoutesFallsBackWithoutMissionControl(t *testing.T) {
	const target = uint64(77)
	for _, tc := range []struct {
		name      string
		withMC    func() (*lnrpc.QueryRoutesResponse, error)
		withoutMC func() (*lnrpc.QueryRoutesResponse, error)
		wantRoute bool
		wantErr   bool
		wantFlags []bool
	}{
		{
			name:      "weighted search finds a route",
			withMC:    func() (*lnrpc.QueryRoutesResponse, error) { return routeVia(target), nil },
			withoutMC: func() (*lnrpc.QueryRoutesResponse, error) { t.Fatal("unexpected fallback"); return nil, nil },
			wantRoute: true,
			wantFlags: []bool{true},
		},
		{
			name:      "no path with mission control, route without it",
			withMC:    noPath,
			withoutMC: func() (*lnrpc.QueryRoutesResponse, error) { return routeVia(target), nil },
			wantRoute: true,
			wantFlags: []bool{true, false},
		},
		{
			name:      "empty answer with mission control, route without it",
			withMC:    func() (*lnrpc.QueryRoutesResponse, error) { return &lnrpc.QueryRoutesResponse{}, nil },
			withoutMC: func() (*lnrpc.QueryRoutesResponse, error) { return routeVia(target), nil },
			wantRoute: true,
			wantFlags: []bool{true, false},
		},
		{
			name:      "no path either way",
			withMC:    noPath,
			withoutMC: noPath,
			wantErr:   true,
			wantFlags: []bool{true, false},
		},
		{
			name: "transport error is not retried",
			withMC: func() (*lnrpc.QueryRoutesResponse, error) {
				return nil, status.Error(codes.Unavailable, "connection closed")
			},
			withoutMC: func() (*lnrpc.QueryRoutesResponse, error) { t.Fatal("unexpected fallback"); return nil, nil },
			wantErr:   true,
			wantFlags: []bool{true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &queryRoutesFakeClient{withMC: tc.withMC, withoutMC: tc.withoutMC}
			routes, err := queryRoutesWithClient(context.Background(), client, "02dest", 100_000, 5, "02ab", target, 150_000, 1, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v want error=%v", err, tc.wantErr)
			}
			if (len(routes) > 0) != tc.wantRoute {
				t.Fatalf("routes=%d want route=%v", len(routes), tc.wantRoute)
			}
			if len(client.flags) != len(tc.wantFlags) {
				t.Fatalf("mission control flags %v, want %v", client.flags, tc.wantFlags)
			}
			for i := range tc.wantFlags {
				if client.flags[i] != tc.wantFlags[i] {
					t.Fatalf("mission control flags %v, want %v", client.flags, tc.wantFlags)
				}
			}
		})
	}
}

func TestQueryRoutesNoFallbackOnceARouteIsCollected(t *testing.T) {
	// Second weighted query fails after one valid route: the caller gets the
	// routes it has, the search is not repeated without mission control.
	calls := 0
	client := &queryRoutesFakeClient{
		withMC: func() (*lnrpc.QueryRoutesResponse, error) {
			calls++
			if calls == 1 {
				return routeVia(77), nil
			}
			return noPath()
		},
		withoutMC: func() (*lnrpc.QueryRoutesResponse, error) { t.Fatal("unexpected fallback"); return nil, nil },
	}
	routes, err := queryRoutesWithClient(context.Background(), client, "02dest", 100_000, 5, "02ab", 77, 150_000, 3, nil, nil)
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes=%d err=%v", len(routes), err)
	}
}

func TestIsQueryRoutesNoPath(t *testing.T) {
	if !isQueryRoutesNoPath(status.Error(codes.Unknown, "unable to find a path to destination")) {
		t.Fatal("pathfinder answer must count as no path")
	}
	if isQueryRoutesNoPath(context.DeadlineExceeded) || isQueryRoutesNoPath(status.Error(codes.Unavailable, "closed")) || isQueryRoutesNoPath(nil) {
		t.Fatal("transport, deadline and nil must not count as no path")
	}
}
