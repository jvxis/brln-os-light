package privileged

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const testOnion = "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.onion"

type recordingTorLNDRESTManager struct {
	operation string
	dryRun    bool
	state     TorLNDRESTState
}

func (manager *recordingTorLNDRESTManager) Status(_ context.Context, dryRun bool) (TorLNDRESTState, error) {
	manager.operation, manager.dryRun = "status", dryRun
	return manager.state, nil
}

func (manager *recordingTorLNDRESTManager) Ensure(_ context.Context, dryRun bool) (TorLNDRESTState, error) {
	manager.operation, manager.dryRun = "ensure", dryRun
	return manager.state, nil
}

func TestFindLNDRESTServiceAcceptsHandWrittenBlocks(t *testing.T) {
	for _, test := range []struct {
		name, torrc, dir string
		found            bool
	}{
		{"none", "SocksPort 9050\nControlPort 127.0.0.1:9051\n", "", false},
		{"manual", "HiddenServiceDir /var/lib/tor/lnd-rest/\nHiddenServicePort 8080 127.0.0.1:8080\n", "/var/lib/tor/lnd-rest", true},
		{"localhost", "HiddenServiceDir /var/lib/tor/x\nHiddenServicePort 8080 localhost:8080\n", "/var/lib/tor/x", true},
		{"port only", "hiddenservicedir /var/lib/tor/y/\nhiddenserviceport 8080\n", "/var/lib/tor/y", true},
		{"port inline comment", "HiddenServiceDir /var/lib/tor/y/ # manual\nHiddenServicePort 8080 # local default\n", "/var/lib/tor/y", true},
		{"other port", "HiddenServiceDir /var/lib/tor/web/\nHiddenServicePort 80 127.0.0.1:3000\n", "", false},
		{"other target", "HiddenServiceDir /var/lib/tor/z/\nHiddenServicePort 8080 10.0.0.5:8080\n", "", false},
		{"commented", "#HiddenServiceDir /var/lib/tor/lnd-rest/\n#HiddenServicePort 8080 127.0.0.1:8080\n", "", false},
		{"port before dir", "HiddenServicePort 8080 127.0.0.1:8080\nHiddenServiceDir /var/lib/tor/a/\n", "", false},
		{"second service", "HiddenServiceDir /var/lib/tor/web/\nHiddenServicePort 80 127.0.0.1:80\nHiddenServiceDir /var/lib/tor/rest/\nHiddenServicePort 8080 127.0.0.1:8080\n", "/var/lib/tor/rest", true},
	} {
		dir, found := findLNDRESTService(test.torrc)
		if dir != test.dir || found != test.found {
			t.Errorf("%s: got %q %v, want %q %v", test.name, dir, found, test.dir, test.found)
		}
	}
}

func TestAppendLNDRESTServiceIsFixedAndFoundAgain(t *testing.T) {
	original := "SocksPort 9050\nControlPort 127.0.0.1:9051"
	updated := appendLNDRESTService(original)
	if !strings.HasPrefix(updated, original+"\n") {
		t.Fatalf("existing torrc was not kept: %q", updated)
	}
	if !strings.Contains(updated, "HiddenServiceDir "+torLNDRESTDir+"/\nHiddenServicePort 8080 127.0.0.1:8080\n") {
		t.Fatalf("fixed block missing: %q", updated)
	}
	dir, found := findLNDRESTService(updated)
	if !found || dir != torLNDRESTDir {
		t.Fatalf("appended block not detected: %q %v", dir, found)
	}
	if again, _ := findLNDRESTService(appendLNDRESTService(updated)); again != torLNDRESTDir {
		t.Fatalf("detection drifted after a second block: %q", again)
	}
}

func TestOnionHostnameAndDirectoryChecks(t *testing.T) {
	if !validOnionHostname(testOnion) {
		t.Fatal("valid v3 onion rejected")
	}
	for _, bad := range []string{"", "short.onion", strings.ToUpper(testOnion), testOnion + "\nextra", "abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuvwx.com"} {
		if validOnionHostname(bad) {
			t.Errorf("invalid hostname accepted: %q", bad)
		}
	}
	for dir, ok := range map[string]bool{
		"/var/lib/tor/lightningos-lnd-rest": true,
		"/var/lib/tor/lnd-rest":             true,
		"/var/lib/tor":                      false,
		"/var/lib/tor/../../etc":            false,
		"/etc/tor/keys":                     false,
		"/var/lib/torx/a":                   false,
		"relative/dir":                      false,
	} {
		if onionDirectoryAllowed(dir) != ok {
			t.Errorf("onionDirectoryAllowed(%q) != %v", dir, ok)
		}
	}
}

func TestTorLNDRESTRequestsAreClosedAndBrokered(t *testing.T) {
	for _, test := range []struct {
		operation Operation
		want      string
		locks     int
	}{
		{OperationTorLNDRESTStatus, "status", 0},
		{OperationTorLNDRESTEnsure, "ensure", 1},
	} {
		request := Request{Version: ProtocolVersion, RequestID: "tor_lnd_rest", Operation: test.operation, Params: json.RawMessage(`{}`)}
		if err := ValidateRequest(request); err != nil {
			t.Fatalf("valid request rejected: %v", err)
		}
		manager := &recordingTorLNDRESTManager{state: TorLNDRESTState{Status: "configured", Hostname: testOnion, RESTReachable: true}}
		locker := &recordingLocker{}
		broker := &Broker{Runner: &recordingRunner{}, Audit: &recordingAudit{}, Locker: locker, TorLNDREST: manager, Caller: "test"}
		response := broker.Handle(context.Background(), request)
		if !response.OK || manager.operation != test.want || manager.dryRun || locker.locks != test.locks {
			t.Fatalf("%s: response=%#v manager=%#v locker=%#v", test.operation, response, manager, locker)
		}
		if !knownOperation(test.operation) || mutatingOperation(test.operation) != (test.locks == 1) {
			t.Fatalf("%s is not classified correctly", test.operation)
		}
	}
}

func TestTorLNDRESTProtocolRejectsCallerInput(t *testing.T) {
	for _, operation := range []Operation{OperationTorLNDRESTStatus, OperationTorLNDRESTEnsure} {
		for _, params := range []string{`{"dir":"/root/.ssh"}`, `{"port":22}`, `{"target":"127.0.0.1:22"}`} {
			request := Request{Version: ProtocolVersion, RequestID: "tor_bad", Operation: operation, Params: json.RawMessage(params)}
			if err := ValidateRequest(request); err == nil {
				t.Errorf("%s accepted caller input %s", operation, params)
			}
		}
	}
}

func TestNativeTorLNDRESTDryRunTouchesNothing(t *testing.T) {
	runner := &recordingRunner{}
	manager := NewNativeTorLNDRESTManager(runner)
	for _, call := range []func(context.Context, bool) (TorLNDRESTState, error){manager.Status, manager.Ensure} {
		state, err := call(context.Background(), true)
		if err != nil || state.Status != "validated" || len(runner.commands) != 0 {
			t.Fatalf("dry run did something: state=%#v err=%v commands=%#v", state, err, runner.commands)
		}
	}
}

func TestClientTorLNDRESTValidatesTheResponse(t *testing.T) {
	transport := &fakeTransport{result: TorLNDRESTState{Status: "configured", Hostname: testOnion, RESTReachable: true}}
	client := NewClientWithTransport(ModeEnforce, time.Second, transport, nil)
	state, err := client.TorLNDREST(context.Background(), true)
	if err != nil || state.Hostname != testOnion || transport.request.Operation != OperationTorLNDRESTEnsure {
		t.Fatalf("unexpected ensure: %#v %v %#v", state, err, transport.request)
	}
	if _, err := client.TorLNDREST(context.Background(), false); err != nil || transport.request.Operation != OperationTorLNDRESTStatus {
		t.Fatalf("unexpected status request: %v %#v", err, transport.request)
	}
	transport.result = TorLNDRESTState{Status: "configured", Hostname: "evil.example.com"}
	if _, err := client.TorLNDREST(context.Background(), false); err == nil {
		t.Fatal("non-onion hostname accepted from the broker")
	}
	transport.result = TorLNDRESTState{Status: "hacked"}
	if _, err := client.TorLNDREST(context.Background(), false); err == nil {
		t.Fatal("unknown status accepted from the broker")
	}
}

func TestTorLNDRESTRefusesAmbiguousConfiguration(t *testing.T) {
	for _, config := range []string{
		"%include /etc/tor/conf.d/*.conf\n",
		"HiddenServiceDir \"/var/lib/tor/manual\"\n",
		"HiddenServiceDir /var/lib/tor/../other\n",
		"HiddenServiceDir " + torLNDRESTDir + "\nHiddenServicePort 22 127.0.0.1:22\n",
		appendLNDRESTService(appendLNDRESTService("")),
		"HiddenServicePort 8080 \\\n127.0.0.1:8080\n",
	} {
		if validateLNDRESTTorrc(config) == nil {
			t.Errorf("accepted ambiguous configuration: %q", config)
		}
	}
	for _, config := range []string{"SocksPort 9050\n", appendLNDRESTService(""), "HiddenServiceDir /var/lib/tor/lnd-rest/\nHiddenServicePort 8080 127.0.0.1:8080\n"} {
		if err := validateLNDRESTTorrc(config); err != nil {
			t.Fatal(err)
		}
	}
}
