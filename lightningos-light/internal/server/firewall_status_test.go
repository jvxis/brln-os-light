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
