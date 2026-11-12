package privileged

import (
	"reflect"
	"testing"
)

func TestTorRESTTargetsRequireNativeLNDAndLocalBridge(t *testing.T) {
	sockets := `LISTEN 0 4096 172.17.0.1:8080 0.0.0.0:* users:(("lnd",pid=123,fd=10))
LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("lnd",pid=123,fd=11))
LISTEN 0 4096 192.168.1.20:8080 0.0.0.0:* users:(("lnd",pid=123,fd=12))
LISTEN 0 4096 172.18.0.1:8080 0.0.0.0:* users:(("other",pid=1234,fd=13))
LISTEN 0 4096 8.8.8.8:8080 0.0.0.0:* users:(("lnd",pid=123,fd=14))`
	got := selectTorRESTTargets(sockets, "123", map[string]bool{"172.17.0.1": true, "172.18.0.1": true, "8.8.8.8": true})
	want := []string{"127.0.0.1:8080", "172.17.0.1:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, pid := range []string{"", "0", "123,", "12"} {
		if got := selectTorRESTTargets(sockets, pid, nil); len(got) != 0 {
			t.Fatalf("invalid PID %q: %v", pid, got)
		}
	}
}

func TestTorRESTDockerOnlyAndWildcard(t *testing.T) {
	for _, tc := range []struct {
		bind    string
		bridges map[string]bool
		want    []string
	}{
		{"172.17.0.1:8080", map[string]bool{"172.17.0.1": true}, []string{"172.17.0.1:8080"}},
		{"172.17.0.1:8080", nil, []string{}},
		{"0.0.0.0:8080", nil, []string{"127.0.0.1:8080"}},
		{"[::]:8080", nil, []string{"[::1]:8080"}},
		{"remote.example:8080", nil, []string{}},
		{"127.0.0.1:10009", nil, []string{}},
	} {
		got := selectTorRESTTargets("LISTEN 0 4096 "+tc.bind+" 0.0.0.0:* users:((\"lnd\",pid=123,fd=10))", "123", tc.bridges)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v want %v", tc.bind, got, tc.want)
		}
	}
}

func TestTorRESTValidatedDockerBlockReused(t *testing.T) {
	target := "172.17.0.1:8080"
	config := appendLNDRESTService("SocksPort 9050\n", target)
	if err := validateLNDRESTTorrc(config, target); err != nil {
		t.Fatal(err)
	}
	if dir, found := findLNDRESTService(config, target); !found || dir != torLNDRESTDir {
		t.Fatal("Docker service not reused")
	}
	if _, found := findLNDRESTService(config, "127.0.0.1:8080"); found {
		t.Fatal("unvalidated destination accepted")
	}
	if validateLNDRESTTorrc(config, "127.0.0.1:8080") == nil {
		t.Fatal("conflicting managed target accepted")
	}
}
