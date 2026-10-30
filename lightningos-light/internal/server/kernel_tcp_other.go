//go:build !linux

package server

import "os"

func kernelTCPRootOwned(os.FileInfo) bool { return false }
