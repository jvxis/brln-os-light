package server

import (
	"reflect"
	"testing"
)

func TestNormalizeMenuPreferences(t *testing.T) {
	got, err := normalizeMenuPreferences(MenuPreferences{
		Version:   99,
		Favorites: []string{"wallet", "fee-center", "wallet", "terminal"},
		Hidden:    []string{"terminal", "logs", "logs"},
	})
	if err != nil {
		t.Fatalf("normalizeMenuPreferences returned error: %v", err)
	}

	want := MenuPreferences{
		Version:   menuPreferencesVersion,
		Favorites: []string{"wallet", "fee-center"},
		Hidden:    []string{"terminal", "logs"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeMenuPreferences = %#v, want %#v", got, want)
	}
}

func TestNormalizeMenuPreferencesRejectsInvalidKey(t *testing.T) {
	_, err := normalizeMenuPreferences(MenuPreferences{
		Favorites: []string{"wallet", "../secrets"},
	})
	if err == nil {
		t.Fatal("normalizeMenuPreferences should reject an invalid route key")
	}
}

func TestNormalizeAppStorePreferences(t *testing.T) {
	got, err := normalizeAppStorePreferences(AppStorePreferences{
		Version: 77,
		Hidden:  []string{"fedimint-guardian", "cpuminer", "fedimint-guardian"},
	})
	if err != nil {
		t.Fatalf("normalizeAppStorePreferences returned error: %v", err)
	}
	want := AppStorePreferences{
		Version: appStorePreferencesVersion,
		Hidden:  []string{"fedimint-guardian", "cpuminer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeAppStorePreferences = %#v, want %#v", got, want)
	}
}

func TestNormalizeAppStorePreferencesRejectsInvalidKey(t *testing.T) {
	_, err := normalizeAppStorePreferences(AppStorePreferences{Hidden: []string{"../../bitcoin.conf"}})
	if err == nil {
		t.Fatal("normalizeAppStorePreferences should reject an invalid app id")
	}
}
