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
}

func TestIsAppHiddenFromStore(t *testing.T) {
	if !isAppHiddenFromStore(depixBuyAppID) {
		t.Fatalf("expected %s to be hidden from store", depixBuyAppID)
	}
	if isAppHiddenFromStore(bitcoinCoreAppID) {
		t.Fatalf("expected %s to remain visible in store", bitcoinCoreAppID)
	}
}
