package system

import (
	"context"
	"errors"
	"testing"
)

type fakeLNDObservabilityClient struct {
	*fakePrivilegedServiceClient
	sizeBytes int64
	err       error
	calls     int
}

func (client *fakeLNDObservabilityClient) LNDChannelDBSize(context.Context) (int64, error) {
	client.calls++
	return client.sizeBytes, client.err
}

func TestReadLNDChannelDBSizeWithBrokerUsesEnforcedReadOnlyCapability(t *testing.T) {
	client := &fakeLNDObservabilityClient{
		fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: "enforce"},
		sizeBytes:                   9876,
	}
	ConfigurePrivilegedClient(client)
	t.Cleanup(func() { ConfigurePrivilegedClient(nil) })

	sizeBytes, handled, err := ReadLNDChannelDBSizeWithBroker(context.Background())
	if !handled || err != nil || sizeBytes != 9876 || client.calls != 1 {
		t.Fatalf("size/handled/error/calls=%d/%v/%v/%d", sizeBytes, handled, err, client.calls)
	}

	client.err = errors.New("unavailable")
	if _, handled, err := ReadLNDChannelDBSizeWithBroker(context.Background()); !handled || err == nil {
		t.Fatalf("broker read error was not preserved: handled=%v err=%v", handled, err)
	}

	client.mode = "shadow"
	client.err = nil
	if sizeBytes, handled, err := ReadLNDChannelDBSizeWithBroker(context.Background()); handled || err != nil || sizeBytes != 0 || client.calls != 2 {
		t.Fatalf("shadow unexpectedly replaced compatibility path: size/handled/error/calls=%d/%v/%v/%d", sizeBytes, handled, err, client.calls)
	}
}
