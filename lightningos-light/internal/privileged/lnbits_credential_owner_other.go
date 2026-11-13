//go:build !linux

package privileged

import "os"

// Native Linux tests exercise the broker's actual ownership contract.
func lnbitsEnrollmentOwnerIsSafe(info os.FileInfo) bool { return info.Mode().IsRegular() }
