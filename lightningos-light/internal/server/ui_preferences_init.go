package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const uiPreferencesInitRetryCooldown = 10 * time.Second

func (s *Server) initUIPreferences() {
	s.uiPreferencesMu.Lock()
	defer s.uiPreferencesMu.Unlock()

	if s.uiPreferences != nil && s.uiPreferencesErr == "" {
		return
	}
	if !s.uiPreferencesInitAt.IsZero() && time.Since(s.uiPreferencesInitAt) < uiPreferencesInitRetryCooldown {
		return
	}
	s.uiPreferencesInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.uiPreferencesErr = fmt.Sprintf("ui preferences unavailable: %v", err)
		s.logger.Printf("%s", s.uiPreferencesErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.uiPreferencesErr = fmt.Sprintf("ui preferences unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.uiPreferencesErr)
			return
		}
		s.db = pool
	}

	service := NewUIPreferencesService(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.EnsureSchema(ctx); err != nil {
		s.uiPreferencesErr = fmt.Sprintf("ui preferences unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.uiPreferencesErr)
		return
	}

	s.uiPreferences = service
	s.uiPreferencesErr = ""
}

func (s *Server) uiPreferencesService() (*UIPreferencesService, string) {
	s.initUIPreferences()
	return s.uiPreferences, s.uiPreferencesErr
}
