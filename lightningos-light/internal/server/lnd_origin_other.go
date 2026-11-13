//go:build !linux

package server

import "os"

func lndOriginOwnedByRoot(os.FileInfo) bool { return false }
