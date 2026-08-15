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

func TestConfiguredAppsHaveExplicitRuntimeSecurityClass(t *testing.T) {
	type runtimePolicy struct {
		class  string
		reason string
	}
	policies := map[string]runtimePolicy{
		"bitcoincore":       {class: "compose", reason: "closed broker-owned Compose manifest"},
		"bark-wallet":       {class: "compose", reason: "closed broker-owned Compose manifest"},
		"electrs":           {class: "compose", reason: "closed broker-owned Compose manifest"},
		"mempool":           {class: "compose", reason: "closed broker-owned Compose manifest"},
		"fedimint-guardian": {class: "compose", reason: "closed broker-owned Compose manifest"},
		"fedimint-gateway":  {class: "compose", reason: "closed broker-owned Compose manifest"},
		"lndg":              {class: "compose", reason: "closed broker-owned Compose manifest"},
		"lnbits":            {class: "compose", reason: "closed broker-owned Compose manifest"},
		"btcpay":            {class: "compose", reason: "closed broker-owned Compose manifest"},
		"robosats":          {class: "compose", reason: "closed broker-owned Compose manifest"},
		"publicpool":        {class: "compose", reason: "closed broker-owned Compose manifest"},
		"cpuminer":          {class: "compose", reason: "closed broker-owned Compose manifest"},
		"tapd":              {class: "compose", reason: "closed broker-owned Compose manifest"},

		"elements": {class: "native", reason: "fixed privileged-broker systemd lifecycle and selectable managed storage"},
		"peerswap": {class: "native", reason: "fixed privileged-broker systemd lifecycle with local or external Elements compatibility"},
		"loop":     {class: "native", reason: "fixed privileged-broker systemd lifecycle and dedicated LND credential"},

		"depixbuy":     {class: "manager", reason: "manager-integrated feature without a separately privileged runtime"},
		"fswap":        {class: "manager", reason: "manager-integrated feature without a separately privileged runtime"},
		"loopout-brln": {class: "manager", reason: "native LightningOS workflow executed through typed manager services"},
		"magma-sales":  {class: "manager", reason: "native LightningOS workflow executed through typed manager services"},
	}

	apps, err := (&Server{}).appRegistry()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, app := range apps {
		id := app.Definition().ID
		policy, ok := policies[id]
		if !ok {
			t.Fatalf("app %s has no runtime security classification", id)
		}
		if policy.class == "" || policy.reason == "" {
			t.Fatalf("app %s has an incomplete runtime security policy: %#v", id, policy)
		}
		seen[id] = true
	}
	for id := range policies {
		if !seen[id] {
			t.Fatalf("stale runtime security policy for unregistered app %s", id)
		}
	}
}

func TestConfiguredAppSecurityNotices(t *testing.T) {
	apps, err := (&Server{}).appRegistry()
	if err != nil {
		t.Fatalf("configured app registry is invalid: %v", err)
	}

	wantElevated := map[string]bool{
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
		if notices[appSecurityNoticeLimitedLNDAccess] != (def.ID == btcpayAppID) {
			t.Fatalf("unexpected limited LND notice for %s: %v", def.ID, def.SecurityNotices)
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
