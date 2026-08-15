package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"

	"lightningos-light/internal/system"
)

const managerFirewallConfigPath = "/etc/lightningos/manager-firewall.conf"

type managerFirewallStatus struct {
	Installed          bool   `json:"installed"`
	Active             bool   `json:"active"`
	ConfiguredCIDR     string `json:"configured_cidr"`
	ConfigValid        bool   `json:"config_valid"`
	LANRulePresent     bool   `json:"lan_rule_present"`
	TailscaleRule      bool   `json:"tailscale_rule"`
	BroadRulePresent   bool   `json:"broad_rule_present"`
	ManagerAccessBound bool   `json:"manager_access_bound"`
	StatusAvailable    bool   `json:"status_available"`
	AccessMode         string `json:"access_mode"`
	ProtectionState    string `json:"protection_state"`
	Acknowledged       bool   `json:"acknowledged_unprotected"`
}

type managerExposureRequest struct {
	Mode                   string `json:"mode"`
	LANCIDR                string `json:"lan_cidr"`
	AcknowledgeUnprotected bool   `json:"acknowledge_unprotected"`
}

func (s *Server) handleManagerExposureGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.managerExposureStatus(r.Context()))
}

func (s *Server) managerExposureStatus(ctx context.Context) managerFirewallStatus {
	status := inspectManagerFirewall(ctx)
	if s != nil && s.cfg != nil {
		if managerHostLoopback(s.cfg.Server.Host) {
			status.AccessMode = "localhost"
			status.ProtectionState = "localhost_only"
			status.ManagerAccessBound = true
		}
	}
	return status
}

func managerHostLoopback(value string) bool {
	host := strings.Trim(strings.TrimSpace(value), "[]")
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func (s *Server) handleManagerExposurePost(w http.ResponseWriter, r *http.Request) {
	var req managerExposureRequest
	if err := readJSON(r, &req); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "manager_exposure_invalid", "invalid Manager exposure request")
		return
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	req.LANCIDR = strings.TrimSpace(req.LANCIDR)
	clientIP := managerExposureClientIP(r.RemoteAddr)
	switch req.Mode {
	case "lan":
		ip, network, err := net.ParseCIDR(req.LANCIDR)
		if err != nil || ip.To4() == nil {
			writeErrorCode(w, http.StatusBadRequest, "manager_exposure_invalid_cidr", "LAN mode requires a valid IPv4 CIDR")
			return
		}
		if clientIP != nil && !clientIP.IsLoopback() && !network.Contains(clientIP) {
			writeErrorCode(w, http.StatusConflict, "manager_exposure_lockout_risk", "the current client is outside the requested LAN CIDR")
			return
		}
		if req.AcknowledgeUnprotected {
			writeErrorCode(w, http.StatusBadRequest, "manager_exposure_invalid", "LAN mode does not accept an unprotected acknowledgement")
			return
		}
	case "vpn":
		if req.LANCIDR != "" || req.AcknowledgeUnprotected {
			writeErrorCode(w, http.StatusBadRequest, "manager_exposure_invalid", "VPN-only mode does not accept LAN or acknowledgement fields")
			return
		}
		if clientIP != nil && !clientIP.IsLoopback() && !managerExposureVPNIP(clientIP) {
			writeErrorCode(w, http.StatusConflict, "manager_exposure_lockout_risk", "switch to VPN-only mode from a Tailscale connection")
			return
		}
	case "unprotected":
		if req.LANCIDR != "" || !req.AcknowledgeUnprotected {
			writeErrorCode(w, http.StatusBadRequest, "manager_exposure_ack_required", "unprotected mode requires explicit acknowledgement")
			return
		}
	default:
		writeErrorCode(w, http.StatusBadRequest, "manager_exposure_invalid_mode", "unsupported Manager exposure mode")
		return
	}

	raw, handled, err := system.ConfigureManagerFirewallWithBroker(r.Context(), req.Mode, req.LANCIDR, req.AcknowledgeUnprotected)
	if !handled {
		writeErrorCode(w, http.StatusConflict, "manager_exposure_broker_required", "Manager exposure changes require the privileged broker")
		return
	}
	if err != nil {
		writeErrorCode(w, http.StatusConflict, "manager_exposure_apply_failed", err.Error())
		return
	}
	var status managerFirewallStatus
	if err := json.Unmarshal([]byte(raw), &status); err != nil {
		writeErrorCode(w, http.StatusInternalServerError, "manager_exposure_status_invalid", "Manager exposure status could not be verified")
		return
	}
	if s != nil && s.cfg != nil && managerHostLoopback(s.cfg.Server.Host) {
		status.AccessMode = "localhost"
		status.ProtectionState = "localhost_only"
		status.ManagerAccessBound = true
	}
	writeJSON(w, http.StatusOK, status)
}

func managerExposureClientIP(remoteAddr string) net.IP {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func managerExposureVPNIP(ip net.IP) bool {
	for _, cidr := range []string{"100.64.0.0/10", "fd7a:115c:a1e0::/48"} {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func inspectManagerFirewall(ctx context.Context) managerFirewallStatus {
	status := managerFirewallStatus{
		ConfiguredCIDR: readManagerFirewallCIDR(managerFirewallConfigPath),
		ConfigValid:    true,
	}
	if status.ConfiguredCIDR == "" {
		status.ConfigValid = false
	} else if !strings.EqualFold(status.ConfiguredCIDR, "none") {
		if _, _, err := net.ParseCIDR(status.ConfiguredCIDR); err != nil {
			status.ConfigValid = false
		}
	}
	raw, handled, err := system.ReadManagerFirewallStatusWithBroker(ctx)
	if !handled || err != nil {
		return status
	}
	var brokerStatus managerFirewallStatus
	if err := json.Unmarshal([]byte(raw), &brokerStatus); err != nil {
		return status
	}
	return brokerStatus
}

func readManagerFirewallCIDR(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "LAN_CIDR" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseManagerFirewallStatus(output string, status managerFirewallStatus) managerFirewallStatus {
	configuredLower := strings.ToLower(strings.TrimSpace(status.ConfiguredCIDR))
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.ToLower(strings.TrimSpace(rawLine))
		if strings.HasPrefix(line, "status:") {
			status.Active = strings.Contains(line, "active") && !strings.Contains(line, "inactive")
		}
		if !strings.Contains(line, "8443") || !strings.Contains(line, "allow") {
			continue
		}
		if configuredLower != "" && configuredLower != "none" && strings.Contains(line, configuredLower) {
			status.LANRulePresent = true
		}
		if strings.Contains(line, "tailscale0") {
			status.TailscaleRule = true
		}
		if strings.Contains(line, "anywhere") && !strings.Contains(line, "tailscale0") {
			status.BroadRulePresent = true
		}
	}
	if status.Active && status.ConfigValid && !status.BroadRulePresent {
		if configuredLower == "none" {
			status.ManagerAccessBound = status.TailscaleRule
		} else {
			status.ManagerAccessBound = status.LANRulePresent && (status.AccessMode != "lan" || !status.TailscaleRule)
		}
	}
	return status
}
