//go:build !linux

package privileged

import "os"

// Ownership enforcement is covered by native Linux tests.
func lndgEnrollmentOwnerIsSafe(info os.FileInfo) bool { return info.Mode().IsRegular() }
