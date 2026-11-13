package server

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
)

type upfrontShutdownSetting struct {
	Mode    string `json:"mode"`
	Address string `json:"address"`
}

// Do not infer LND ownership from the Bitcoin backend or an editable config
// comment. Missing/legacy origins fail closed for this new opt-in control only.
func readLNDInstallationOrigin(configPath string) string {
	if configPath == "" {
		return "unknown"
	}
	path := filepath.Join(filepath.Dir(configPath), "installation-origin")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || !lndOriginOwnedByRoot(info) || info.Mode().Perm()&0022 != 0 || info.Size() > 64 {
		return "unknown"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	switch strings.TrimSpace(string(raw)) {
	case "native-v1":
		return "native"
	case "existing-v1":
		return "existing"
	default:
		return "unknown"
	}
}

func parseUpfrontShutdown(raw string) (upfrontShutdownSetting, error) {
	out := upfrontShutdownSetting{Mode: "off"}
	seen := map[string]bool{}
	for _, entry := range lndConfigEntries(raw) {
		if entry.key != "enable-upfront-shutdown" && entry.key != "upfront-shutdown-address" {
			continue
		}
		if entry.section != "application options" || seen[entry.key] {
			return out, errors.New("ambiguous upfront shutdown configuration; review the advanced editor")
		}
		seen[entry.key] = true
		if entry.key == "upfront-shutdown-address" {
			out.Address = strings.Trim(entry.value, "\"")
		} else {
			enabled, err := strconv.ParseBool(entry.value)
			if err != nil {
				return out, errors.New("invalid upfront shutdown boolean")
			}
			if enabled {
				out.Mode = "wallet"
			}
		}
	}
	if out.Address != "" {
		out.Mode = "external"
	}
	return out, nil
}

func buildUpfrontShutdownConfig(raw string, setting upfrontShutdownSetting, network *chaincfg.Params) (string, error) {
	if _, err := parseUpfrontShutdown(raw); err != nil {
		return "", err
	}
	if setting.Mode != "off" && setting.Mode != "wallet" && setting.Mode != "external" {
		return "", errors.New("invalid cooperative close destination mode")
	}
	address := strings.TrimSpace(setting.Address)
	if setting.Mode != "external" && address != "" {
		return "", errors.New("an address is only accepted in external mode")
	}
	if setting.Mode == "external" {
		if len(address) > 100 || strings.ContainsAny(address, "\r\n\x00") {
			return "", errors.New("invalid Bitcoin address")
		}
		decoded, err := btcutil.DecodeAddress(address, network)
		if err != nil || !decoded.IsForNet(network) {
			return "", errors.New("invalid Bitcoin address for this network")
		}
		switch decoded.(type) {
		case *btcutil.AddressPubKeyHash, *btcutil.AddressScriptHash, *btcutil.AddressWitnessPubKeyHash, *btcutil.AddressWitnessScriptHash, *btcutil.AddressTaproot:
		default:
			return "", errors.New("unsupported cooperative close address type")
		}
		address = decoded.EncodeAddress()
	}
	updated := setOrRemoveLNDOption(raw, "Application Options", "enable-upfront-shutdown", strconv.FormatBool(setting.Mode == "wallet"), setting.Mode != "off")
	updated = setOrRemoveLNDOption(updated, "Application Options", "upfront-shutdown-address", address, setting.Mode == "external")
	return updated, nil
}

func (s *Server) upfrontShutdownOrigin() string {
	if s.cfg == nil {
		return "unknown"
	}
	return readLNDInstallationOrigin(s.cfg.Path)
}

func (s *Server) upfrontShutdownVersion(ctx context.Context) bool {
	if s.lnd == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	info, err := s.lnd.GetStatus(ctx)
	if err != nil {
		return false
	}
	v := strings.Split(strings.TrimPrefix(info.Version, "v"), ".")
	if len(v) < 3 {
		return false
	}
	major, err1 := strconv.Atoi(v[0])
	minor, err2 := strconv.Atoi(v[1])
	return err1 == nil && err2 == nil && (major > 0 || minor >= 21)
}

func (s *Server) handleUpfrontShutdownGet(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	snapshot, err := s.readLNDConfigSnapshot()
	if err != nil {
		writeError(w, 500, "failed to read LND configuration")
		return
	}
	setting, parseErr := parseUpfrontShutdown(string(snapshot.raw))
	origin := s.upfrontShutdownOrigin()
	reason := ""
	if origin != "native" {
		reason = "installation_origin_unverified"
	} else if parseErr != nil {
		reason = "ambiguous_configuration"
	} else if !s.upfrontShutdownVersion(r.Context()) {
		reason = "lnd_version_unavailable_or_unsupported"
	}
	writeJSON(w, 200, map[string]any{"current": setting, "origin": origin, "editable": reason == "", "reason": reason, "config_revision": snapshot.revision})
}

func (s *Server) handleUpfrontShutdownPost(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if s.rejectLNDMaintenanceAction(w, r, "cooperative close destination") {
		return
	}
	var req struct {
		upfrontShutdownSetting
		ConfigRevision  string `json:"config_revision"`
		ConfirmPassword string `json:"confirm_password"`
		ConfirmExternal bool   `json:"confirm_external"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, "invalid json")
		return
	}
	if !s.requireSensitiveReauth(w, r, authScopeLNDConfig, req.ConfirmPassword, "lnd_config_reauth_required", "password confirmation required to change LND configuration") {
		return
	}
	if s.upfrontShutdownOrigin() != "native" {
		writeErrorCode(w, 403, "lnd_origin_unverified", "this control requires a verified native LOS installation")
		return
	}
	if !s.upfrontShutdownVersion(r.Context()) {
		writeErrorCode(w, 409, "lnd_version_unsupported", "a reachable LND 0.21 or newer is required")
		return
	}
	if req.Mode == "external" && !req.ConfirmExternal {
		writeError(w, 400, "confirm ownership, address reuse and peer compatibility before using an external address")
		return
	}
	lndConfigMutationMu.Lock()
	defer lndConfigMutationMu.Unlock()
	ops := s.lndConfigOperations()
	raw, err := ops.read()
	if err != nil {
		writeError(w, 500, "failed to read lnd.conf")
		return
	}
	revision, err := s.lndConfigRevision(raw)
	if err != nil {
		writeError(w, 500, "config revision unavailable")
		return
	}
	if !hmac.Equal([]byte(req.ConfigRevision), []byte(revision)) {
		writeErrorCode(w, 409, "lnd_config_revision_conflict", "configuration changed; reload before saving")
		return
	}
	// Production is mainnet-only. The isolated native test harness validates
	// the same builder/apply path with RegressionNetParams, without unlocking
	// general config writes or production broker access in the regtest panels.
	updated, err := buildUpfrontShutdownConfig(string(raw), req.upfrontShutdownSetting, &chaincfg.MainNetParams)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	result, applyErr := applyLNDConfig(r.Context(), ops, raw, []byte(updated), true)
	s.respondLNDConfigApply(w, r, "upfront_shutdown", result, applyErr)
}
