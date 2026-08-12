package server

import (
	"context"
	"testing"
)

type stubApp struct {
	def appDefinition
}

func (s stubApp) Definition() appDefinition                 { return s.def }
func (s stubApp) Info(ctx context.Context) (appInfo, error) { return newAppInfo(s.def), nil }
func (s stubApp) Install(ctx context.Context) error         { return nil }
func (s stubApp) Uninstall(ctx context.Context) error       { return nil }
func (s stubApp) Start(ctx context.Context) error           { return nil }
func (s stubApp) Stop(ctx context.Context) error            { return nil }

func TestValidateAppRegistry(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		apps := []appHandler{
			stubApp{def: appDefinition{ID: "a", Name: "App A", Port: 8889}},
			stubApp{def: appDefinition{ID: "b", Name: "App B", Port: 8890}},
			stubApp{def: appDefinition{ID: "c", Name: "App C", Port: 0}},
		}
		if err := validateAppRegistry(apps); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		apps := []appHandler{
			stubApp{def: appDefinition{ID: "a", Name: "App A", Port: 8889}},
			stubApp{def: appDefinition{ID: "a", Name: "App B", Port: 8890}},
		}
		if err := validateAppRegistry(apps); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("duplicate port", func(t *testing.T) {
		apps := []appHandler{
			stubApp{def: appDefinition{ID: "a", Name: "App A", Port: 8889}},
			stubApp{def: appDefinition{ID: "b", Name: "App B", Port: 8889}},
		}
		if err := validateAppRegistry(apps); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("unknown security notice", func(t *testing.T) {
		apps := []appHandler{
			stubApp{def: appDefinition{
				ID: "a", Name: "App A", Port: 8889,
				SecurityNotices: []string{"unknown_notice"},
			}},
		}
		if err := validateAppRegistry(apps); err == nil {
			t.Fatalf("expected error")
		}
	})

	t.Run("LND data notice requires elevated access", func(t *testing.T) {
		apps := []appHandler{
			stubApp{def: appDefinition{
				ID: "a", Name: "App A", Port: 8889,
				SecurityNotices: []string{appSecurityNoticeLNDDataDirectoryRead},
			}},
		}
		if err := validateAppRegistry(apps); err == nil {
			t.Fatalf("expected error")
		}
	})
}

func TestValidateAppRegistryConfiguredApps(t *testing.T) {
	apps, err := (&Server{}).appRegistry()
	if err != nil {
		t.Fatalf("configured app registry is invalid: %v", err)
	}
	if len(apps) == 0 {
		t.Fatalf("configured app registry is empty")
	}
}

func TestConfiguredAppSecurityNotices(t *testing.T) {
	apps, err := (&Server{}).appRegistry()
	if err != nil {
		t.Fatalf("configured app registry is invalid: %v", err)
	}

	wantElevated := map[string]bool{
		btcpayAppID:          true,
		"lndg":               true,
		"lnbits":             true,
		loopAppID:            true,
		fedimintGatewayAppID: true,
		peerswapAppID:        true,
		tapdAppID:            true,
	}
	for _, app := range apps {
		def := app.Definition()
		notices := map[string]bool{}
		for _, notice := range def.SecurityNotices {
			notices[notice] = true
		}
		if notices[appSecurityNoticeElevatedLNDAccess] != wantElevated[def.ID] {
			t.Fatalf("unexpected elevated LND notice for %s: %v", def.ID, def.SecurityNotices)
		}
		if notices[appSecurityNoticeLNDDataDirectoryRead] != (def.ID == "lndg") {
			t.Fatalf("unexpected LND data directory notice for %s: %v", def.ID, def.SecurityNotices)
		}

		info := newAppInfo(def)
		if len(info.SecurityNotices) != len(def.SecurityNotices) {
			t.Fatalf("security notices were not copied to app info for %s", def.ID)
		}
	}
}

func TestIsAppHiddenFromStore(t *testing.T) {
	if !isAppHiddenFromStore(depixBuyAppID) {
		t.Fatalf("expected %s to be hidden from store", depixBuyAppID)
	}
	if isAppHiddenFromStore(bitcoinCoreAppID) {
		t.Fatalf("expected %s to remain visible in store", bitcoinCoreAppID)
	}
}
