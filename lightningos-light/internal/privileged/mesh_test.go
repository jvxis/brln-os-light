package privileged

import (
	"strings"
	"testing"
)

func TestMeshClosedParameters(t *testing.T) {
	for _, p := range []MeshParams{{Action: "shell"}, {Action: "start", Device: "/dev/ttyACM0"}, {Action: "install", Device: "/dev/serial/by-id/../../sda"}, {Action: "install", Device: "/dev/serial/by-id/radio\nExecStart=/bin/sh"}, {Action: "install", Device: "/dev/ttyACM0"}} {
		if validateMeshParams(p) == nil {
			t.Fatal("accepted unsafe params", p)
		}
	}
	if validateMeshParams(MeshParams{Action: "install", Device: "/dev/serial/by-id/usb-T-Deck_123-if00"}) != nil {
		t.Fatal("valid stable device rejected")
	}
}
func TestMeshIsolation(t *testing.T) {
	unit := meshServiceUnit("/dev/ttyACM0")
	for _, required := range []string{"User=losmesh", "NoNewPrivileges=yes", "DevicePolicy=closed", "DeviceAllow=/dev/ttyACM0 rw", "RestrictAddressFamilies=AF_UNIX", "ProtectSystem=strict", "CapabilityBoundingSet=", "MemoryMax=64M"} {
		if !strings.Contains(unit, required) {
			t.Fatal("missing sandbox control", required)
		}
	}
	for _, forbidden := range []string{"dialout", "SupplementaryGroups=lightningos", "AF_INET", "EnvironmentFile=", "/admin.macaroon", "/bin/sh"} {
		if strings.Contains(unit, forbidden) {
			t.Fatal("excess privilege", forbidden)
		}
	}
}

func TestMeshTCPIsolation(t *testing.T) {
	target := "tcp://192.168.1.50:4403"
	if err := validateMeshParams(MeshParams{Action: "install", Device: target}); err != nil {
		t.Fatal(err)
	}
	unit := meshServiceUnit(target)
	for _, required := range []string{"IPAddressDeny=any", "IPAddressAllow=192.168.1.50", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "DevicePolicy=closed", "User=losmesh", "ProtectSystem=strict"} {
		if !strings.Contains(unit, required) {
			t.Fatal("missing restriction", required)
		}
	}
	if strings.Contains(unit, "DeviceAllow=") {
		t.Fatal("TCP must not grant serial access")
	}
	if meshServiceUnit("tcp://1.1.1.1:4403") != "" {
		t.Fatal("unsafe unit rendered")
	}
}
