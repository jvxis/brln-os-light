package privileged

import (
	"lightningos-light/internal/appmanifest"
	"os"
	"syscall"
)

// Match the broker-created snapshot, not the root-only private receipt.
func lnbitsEnrollmentOwnerIsSafe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().Perm() == 0640 &&
		(os.Geteuid() != 0 || (stat.Uid == 0 && stat.Gid == appmanifest.LNbitsContainerGID))
}
