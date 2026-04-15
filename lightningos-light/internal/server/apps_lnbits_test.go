package server

import (
	"reflect"
	"testing"
)

func TestUpdateLndRestOptionsReplacesStaleManagedGateways(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.19.0.1:8080",
		"tlsextraip=172.18.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.18.0.1:8080",
		"# alias=LightningOS-Node",
		"color=#ff9900",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.17.0.1:8080",
		"# alias=LightningOS-Node",
		"color=#ff9900",
	}

	if !changed {
		t.Fatalf("expected change when stale gateway listeners exist")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestUpdateLndRestOptionsPreservesWildcardRestlisten(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.19.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.19.0.1:8080",
		"restlisten=0.0.0.0:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})
	want := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=0.0.0.0:8080",
		"# alias=LightningOS-Node",
	}

	if !changed {
		t.Fatalf("expected change when replacing stale managed block")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestUpdateLndRestOptionsNoChangeWhenManagedBlockIsCurrent(t *testing.T) {
	lines := []string{
		"[Application Options]",
		"tlsextraip=172.17.0.1",
		"tlsextradomain=host.docker.internal",
		"restlisten=172.17.0.1:8080",
		"# alias=LightningOS-Node",
	}

	got, changed := updateLndRestOptions(lines, []string{"172.17.0.1"})

	if changed {
		t.Fatalf("expected no change for current managed block")
	}
	if !reflect.DeepEqual(got, lines) {
		t.Fatalf("unexpected updated lines\nwant: %#v\ngot:  %#v", lines, got)
	}
}
