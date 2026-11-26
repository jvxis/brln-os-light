package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
)

const treasuryKeyFile = "/var/lib/lightningos/treasury-credential.key"
const treasuryKeySize = 32

func treasuryKey(create bool) ([]byte, error) {
	info, err := os.Lstat(treasuryKeyFile)
	if errors.Is(err, os.ErrNotExist) && create {
		key := make([]byte, treasuryKeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(treasuryKeyFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			return treasuryKey(false)
		}
		if err != nil {
			return nil, err
		}
		_, writeErr := file.Write(key)
		syncErr := file.Sync()
		closeErr := file.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			_ = os.Remove(treasuryKeyFile)
			return nil, err
		}
		return key, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != treasuryKeySize {
		return nil, errors.New("Treasury credential key unavailable")
	}
	file, err := os.Open(treasuryKeyFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return nil, errors.New("Treasury credential key changed")
	}
	return io.ReadAll(io.LimitReader(file, treasuryKeySize+1))
}

func treasuryCipher(create bool) (cipher.AEAD, error) {
	key, err := treasuryKey(create)
	if err != nil || len(key) != treasuryKeySize {
		return nil, errors.New("Treasury credential key unavailable")
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealTreasuryCredential(value string) (string, error) {
	aead, err := treasuryCipher(true)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(value), []byte(treasuryAppID))), nil
}

func openTreasuryCredential(value string) (string, error) {
	aead, err := treasuryCipher(false)
	if err != nil {
		return "", err
	}
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return "", errors.New("Treasury credential is corrupt")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(treasuryAppID))
	if err != nil {
		return "", errors.New("Treasury credential could not be decrypted")
	}
	defer clear(plain)
	return string(plain), nil
}
