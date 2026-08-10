package privileged

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"lightningos-light/internal/appmanifest"
)

const (
	bitcoinCoreConfigFile        = "bitcoin.conf"
	bitcoinCoreStorageMarkerFile = appmanifest.BitcoinCoreStorageMarker
	maxBitcoinCoreConfigBytes    = 8 * 1024
)

type BitcoinCoreConfigManager struct {
	PrivilegedAppsRoot string
}

func NewBitcoinCoreConfigManager() *BitcoinCoreConfigManager {
	return &BitcoinCoreConfigManager{}
}

func (manager *BitcoinCoreConfigManager) storageRoot() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.BitcoinCoreID)
}

func validateBitcoinCoreConfigContent(content string) error {
	if content == "" {
		return errors.New("bitcoin config content is empty")
	}
	if len(content) > maxBitcoinCoreConfigBytes {
		return errors.New("bitcoin config content is too large")
	}
	if !utf8.ValidString(content) || strings.ContainsAny(content, "\x00\r") {
		return errors.New("bitcoin config content is invalid")
	}
	for _, value := range content {
		if value < 0x20 && value != '\n' && value != '\t' {
			return errors.New("bitcoin config content contains a control character")
		}
	}
	if !strings.HasSuffix(content, "\n") {
		return errors.New("bitcoin config content must end with a newline")
	}
	return nil
}

func newBitcoinCoreConfigTemporaryName() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return ".bitcoin.conf.lightningos-" + hex.EncodeToString(raw), nil
}
