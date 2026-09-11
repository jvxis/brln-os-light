//go:build !linux

package privileged

import "errors"

func validateMeshRootDirectory(string) error { return errors.New("LOS Mesh requires Linux") }
