package privileged

import (
	"os"
	"syscall"

	"lightningos-light/internal/appmanifest"
)

// The broker snapshot is root-owned but deliberately readable by the container
// group. Do not require GID 0 or allow group/other write access.
func lndgEnrollmentOwnerIsSafe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().Perm() == 0640 &&
		(os.Geteuid() != 0 || (stat.Uid == 0 && stat.Gid == appmanifest.LNDgContainerGID))
}
