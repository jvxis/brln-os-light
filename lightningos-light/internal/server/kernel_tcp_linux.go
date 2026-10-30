//go:build linux

package server

import (
	"os"
	"syscall"
)

func kernelTCPRootOwned(info os.FileInfo) bool {
	value, ok := info.Sys().(*syscall.Stat_t)
	return ok && value.Uid == 0 && value.Gid == 0
}
