package networkpolicy

import "testing"

func TestPrivateLANContainsEntireNetwork(t *testing.T) {
	for _, cidr := range []string{"10.0.0.0/8", "10.2.3.4/32", "172.16.0.0/12", "172.31.255.0/24", "192.168.0.0/16", "192.168.68.0/24"} {
		if !PrivateLAN(cidr) {
			t.Errorf("private LAN rejected: %s", cidr)
		}
	}
	for _, cidr := range []string{"203.0.112.0/20", "10.0.0.0/7", "172.16.0.0/11", "192.168.0.0/15", "100.64.0.0/10", "127.0.0.1/32", "169.254.0.0/16", "0.0.0.0/0", "::/0", "fd00::/8", "::ffff:192.168.1.0/120", "invalid"} {
		if PrivateLAN(cidr) {
			t.Errorf("non-LAN accepted: %s", cidr)
		}
	}
}

func TestPublicManagerAllow(t *testing.T) {
	for _, line := range []string{
		"8443/tcp ALLOW IN 203.0.112.0/20 # LightningOS LAN",
		"8443/tcp ALLOW IN 203.0.113.10",
		"8443/tcp ALLOW IN 0.0.0.0/0",
		"8443/tcp ALLOW IN 10.0.0.0/7",
		"8443/tcp (v6) ALLOW IN Anywhere (v6)",
		"8443/tcp ALLOW IN 2001:db8::/32",
		"8443/tcp ALLOW IN Anywhere # on tailscale0",
	} {
		if !PublicManagerAllow(line) {
			t.Errorf("public ALLOW not detected: %s", line)
		}
	}
	for _, line := range []string{
		"8443/tcp ALLOW IN 192.168.68.0/24",
		"8443/tcp ALLOW IN 10.2.3.4",
		"8443/tcp on br-0123456789ab ALLOW IN 172.20.0.0/16",
		"8443/tcp on tailscale0 ALLOW IN Anywhere",
		"8443/tcp (v6) on tailscale0 ALLOW IN Anywhere (v6)",
		"8443/tcp on enp0s3 DENY IN Anywhere",
		"22/tcp ALLOW IN Anywhere",
	} {
		if PublicManagerAllow(line) {
			t.Errorf("safe/unrelated rule marked public: %s", line)
		}
	}
}
