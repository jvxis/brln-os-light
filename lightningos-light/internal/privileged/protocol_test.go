package privileged

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecodeRequestStrictValidation(t *testing.T) {
	valid := `{"version":1,"request_id":"request_1","operation":"service.restart","dry_run":true,"params":{"unit":"lnd","no_block":true}}`
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "valid", payload: valid},
		{name: "unknown top field", payload: `{"version":1,"request_id":"request_1","operation":"self_test","params":{},"command":"/bin/sh"}`, wantErr: true},
		{name: "unsupported version", payload: `{"version":2,"request_id":"request_1","operation":"self_test","params":{}}`, wantErr: true},
		{name: "invalid id", payload: `{"version":1,"request_id":"../../bad","operation":"self_test","params":{}}`, wantErr: true},
		{name: "unknown operation", payload: `{"version":1,"request_id":"request_1","operation":"/bin/sh","params":{}}`, wantErr: true},
		{name: "missing params", payload: `{"version":1,"request_id":"request_1","operation":"self_test"}`, wantErr: true},
		{name: "null params", payload: `{"version":1,"request_id":"request_1","operation":"self_test","params":null}`, wantErr: true},
		{name: "unknown params", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lnd","args":["; reboot"]}}`, wantErr: true},
		{name: "file path injection", payload: `{"version":1,"request_id":"request_1","operation":"files.enable_login","params":{"path":"/etc/shadow"}}`, wantErr: true},
		{name: "file content injection", payload: `{"version":1,"request_id":"request_1","operation":"files.enable_login","params":{"content":"root shell"}}`, wantErr: true},
		{name: "valid app lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start"}}`},
		{name: "unknown app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"mempool","action":"start"}}`, wantErr: true},
		{name: "shell app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer;reboot","action":"start"}}`, wantErr: true},
		{name: "unknown app action", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"exec"}}`, wantErr: true},
		{name: "app argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","args":["--privileged"]}}`, wantErr: true},
		{name: "app path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "shell unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lnd;reboot"}}`, wantErr: true},
		{name: "path unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"../../lnd"}}`, wantErr: true},
		{name: "unknown unit", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"ssh"}}`, wantErr: true},
		{name: "blocking manager restart", payload: `{"version":1,"request_id":"request_1","operation":"service.restart","params":{"unit":"lightningos-manager"}}`, wantErr: true},
		{name: "trailing object", payload: valid + `{}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeRequest(strings.NewReader(test.payload))
			if (err != nil) != test.wantErr {
				t.Fatalf("DecodeRequest() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestDecodeRequestRejectsOversizedMessage(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), MaxMessageBytes+1)
	if _, err := DecodeRequest(bytes.NewReader(payload)); err == nil {
		t.Fatal("expected oversized request to fail")
	}
}

func TestValidateServiceUnitAllowlist(t *testing.T) {
	for _, unit := range []string{"lnd", "lnd@default", "lightningos-manager", "postgresql"} {
		if err := ValidateServiceUnit(unit); err != nil {
			t.Fatalf("expected %q to be allowed: %v", unit, err)
		}
	}
	for _, unit := range []string{"", " lnd", "lnd ", "lnd.service", "docker", "ssh", "lnd;reboot", "../lnd"} {
		if err := ValidateServiceUnit(unit); err == nil {
			t.Fatalf("expected %q to be rejected", unit)
		}
	}
}

func FuzzDecodeRequest(f *testing.F) {
	f.Add([]byte(`{"version":1,"request_id":"fuzz_1","operation":"self_test","params":{}}`))
	f.Add([]byte(`{"version":1,"request_id":"fuzz_2","operation":"service.restart","params":{"unit":"lnd"}}`))
	f.Add([]byte(`{"version":1,"request_id":"bad","operation":"/bin/sh","params":{"unit":"lnd;reboot"}}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		request, err := DecodeRequest(bytes.NewReader(payload))
		if err == nil {
			if err := ValidateRequest(request); err != nil {
				t.Fatalf("accepted request did not validate: %v", err)
			}
		}
	})
}
