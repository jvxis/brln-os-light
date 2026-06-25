package server

import (
	"encoding/json"
	"strings"
	"testing"

	"lightningos-light/internal/lndclient"
)

func TestBuildMacaroonPresetsUsesAvailableInvoicePermissions(t *testing.T) {
	available := []lndclient.MacaroonPermission{
		{Entity: "info", Action: "read"},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "onchain", Action: "write"},
	}

	presets := buildMacaroonPresets(available)
	var invoice *macaroonPreset
	for i := range presets {
		if presets[i].ID == "invoice_permissions" {
			invoice = &presets[i]
			break
		}
	}
	if invoice == nil {
		t.Fatal("expected invoice_permissions preset")
	}
	if len(invoice.Permissions) != 2 {
		t.Fatalf("expected 2 invoice permissions, got %d", len(invoice.Permissions))
	}
	if lndclient.MacaroonPermissionKey(invoice.Permissions[0]) != "invoices:read" {
		t.Fatalf("unexpected first invoice permission: %#v", invoice.Permissions[0])
	}
	if lndclient.MacaroonPermissionKey(invoice.Permissions[1]) != "invoices:write" {
		t.Fatalf("unexpected second invoice permission: %#v", invoice.Permissions[1])
	}
}

func TestResolveMacaroonBakePermissionsPresetOverridesPayload(t *testing.T) {
	available := []lndclient.MacaroonPermission{
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
		{Entity: "info", Action: "read"},
	}

	permissions, presetID, err := resolveMacaroonBakePermissions(macaroonBakeRequest{
		Preset: "invoice_permissions",
		Permissions: []lndclient.MacaroonPermission{
			{Entity: "info", Action: "read"},
		},
	}, available)
	if err != nil {
		t.Fatalf("resolveMacaroonBakePermissions returned error: %v", err)
	}
	if presetID != "invoice_permissions" {
		t.Fatalf("unexpected preset ID %q", presetID)
	}
	got := lndclient.MacaroonPermissionStrings(permissions)
	if len(got) != 2 || got[0] != "invoices:read" || got[1] != "invoices:write" {
		t.Fatalf("preset permissions were not used: %#v", got)
	}
}

func TestValidateMacaroonPermissionsAvailableRejectsUnknown(t *testing.T) {
	err := validateMacaroonPermissionsAvailable(
		[]lndclient.MacaroonPermission{{Entity: "external", Action: "read"}},
		[]lndclient.MacaroonPermission{{Entity: "info", Action: "read"}},
	)
	if err == nil {
		t.Fatal("expected unknown permission to be rejected")
	}
}

func TestMacaroonBakeAuditMetadataDoesNotContainSecrets(t *testing.T) {
	metadata := macaroonBakeAuditMetadata(1750776886123, []string{"invoices:read"}, "invoice_permissions", false)
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{"macaroon_hex", "macaroon_base64", "secret-value"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("audit metadata contains forbidden value %q: %s", forbidden, serialized)
		}
	}
}
