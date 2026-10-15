//go:build linux

package privileged

import (
	"errors"
	"lightningos-light/internal/appmanifest"
	"os"
	"syscall"
)

func validateHardwalletPermissions(p appmanifest.HardwalletPaths) error {
	for _, f := range []struct {
		path string
		mode os.FileMode
		gid  uint32
		dir  bool
	}{
		{p.Root, 0700, 0, true}, {p.Compose, 0600, 0, false}, {p.Settings, 0600, 0, false},
		{p.Token, 0640, appmanifest.HardwalletUID, false}, {p.Caddy, 0640, appmanifest.HardwalletProxyGID, false},
		{p.Certificate, 0640, appmanifest.HardwalletProxyGID, false}, {p.PrivateKey, 0640, appmanifest.HardwalletProxyGID, false},
	} {
		info, err := os.Lstat(f.path)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || st.Gid != f.gid || info.Mode().Perm() != f.mode || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != f.dir || (!f.dir && !info.Mode().IsRegular()) {
			return errors.New("unsafe Hardwallet snapshot permissions")
		}
	}
	return nil
}
