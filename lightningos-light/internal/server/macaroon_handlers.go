package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const macaroonBakeTimeout = 15 * time.Second

type macaroonPreset struct {
	ID          string                         `json:"id"`
	Label       string                         `json:"label"`
	Permissions []lndclient.MacaroonPermission `json:"permissions"`
}

type macaroonBakeRequest struct {
	Preset                   string                         `json:"preset"`
	Permissions              []lndclient.MacaroonPermission `json:"permissions"`
	ConfirmPassword          string                         `json:"confirm_password"`
	RootKeyID                uint64                         `json:"root_key_id,omitempty"`
	AllowExternalPermissions bool                           `json:"allow_external_permissions,omitempty"`
}

func (s *Server) handleMacaroonOptions(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND client unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	permissions, err := s.lnd.ListMacaroonPermissions(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, lndRPCErrorMessage(err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"presets":     buildMacaroonPresets(permissions),
		"permissions": permissions,
	})
}

func (s *Server) handleMacaroonBake(w http.ResponseWriter, r *http.Request) {
	if s.lnd == nil {
		writeError(w, http.StatusServiceUnavailable, "LND client unavailable")
		return
	}

	var req macaroonBakeRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), macaroonBakeTimeout)
	defer cancel()

	available, err := s.lnd.ListMacaroonPermissions(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, lndRPCErrorMessage(err))
		return
	}

	permissions, presetID, err := resolveMacaroonBakePermissions(req, available)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.AllowExternalPermissions {
		if err := validateMacaroonPermissionsAvailable(permissions, available); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if !s.requireMacaroonExportReauth(w, r, req.ConfirmPassword) {
		return
	}

	rootKeyIDs, err := s.lnd.ListMacaroonIDs(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, lndRPCErrorMessage(err))
		return
	}

	now := time.Now().UTC()
	rootKeyID := req.RootKeyID
	if rootKeyID == 0 {
		rootKeyID, err = lndclient.GenerateMacaroonRootKeyID(rootKeyIDs, now)
	} else {
		err = lndclient.ValidateMacaroonRootKeyID(rootKeyID, rootKeyIDs)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.lnd.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions:              permissions,
		RootKeyID:                rootKeyID,
		AllowExternalPermissions: req.AllowExternalPermissions,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, lndRPCErrorMessage(err))
		return
	}

	fileName := lndclient.CustomMacaroonFileName(rootKeyID, now)
	permissionStrings := lndclient.MacaroonPermissionStrings(result.Permissions)
	s.recordAuditEvent(r, "macaroon.bake", fileName, macaroonBakeAuditMetadata(rootKeyID, permissionStrings, presetID, req.AllowExternalPermissions))

	writeJSON(w, http.StatusOK, map[string]any{
		"file_name":       fileName,
		"root_key_id":     rootKeyID,
		"macaroon_hex":    result.MacaroonHex,
		"macaroon_base64": result.MacaroonBase64,
		"permissions":     permissionStrings,
	})
}

func macaroonBakeAuditMetadata(rootKeyID uint64, permissions []string, presetID string, allowExternalPermissions bool) map[string]any {
	return map[string]any{
		"root_key_id":                rootKeyID,
		"permission_count":           len(permissions),
		"permissions":                permissions,
		"preset":                     presetID,
		"allow_external_permissions": allowExternalPermissions,
		"status":                     "success",
	}
}

func (s *Server) requireMacaroonExportReauth(w http.ResponseWriter, r *http.Request, confirmPassword string) bool {
	if s.auth == nil || !s.auth.Enabled() {
		return true
	}

	session, ok := authSessionFromContext(r.Context())
	if !ok {
		writeErrorCode(w, http.StatusUnauthorized, "auth_required", "authentication required")
		return false
	}
	if s.auth.HasRecentReauth(session.ID, authScopeMacaroonExport) {
		return true
	}

	confirmPassword = strings.TrimSpace(confirmPassword)
	if confirmPassword == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{
			"error":                          "password confirmation required for macaroon export",
			"code":                           "macaroon_export_reauth_required",
			"requires_password_confirmation": true,
		})
		return false
	}
	if _, err := s.auth.reauth(session.ID, confirmPassword, authScopeMacaroonExport); err != nil {
		writeErrorCode(w, http.StatusUnauthorized, "auth_invalid_credentials", "invalid credentials")
		return false
	}
	return true
}

func buildMacaroonPresets(available []lndclient.MacaroonPermission) []macaroonPreset {
	presets := make([]macaroonPreset, 0, 3)
	invoicePermissions := filterAvailableMacaroonPermissions(available, []lndclient.MacaroonPermission{
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "invoice", Action: "read"},
		{Entity: "invoice", Action: "write"},
	})
	if len(invoicePermissions) > 0 {
		presets = append(presets, macaroonPreset{
			ID:          "invoice_permissions",
			Label:       "Invoice permissions",
			Permissions: invoicePermissions,
		})
	}

	readOnlyPermissions := make([]lndclient.MacaroonPermission, 0)
	for _, permission := range available {
		if permission.Action == "read" {
			readOnlyPermissions = append(readOnlyPermissions, permission)
		}
	}
	if len(readOnlyPermissions) > 0 {
		presets = append(presets, macaroonPreset{
			ID:          "read_only",
			Label:       "Read-only permissions",
			Permissions: readOnlyPermissions,
		})
	}

	presets = append(presets, macaroonPreset{
		ID:          "custom",
		Label:       "Custom",
		Permissions: nil,
	})
	return presets
}

func resolveMacaroonBakePermissions(req macaroonBakeRequest, available []lndclient.MacaroonPermission) ([]lndclient.MacaroonPermission, string, error) {
	presetID := strings.TrimSpace(req.Preset)
	if presetID == "" {
		presetID = "custom"
	}
	if presetID == "custom" {
		permissions, err := lndclient.NormalizeMacaroonPermissions(req.Permissions)
		return permissions, presetID, err
	}
	for _, preset := range buildMacaroonPresets(available) {
		if preset.ID != presetID {
			continue
		}
		if len(preset.Permissions) == 0 {
			permissions, err := lndclient.NormalizeMacaroonPermissions(req.Permissions)
			return permissions, presetID, err
		}
		return preset.Permissions, presetID, nil
	}
	return nil, presetID, errors.New("unknown macaroon preset")
}

func validateMacaroonPermissionsAvailable(permissions []lndclient.MacaroonPermission, available []lndclient.MacaroonPermission) error {
	known := make(map[string]struct{}, len(available))
	for _, permission := range available {
		known[lndclient.MacaroonPermissionKey(permission)] = struct{}{}
	}
	for _, permission := range permissions {
		if _, ok := known[lndclient.MacaroonPermissionKey(permission)]; ok {
			continue
		}
		return errors.New("permission not available from LND: " + lndclient.MacaroonPermissionKey(permission))
	}
	return nil
}

func filterAvailableMacaroonPermissions(available []lndclient.MacaroonPermission, candidates []lndclient.MacaroonPermission) []lndclient.MacaroonPermission {
	known := make(map[string]lndclient.MacaroonPermission, len(available))
	for _, permission := range available {
		known[lndclient.MacaroonPermissionKey(permission)] = permission
	}
	filtered := make([]lndclient.MacaroonPermission, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		permission, err := lndclient.NormalizeMacaroonPermission(candidate)
		if err != nil {
			continue
		}
		key := lndclient.MacaroonPermissionKey(permission)
		if _, ok := seen[key]; ok {
			continue
		}
		match, ok := known[key]
		if !ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, match)
	}
	return filtered
}
