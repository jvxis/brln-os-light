package privileged

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"lightningos-light/internal/appmanifest"
)

const (
	bitcoinCoreConfigFile          = "bitcoin.conf"
	bitcoinCoreStorageMarkerFile   = appmanifest.BitcoinCoreStorageMarker
	bitcoinCoreCredentialsFile     = "rpc-credentials.json"
	maxBitcoinCoreConfigBytes      = 8 * 1024
	maxBitcoinCoreCredentialsBytes = 1024
)

type bitcoinCoreStoredCredentials struct {
	Version  int    `json:"version"`
	User     string `json:"user"`
	Password string `json:"password"`
	RPCAuth  string `json:"rpcauth"`
}

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

func generateBitcoinCoreCredentials() (bitcoinCoreStoredCredentials, error) {
	passwordRaw := make([]byte, 32)
	if _, err := rand.Read(passwordRaw); err != nil {
		return bitcoinCoreStoredCredentials{}, err
	}
	saltRaw := make([]byte, 16)
	if _, err := rand.Read(saltRaw); err != nil {
		return bitcoinCoreStoredCredentials{}, err
	}
	password := hex.EncodeToString(passwordRaw)
	salt := hex.EncodeToString(saltRaw)
	digest := hmac.New(sha256.New, []byte(salt))
	_, _ = digest.Write([]byte(password))
	return bitcoinCoreStoredCredentials{
		Version:  1,
		User:     appmanifest.BitcoinCoreRPCUser,
		Password: password,
		RPCAuth:  appmanifest.BitcoinCoreRPCUser + ":" + salt + "$" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func validateBitcoinCoreCredentials(credentials bitcoinCoreStoredCredentials) error {
	if credentials.Version != 1 || credentials.User != appmanifest.BitcoinCoreRPCUser ||
		len(credentials.Password) != 64 || len(credentials.RPCAuth) == 0 {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	if _, err := hex.DecodeString(credentials.Password); err != nil {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	parts := strings.SplitN(strings.TrimPrefix(credentials.RPCAuth, credentials.User+":"), "$", 2)
	if len(parts) != 2 || len(parts[0]) != 32 {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	digest := hmac.New(sha256.New, []byte(parts[0]))
	_, _ = digest.Write([]byte(credentials.Password))
	expected := credentials.User + ":" + parts[0] + "$" + hex.EncodeToString(digest.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(credentials.RPCAuth)) {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	return nil
}

func bitcoinCoreConfigWithRPCAuth(content string, rpcAuth string) (string, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return "", err
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(trimmed, "=", 2)[0])
		switch strings.ToLower(key) {
		case "rpcuser", "rpcpassword", "rpcauth":
			return "", errors.New("bitcoin config template contains RPC credentials")
		}
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	insertAt := len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			insertAt = index
			break
		}
	}
	lines = append(lines, "")
	copy(lines[insertAt+1:], lines[insertAt:])
	lines[insertAt] = "rpcauth=" + rpcAuth
	return strings.Join(lines, "\n") + "\n", nil
}

func marshalBitcoinCoreCredentials(credentials bitcoinCoreStoredCredentials) ([]byte, error) {
	if err := validateBitcoinCoreCredentials(credentials); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
