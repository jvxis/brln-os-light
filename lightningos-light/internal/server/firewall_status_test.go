package server

import "testing"

func TestParseManagerFirewallStatus(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		configured string
		active     bool
		bound      bool
		broad      bool
	}{
		{
			name:       "inactive",
			output:     "Status: inactive\n",
			configured: "192.168.68.0/22",
		},
		{
			name:       "restricted lan",
			output:     "Status: active\n8443/tcp ALLOW IN 192.168.68.0/22\n",
			configured: "192.168.68.0/22",
			active:     true,
			bound:      true,
		},
		{
			name:       "broad rule overrides lan",
			output:     "Status: active\n8443/tcp ALLOW IN 192.168.68.0/22\n8443/tcp ALLOW IN Anywhere\n",
			configured: "192.168.68.0/22",
			active:     true,
			broad:      true,
		},
		{
			name:       "vpn mode without interface rule is not protected",
			output:     "Status: active\n",
			configured: "none",
			active:     true,
		},
		{
			name:       "tailscale only is not broad",
			output:     "Status: active\n8443/tcp on tailscale0 ALLOW IN Anywhere\n",
			configured: "none",
			active:     true,
			bound:      true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := parseManagerFirewallStatus(tc.output, managerFirewallStatus{
				ConfiguredCIDR:  tc.configured,
				ConfigValid:     true,
				Installed:       true,
				StatusAvailable: true,
			})
			if status.Active != tc.active || status.ManagerAccessBound != tc.bound || status.BroadRulePresent != tc.broad {
				t.Fatalf("unexpected status: %+v", status)
			}
		})
	}
}

func TestManagerExposureClientNetworks(t *testing.T) {
	if ip := managerExposureClientIP("192.168.68.10:55123"); ip == nil || ip.String() != "192.168.68.10" {
		t.Fatalf("unexpected LAN client IP: %v", ip)
	}
	for _, value := range []string{"100.100.20.30", "fd7a:115c:a1e0::1234"} {
		if !managerExposureVPNIP(managerExposureClientIP(value)) {
			t.Fatalf("Tailscale address rejected: %s", value)
		}
	}
	if managerExposureVPNIP(managerExposureClientIP("192.168.68.10")) {
		t.Fatal("LAN address accepted as Tailscale")
	}
}

func TestManagerHostLoopback(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "[::1]", "localhost"} {
		if !managerHostLoopback(host) {
			t.Fatalf("loopback host rejected: %s", host)
		}
	}
	for _, host := range []string{"0.0.0.0", "192.168.68.92", ""} {
		if managerHostLoopback(host) {
			t.Fatalf("non-loopback host accepted: %q", host)
		}
	}
}
