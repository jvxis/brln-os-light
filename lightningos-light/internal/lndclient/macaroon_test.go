package lndclient

import (
	"testing"
	"time"
)

func TestNormalizeMacaroonPermissionsDeduplicates(t *testing.T) {
	got, err := NormalizeMacaroonPermissions([]MacaroonPermission{
		{Entity: " Invoices ", Action: " Read "},
		{Entity: "invoices", Action: "read"},
		{Entity: "invoices", Action: "write"},
	})
	if err != nil {
		t.Fatalf("NormalizeMacaroonPermissions returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 permissions after dedupe, got %d", len(got))
	}
	if got[0].Entity != "invoices" || got[0].Action != "read" {
		t.Fatalf("unexpected first permission: %#v", got[0])
	}
	if got[1].Entity != "invoices" || got[1].Action != "write" {
		t.Fatalf("unexpected second permission: %#v", got[1])
	}
}

func TestNormalizeMacaroonPermissionsRejectsInvalid(t *testing.T) {
	_, err := NormalizeMacaroonPermissions([]MacaroonPermission{
		{Entity: "invoice", Action: "read:write"},
	})
	if err == nil {
		t.Fatal("expected invalid permission to fail")
	}
}

func TestMacaroonHexToBase64(t *testing.T) {
	got, err := MacaroonHexToBase64("000102ff")
	if err != nil {
		t.Fatalf("MacaroonHexToBase64 returned error: %v", err)
	}
	if got != "AAEC/w==" {
		t.Fatalf("unexpected base64 value %q", got)
	}
}

func TestGenerateMacaroonRootKeyIDAvoidsCollisions(t *testing.T) {
	now := time.Unix(0, 1750776886123*int64(time.Millisecond)).UTC()
	got, err := GenerateMacaroonRootKeyID([]uint64{1750776886123, 1750776886124}, now)
	if err != nil {
		t.Fatalf("GenerateMacaroonRootKeyID returned error: %v", err)
	}
	if got != 1750776886125 {
		t.Fatalf("expected collision-safe ID 1750776886125, got %d", got)
	}
}

func TestCustomMacaroonFileName(t *testing.T) {
	at := time.Date(2026, 6, 24, 14, 54, 46, 0, time.UTC)
	got := CustomMacaroonFileName(1750776886123, at)
	want := "los-custom-macaroon-20260624T145446Z-rk1750776886123.macaroon"
	if got != want {
		t.Fatalf("CustomMacaroonFileName = %q, want %q", got, want)
	}
}
