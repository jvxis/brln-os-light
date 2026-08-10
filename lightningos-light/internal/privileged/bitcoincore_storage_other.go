//go:build !linux

package privileged

import (
	"errors"
	"os"
)

func storageDeviceID(string) (uint64, error) {
	return 0, errors.New("bitcoin storage is supported only on Linux")
}

func storageAvailableKiB(string) (uint64, error) {
	return 0, errors.New("bitcoin storage is supported only on Linux")
}

func validateRootOwnedDirectory(string, os.FileMode) error {
	return errors.New("bitcoin storage is supported only on Linux")
}

func validateRootOwnedRegularFile(string, os.FileMode) error {
	return errors.New("bitcoin storage is supported only on Linux")
}
