package server

import (
	"context"
	"testing"

	"lightningos-light/internal/system"
)

type channelDBBrokerTestClient struct {
	*cpuMinerPrivilegedClient
	sizeBytes int64
	calls     int
}

func (client *channelDBBrokerTestClient) LNDChannelDBSize(context.Context) (int64, error) {
	client.calls++
	return client.sizeBytes, nil
}

func TestLNDChannelDBSizeUsesBrokerInEnforceMode(t *testing.T) {
	client := &channelDBBrokerTestClient{
		cpuMinerPrivilegedClient: &cpuMinerPrivilegedClient{mode: "enforce"},
		sizeBytes:                21_165_629_440,
	}
	system.ConfigurePrivilegedClient(client)
	t.Cleanup(func() { system.ConfigurePrivilegedClient(nil) })

	sizeBytes, err := lndChannelDBSizeBytes(context.Background())
	if err != nil || sizeBytes != client.sizeBytes || client.calls != 1 {
		t.Fatalf("size/error/calls=%d/%v/%d", sizeBytes, err, client.calls)
	}
	sizeGB, err := lndChannelDBSizeGB(context.Background())
	if err != nil || sizeGB != float64(client.sizeBytes)/1_000_000_000 || client.calls != 2 {
		t.Fatalf("sizeGB/error/calls=%f/%v/%d", sizeGB, err, client.calls)
	}
}
