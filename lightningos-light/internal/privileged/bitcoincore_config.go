package privileged

import (
	"context"
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
	bitcoinCoreConfigFile             = "bitcoin.conf"
	bitcoinCoreStorageMarkerFile      = appmanifest.BitcoinCoreStorageMarker
	bitcoinCoreCredentialsFile        = "rpc-credentials.json"
	bitcoinCoreElectrsCredentialsFile = "electrs-rpc-credentials.json"
	maxBitcoinCoreConfigBytes         = 8 * 1024
	maxBitcoinCoreCredentialsBytes    = 1024
)

type bitcoinCoreStoredCredentials struct {
	Version  int    `json:"version"`
	User     string `json:"user"`
	Password string `json:"password"`
	RPCAuth  string `json:"rpcauth"`
}

type BitcoinCoreConfigManager struct {
	PrivilegedAppsRoot     string
	ElectrsCredentialProbe func(context.Context, string, string) error
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
	return generateBitcoinCoreCredentialsForUser(appmanifest.BitcoinCoreRPCUser)
}

func generateBitcoinCoreCredentialsForUser(user string) (bitcoinCoreStoredCredentials, error) {
	if user != appmanifest.BitcoinCoreRPCUser && user != appmanifest.ElectrsBitcoinRPCUser {
		return bitcoinCoreStoredCredentials{}, errors.New("bitcoin RPC credential user is invalid")
	}
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
		User:     user,
		Password: password,
		RPCAuth:  user + ":" + salt + "$" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func validateBitcoinCoreCredentials(credentials bitcoinCoreStoredCredentials) error {
	return validateBitcoinCoreCredentialsForUser(credentials, appmanifest.BitcoinCoreRPCUser)
}

func validateBitcoinCoreCredentialsForUser(credentials bitcoinCoreStoredCredentials, expectedUser string) error {
	if expectedUser != appmanifest.BitcoinCoreRPCUser && expectedUser != appmanifest.ElectrsBitcoinRPCUser {
		return errors.New("bitcoin RPC credentials are invalid")
	}
	if credentials.Version != 1 || credentials.User != expectedUser ||
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

func bitcoinCoreConfigWithElectrsRPCAuth(content string, rpcAuth string) (string, bool, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return "", false, err
	}
	credentials := bitcoinCoreStoredCredentials{User: appmanifest.ElectrsBitcoinRPCUser, RPCAuth: rpcAuth}
	if !strings.HasPrefix(credentials.RPCAuth, credentials.User+":") {
		return "", false, errors.New("bitcoin RPC credential user is invalid")
	}

	// The pre-hardening store already created a dedicated `electrs` rpcauth,
	// but did not retain enough state for the broker to recover its password.
	// Replace only hashes for that dedicated user and preserve every unrelated
	// credential. This activates the managed credential with one Bitcoin
	// restart without leaving the legacy Electrs password valid.
	want := "rpcauth=" + rpcAuth
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	filtered := make([]string, 0, len(lines)+1)
	found := false
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "rpcauth") {
			value := strings.TrimSpace(parts[1])
			if strings.HasPrefix(value, appmanifest.ElectrsBitcoinRPCUser+":") {
				if value == rpcAuth && !found {
					filtered = append(filtered, line)
					found = true
				} else {
					changed = true
				}
				continue
			}
		}
		filtered = append(filtered, line)
	}
	if !found {
		insertAt := len(filtered)
		for index, line := range filtered {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				insertAt = index
				break
			}
		}
		filtered = append(filtered, "")
		copy(filtered[insertAt+1:], filtered[insertAt:])
		filtered[insertAt] = want
		changed = true
	}
	updated := strings.Join(filtered, "\n") + "\n"
	if err := validateBitcoinCoreConfigContent(updated); err != nil {
		return "", false, err
	}
	return updated, changed, nil
}

func bitcoinCoreConfigWithManagedRPCAuth(content string, rpcAuth string, expectedUser string, preserveLegacySameUser bool) (string, bool, error) {
	if err := validateBitcoinCoreConfigContent(content); err != nil {
		return "", false, err
	}
	if expectedUser != appmanifest.BitcoinCoreRPCUser && expectedUser != appmanifest.ElectrsBitcoinRPCUser {
		return "", false, errors.New("bitcoin RPC credential user is invalid")
	}
	want := "rpcauth=" + rpcAuth
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "rpcauth") {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if value == rpcAuth {
			return content, false, nil
		}
		if strings.HasPrefix(value, expectedUser+":") {
			if !preserveLegacySameUser {
				return "", false, errors.New("bitcoin config contains an unmanaged RPC credential")
			}
			// Multiple rpcauth entries for the same user are accepted by Bitcoin
			// Core. Keep the legacy hash during migration because rpcauth is
			// deliberately non-reversible, and append the new managed hash.
			continue
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
	lines[insertAt] = want
	updated := strings.Join(lines, "\n") + "\n"
	if err := validateBitcoinCoreConfigContent(updated); err != nil {
		return "", false, err
	}
	return updated, true, nil
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
	return marshalBitcoinCoreCredentialsForUser(credentials, appmanifest.BitcoinCoreRPCUser)
}

func marshalBitcoinCoreCredentialsForUser(credentials bitcoinCoreStoredCredentials, expectedUser string) ([]byte, error) {
	if err := validateBitcoinCoreCredentialsForUser(credentials, expectedUser); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(credentials)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
