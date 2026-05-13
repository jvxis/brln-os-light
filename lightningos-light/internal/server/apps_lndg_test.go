package server

import (
	"reflect"
	"testing"
)

func TestUpdateLndGrpcOptionsRemovesStaleDockerBridgeListeners(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=172.19.0.1:10009",
		"rpclisten=127.0.0.1:10009",
		"alias=LightningOS-Node",
	}

	got, changed := updateLndGrpcOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"rpclisten=127.0.0.1:10009",
		"rpclisten=172.17.0.1:10009",
		"alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected stale Docker listener cleanup")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected LND config\nwant: %#v\ngot:  %#v", want, got)
	}
}
