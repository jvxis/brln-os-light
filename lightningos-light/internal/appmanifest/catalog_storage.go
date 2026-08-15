package appmanifest

import (
	"errors"
	"path"
	"strings"
)

const (
	ElectrsDefaultDataDir = "/var/lib/lightningos/apps-data/electrs"
	MempoolDefaultDataDir = "/var/lib/lightningos/apps-data/mempool"
)

// NormalizeCatalogDataDir keeps writable container mounts in a deliberately
// small namespace. Custom targets are always <mounted-volume>/lightningos/<app>;
// the privileged broker separately proves that they live on a non-root device.
func NormalizeCatalogDataDir(appID, value string) (string, error) {
	if appID != ElectrsID && appID != MempoolID {
		return "", errors.New("catalog storage app is not allowed")
	}
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\\r\n\x00") {
		return "", errors.New("catalog storage path is invalid")
	}
	clean := path.Clean(value)
	if clean != value || clean == "/" {
		return "", errors.New("catalog storage path is not canonical")
	}
	for _, char := range clean {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("/._-", char) {
			continue
		}
		return "", errors.New("catalog storage path contains unsafe characters")
	}
	defaultPath := ElectrsDefaultDataDir
	if appID == MempoolID {
		defaultPath = MempoolDefaultDataDir
	}
	if clean != defaultPath && !strings.HasSuffix(clean, "/lightningos/"+appID) {
		return "", errors.New("catalog storage path is outside the managed layout")
	}
	return clean, nil
}
