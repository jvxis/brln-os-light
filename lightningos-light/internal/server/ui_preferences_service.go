package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	menuPreferencesKey     = "menu"
	menuPreferencesVersion = 1
	maxMenuPreferenceItems = 128
)

var menuPreferenceKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type MenuPreferences struct {
	Version   int      `json:"version"`
	Favorites []string `json:"favorites"`
	Hidden    []string `json:"hidden"`
}

type MenuPreferencesRecord struct {
	MenuPreferences
	Exists    bool   `json:"exists"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type UIPreferencesService struct {
	db *pgxpool.Pool
}

func NewUIPreferencesService(db *pgxpool.Pool) *UIPreferencesService {
	return &UIPreferencesService{db: db}
}

func (s *UIPreferencesService) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("ui preferences db unavailable")
	}
	_, err := s.db.Exec(ctx, `
create table if not exists ui_preferences (
  preference_key text primary key,
  value jsonb not null,
  updated_at timestamptz not null default now()
);
`)
	return err
}

func (s *UIPreferencesService) LoadMenu(ctx context.Context) (MenuPreferencesRecord, error) {
	defaults := MenuPreferencesRecord{
		MenuPreferences: MenuPreferences{
			Version:   menuPreferencesVersion,
			Favorites: []string{},
			Hidden:    []string{},
		},
	}
	if s == nil || s.db == nil {
		return defaults, errors.New("ui preferences db unavailable")
	}

	var raw []byte
	var updatedAt string
	err := s.db.QueryRow(ctx, `
select value, to_char(updated_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
from ui_preferences
where preference_key = $1
`, menuPreferencesKey).Scan(&raw, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}

	var preferences MenuPreferences
	if err := json.Unmarshal(raw, &preferences); err != nil {
		return defaults, fmt.Errorf("decode menu preferences: %w", err)
	}
	normalized, err := normalizeMenuPreferences(preferences)
	if err != nil {
		return defaults, fmt.Errorf("stored menu preferences are invalid: %w", err)
	}
	return MenuPreferencesRecord{
		MenuPreferences: normalized,
		Exists:          true,
		UpdatedAt:       updatedAt,
	}, nil
}

func (s *UIPreferencesService) SaveMenu(ctx context.Context, preferences MenuPreferences) (MenuPreferencesRecord, error) {
	if s == nil || s.db == nil {
		return MenuPreferencesRecord{}, errors.New("ui preferences db unavailable")
	}
	normalized, err := normalizeMenuPreferences(preferences)
	if err != nil {
		return MenuPreferencesRecord{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return MenuPreferencesRecord{}, err
	}

	var updatedAt string
	err = s.db.QueryRow(ctx, `
insert into ui_preferences (preference_key, value, updated_at)
values ($1, $2::jsonb, now())
on conflict (preference_key) do update
set value = excluded.value,
    updated_at = now()
returning to_char(updated_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
`, menuPreferencesKey, string(raw)).Scan(&updatedAt)
	if err != nil {
		return MenuPreferencesRecord{}, err
	}
	return MenuPreferencesRecord{
		MenuPreferences: normalized,
		Exists:          true,
		UpdatedAt:       updatedAt,
	}, nil
}

func normalizeMenuPreferences(preferences MenuPreferences) (MenuPreferences, error) {
	favorites, err := normalizeMenuPreferenceKeys(preferences.Favorites)
	if err != nil {
		return MenuPreferences{}, fmt.Errorf("favorites: %w", err)
	}
	hidden, err := normalizeMenuPreferenceKeys(preferences.Hidden)
	if err != nil {
		return MenuPreferences{}, fmt.Errorf("hidden: %w", err)
	}

	hiddenSet := make(map[string]struct{}, len(hidden))
	for _, key := range hidden {
		hiddenSet[key] = struct{}{}
	}
	visibleFavorites := make([]string, 0, len(favorites))
	for _, key := range favorites {
		if _, isHidden := hiddenSet[key]; !isHidden {
			visibleFavorites = append(visibleFavorites, key)
		}
	}

	return MenuPreferences{
		Version:   menuPreferencesVersion,
		Favorites: visibleFavorites,
		Hidden:    hidden,
	}, nil
}

func normalizeMenuPreferenceKeys(keys []string) ([]string, error) {
	if len(keys) > maxMenuPreferenceItems {
		return nil, fmt.Errorf("cannot contain more than %d items", maxMenuPreferenceItems)
	}
	result := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		if !menuPreferenceKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid menu item key %q", rawKey)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result, nil
}
