package server

import (
	"errors"
	"log"
	"os"
	"strings"
)

func ResolveNotificationsDSN(logger *log.Logger) (string, error) {
	return resolveNotificationsDSN(
		os.Getenv("NOTIFICATIONS_PG_DSN"),
		postgresNotificationsDSNUsable,
		func() (string, error) { return bootstrapNotificationsDSN(logger) },
	)
}

func resolveNotificationsDSN(current string, usable func(string) bool, bootstrap func() (string, error)) (string, error) {
	current = strings.TrimSpace(current)
	if current != "" && !isPlaceholderDSN(current) && usable != nil && usable(current) {
		return current, nil
	}
	if bootstrap == nil {
		return "", errors.New("NOTIFICATIONS_PG_DSN not set")
	}
	dsn, err := bootstrap()
	if err != nil {
		return "", err
	}
	dsn = strings.TrimSpace(dsn)
	if dsn == "" || isPlaceholderDSN(dsn) || usable == nil || !usable(dsn) {
		return "", errors.New("NOTIFICATIONS_PG_DSN is unavailable")
	}
	return dsn, nil
}
