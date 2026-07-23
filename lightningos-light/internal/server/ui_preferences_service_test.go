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
