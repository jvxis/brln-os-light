// Package networkpolicy holds the Manager's shared network trust boundary.
package networkpolicy

import (
	"net/netip"
	"strings"
)

// PrivateLAN requires the entire IPv4 network, not only its first address, to
// fit inside RFC1918. CGNAT/Tailscale addresses are not a physical LAN.
func PrivateLAN(cidr string) bool {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() {
		return false
	}
	for _, block := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		private := netip.MustParsePrefix(block)
		if prefix.Bits() >= private.Bits() && private.Contains(prefix.Masked().Addr()) {
			return true
		}
	}
	return false
}

// PublicManagerAllow inspects an English `ufw status` ALLOW line for 8443.
// Interface-bound Tailscale rules and private app-bridge sources remain valid.
// Unknown/non-private sources fail closed rather than claiming protection.
func PublicManagerAllow(line string) bool {
	line, _, _ = strings.Cut(line, "#")
	line = strings.ToLower(line)
	if !strings.Contains(line, "8443") || !strings.Contains(line, "allow") {
		return false
	}
	fields := strings.Fields(line)
	for i, field := range fields {
		if field == "on" && i+1 < len(fields) && fields[i+1] == "tailscale0" {
			return false
		}
	}
	for i, field := range fields {
		if field != "allow" {
			continue
		}
		i++
		if i < len(fields) && fields[i] == "in" {
			i++
		}
		if i >= len(fields) {
			return true
		}
		source := fields[i]
		if addr, err := netip.ParseAddr(source); err == nil && addr.Is4() {
			source += "/32"
		}
		return !PrivateLAN(source)
	}
	return true
}
