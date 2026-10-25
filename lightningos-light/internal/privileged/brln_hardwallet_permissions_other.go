//go:build !linux

package privileged

import (
	"errors"
	"lightningos-light/internal/appmanifest"
	"os"
)

// Non-Linux checks support unit tests, never Linux deployment attestation.
func validateHardwalletPermissions(p appmanifest.HardwalletPaths) error {
	for _, path := range []string{p.Compose, p.Settings, p.Token, p.Caddy, p.Certificate, p.PrivateKey} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("unsafe Hardwallet file")
		}
	}
	return nil
}
