package appmanifest

import "testing"

func TestValidateCPUMinerEnv(t *testing.T) {
	valid := "CPUMINER_IMAGE=jvx1971/cpu-lottery-miner:v1\n" +
		"POOL_MODE=brln\n" +
		"STRATUM_HOST=btcpool.br-ln.com\n" +
		"STRATUM_PORT=3332\n" +
		"MINING_ADDRESS=bc1qexampleaddress000000000000000000000000\n" +
		"WORKER_NAME=cpu-lottery\n" +
		"THREADS=1\n"
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "valid BR-LN", raw: valid},
		{name: "valid local", raw: "CPUMINER_IMAGE=cniweb/cpuminer-opt:latest\nPOOL_MODE=local\nSTRATUM_HOST=host.docker.internal\nSTRATUM_PORT=3333\nMINING_ADDRESS=1ExampleAddress00000000000000000000\nWORKER_NAME=worker_1\nTHREADS=2\n"},
		{name: "unknown key", raw: valid + "COMPOSE_FILE=/tmp/evil.yaml\n", wantErr: true},
		{name: "duplicate key", raw: valid + "THREADS=2\n", wantErr: true},
		{name: "image injection", raw: replaceEnvValue(valid, "CPUMINER_IMAGE", "evil/image:latest"), wantErr: true},
		{name: "pool mismatch", raw: replaceEnvValue(valid, "STRATUM_HOST", "attacker.example"), wantErr: true},
		{name: "worker interpolation", raw: replaceEnvValue(valid, "WORKER_NAME", "${PWD}"), wantErr: true},
		{name: "invalid threads", raw: replaceEnvValue(valid, "THREADS", "01"), wantErr: true},
		{name: "comment rejected", raw: "# source /etc/shadow\n" + valid, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateCPUMinerEnv([]byte(test.raw))
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateCPUMinerEnv() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func replaceEnvValue(raw string, key string, value string) string {
	oldStart := key + "="
	start := 0
	for start < len(raw) {
		end := start
		for end < len(raw) && raw[end] != '\n' {
			end++
		}
		if len(raw[start:end]) >= len(oldStart) && raw[start:start+len(oldStart)] == oldStart {
			return raw[:start] + oldStart + value + raw[end:]
		}
		start = end + 1
	}
	return raw
}
