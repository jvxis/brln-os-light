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
		{name: "valid robosats lifecycle", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"robosats","action":"stop"}}`},
		{name: "unknown app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"mempool","action":"start"}}`, wantErr: true},
		{name: "shell app", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer;reboot","action":"start"}}`, wantErr: true},
		{name: "unknown app action", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"exec"}}`, wantErr: true},
		{name: "app argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","args":["--privileged"]}}`, wantErr: true},
		{name: "app path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.lifecycle","params":{"app_id":"cpuminer","action":"start","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid app inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer"}}`},
		{name: "valid robosats inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"robosats"}}`},
		{name: "app inspect dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","dry_run":true,"params":{"app_id":"cpuminer"}}`, wantErr: true},
		{name: "unknown app inspect", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"mempool"}}`, wantErr: true},
		{name: "app inspect argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer","args":["--privileged"]}}`, wantErr: true},
		{name: "app inspect path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.inspect","params":{"app_id":"cpuminer","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid app remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer"}}`},
		{name: "valid robosats remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"robosats"}}`},
		{name: "valid app remove dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","dry_run":true,"params":{"app_id":"cpuminer"}}`},
		{name: "unknown app remove", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"mempool"}}`, wantErr: true},
		{name: "app remove argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer","args":["--volumes"]}}`, wantErr: true},
		{name: "app remove path injection", payload: `{"version":1,"request_id":"request_1","operation":"app.compose.remove","params":{"app_id":"cpuminer","compose_path":"/tmp/evil.yaml"}}`, wantErr: true},
		{name: "valid docker ensure", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","params":{}}`},
		{name: "valid docker ensure dry run", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","dry_run":true,"params":{}}`},
		{name: "docker ensure arguments", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.ensure","params":{"packages":["docker.io"]}}`, wantErr: true},
		{name: "valid docker status", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.status","params":{}}`},
		{name: "docker status dry run", payload: `{"version":1,"request_id":"request_1","operation":"docker.runtime.status","dry_run":true,"params":{}}`, wantErr: true},
		{name: "valid package ensure", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime"}}`},
		{name: "valid package ensure dry run", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","dry_run":true,"params":{"feature":"docker_runtime"}}`},
		{name: "valid package status", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.status","params":{"feature":"docker_runtime"}}`},
		{name: "package status dry run", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.status","dry_run":true,"params":{"feature":"docker_runtime"}}`, wantErr: true},
		{name: "unknown package feature", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime;reboot"}}`, wantErr: true},
		{name: "package argument injection", payload: `{"version":1,"request_id":"request_1","operation":"packages.feature.ensure","params":{"feature":"docker_runtime","packages":["docker.io"]}}`, wantErr: true},
		{name: "valid image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline"}}`},
		{name: "valid robosats image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"robosats","variant":"client"}}`},
		{name: "valid bitcoin core image prepare", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"bitcoincore","variant":"node"}}`},
		{name: "valid image prepare dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","dry_run":true,"params":{"app_id":"cpuminer","variant":"fast_pinned"}}`},
		{name: "valid image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"cpuminer","variant":"fast_latest"}}`},
		{name: "valid robosats image status", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","params":{"app_id":"robosats","variant":"proxy"}}`},
		{name: "image status dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.image.status","dry_run":true,"params":{"app_id":"cpuminer","variant":"baseline"}}`, wantErr: true},
		{name: "valid image probe", payload: `{"version":1,"request_id":"request_1","operation":"app.image.probe","params":{"app_id":"cpuminer","variant":"fast_latest"}}`},
		{name: "robosats image probe not allowed", payload: `{"version":1,"request_id":"request_1","operation":"app.image.probe","params":{"app_id":"robosats","variant":"client"}}`, wantErr: true},
		{name: "unknown image app", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"mempool","variant":"baseline"}}`, wantErr: true},
		{name: "unknown image variant", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"latest"}}`, wantErr: true},
		{name: "image argument injection", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline","image":"evil/root:latest"}}`, wantErr: true},
		{name: "valid robosats firewall ensure", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"robosats"}}`},
		{name: "valid robosats firewall dry run", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","dry_run":true,"params":{"app_id":"robosats"}}`},
		{name: "unknown firewall app", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"cpuminer"}}`, wantErr: true},
		{name: "firewall port injection", payload: `{"version":1,"request_id":"request_1","operation":"app.firewall.ensure","params":{"app_id":"robosats","port":22}}`, wantErr: true},
		{name: "image unit injection", payload: `{"version":1,"request_id":"request_1","operation":"app.image.prepare","params":{"app_id":"cpuminer","variant":"baseline","unit":"ssh"}}`, wantErr: true},
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

func TestBitcoinCoreStorageRequestUsesCanonicalClosedPath(t *testing.T) {
	valid := `{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin"}}`
	if _, err := DecodeRequest(strings.NewReader(valid)); err != nil {
		t.Fatalf("valid storage request rejected: %v", err)
	}
	for _, payload := range []string{
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin/../bitcoin-data"}}`,
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/etc/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_storage_1","operation":"app.bitcoincore.storage.ensure","params":{"data_dir":"/mnt/bitcoin","storage_id":"attacker"}}`,
	} {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("unsafe storage request accepted: %s", payload)
		}
	}
}

func TestBitcoinCoreConfigRequestsAreClosedAndSecretBounded(t *testing.T) {
	valid := []string{
		`{"version":1,"request_id":"bitcoin_config_1","operation":"app.bitcoincore.config.read","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_2","operation":"app.bitcoincore.config.ensure","params":{"data_dir":"/mnt/bitcoin-ssd/bitcoin","content":"server=1\nrpcpassword=secret\n"}}`,
		`{"version":1,"request_id":"bitcoin_config_3","operation":"app.bitcoincore.config.write","dry_run":true,"params":{"data_dir":"/data/bitcoin","content":"server=1\n"}}`,
	}
	for _, payload := range valid {
		if _, err := DecodeRequest(strings.NewReader(payload)); err != nil {
			t.Fatalf("valid bitcoin config request rejected: %v", err)
		}
	}

	invalid := []string{
		`{"version":1,"request_id":"bitcoin_config_4","operation":"app.bitcoincore.config.read","dry_run":true,"params":{"data_dir":"/data/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_5","operation":"app.bitcoincore.config.read","params":{"data_dir":"/etc/bitcoin"}}`,
		`{"version":1,"request_id":"bitcoin_config_6","operation":"app.bitcoincore.config.ensure","params":{"data_dir":"/data/bitcoin","content":"server=1"}}`,
		`{"version":1,"request_id":"bitcoin_config_7","operation":"app.bitcoincore.config.write","params":{"data_dir":"/data/bitcoin","content":"server=1\r\n"}}`,
		`{"version":1,"request_id":"bitcoin_config_8","operation":"app.bitcoincore.config.write","params":{"data_dir":"/data/bitcoin","content":"server=1\n","path":"/etc/passwd"}}`,
	}
	for _, payload := range invalid {
		if _, err := DecodeRequest(strings.NewReader(payload)); err == nil {
			t.Fatalf("unsafe bitcoin config request accepted: %s", payload)
		}
	}
}
