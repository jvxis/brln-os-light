package server

import (
	"bytes"
	"errors"
	"os"
)

func lndCredentialEqualsNativeAdmin(credential []byte) (bool, error) {
	admin, err := os.ReadFile(lndAdminMacaroonPath)
	if err != nil {
		// After manager credential migration the native admin macaroon is 0600
		// and intentionally unreadable to the manager. Every privileged app
		// placement independently repeats the equality check as root.
		if errors.Is(err, os.ErrPermission) {
			return false, nil
		}
		return false, errors.New("native LND admin credential is unavailable")
	}
	return bytes.Equal(credential, admin), nil
}
