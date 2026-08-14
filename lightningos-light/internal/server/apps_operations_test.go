package server

import "testing"

func TestAppOperationLifecycle(t *testing.T) {
	s := &Server{}

	finish, started := s.beginAppOperation("electrs", "install")
	if !started {
		t.Fatal("expected first operation to start")
	}

	operation, active := s.currentAppOperation("electrs")
	if !active {
		t.Fatal("expected operation to be active")
	}
	if operation.Action != "install" {
		t.Fatalf("expected install action, got %q", operation.Action)
	}
	if operation.StartedAt.IsZero() {
		t.Fatal("expected operation start time")
	}
	if operation.StartedAt.Location().String() != "UTC" {
		t.Fatalf("expected UTC start time, got %s", operation.StartedAt.Location())
	}
	snapshot := s.appOperationSnapshot()
	if len(snapshot) != 1 || snapshot["electrs"].Action != "install" {
		t.Fatalf("unexpected operation snapshot: %#v", snapshot)
	}

	if _, duplicateStarted := s.beginAppOperation("electrs", "stop"); duplicateStarted {
		t.Fatal("expected concurrent operation for the same app to be rejected")
	}

	finish()
	if _, active := s.currentAppOperation("electrs"); active {
		t.Fatal("expected operation to be cleared after completion")
	}
	if snapshot := s.appOperationSnapshot(); len(snapshot) != 0 {
		t.Fatalf("expected empty operation snapshot, got %#v", snapshot)
	}
}

func TestAppOperationCompletionCannotClearNewerOperation(t *testing.T) {
	s := &Server{}

	finishFirst, started := s.beginAppOperation("electrs", "install")
	if !started {
		t.Fatal("expected first operation to start")
	}
	firstToken := s.appOperations["electrs"].token
	s.finishAppOperation("electrs", firstToken)

	finishSecond, started := s.beginAppOperation("electrs", "start")
	if !started {
		t.Fatal("expected second operation to start")
	}
	finishFirst()

	operation, active := s.currentAppOperation("electrs")
	if !active || operation.Action != "start" {
		t.Fatalf("expected newer start operation to remain active, got %#v, active=%v", operation, active)
	}
	finishSecond()
}

func TestAppOperationRejectsInvalidInput(t *testing.T) {
	s := &Server{}
	if _, started := s.beginAppOperation("", "install"); started {
		t.Fatal("expected empty app id to be rejected")
	}
	if _, started := s.beginAppOperation("electrs", ""); started {
		t.Fatal("expected empty action to be rejected")
	}
}
