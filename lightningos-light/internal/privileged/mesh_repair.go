package privileged

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// RepairMeshBinary is a fixed, root-only host operation. Older upgrade helpers
// build the new broker but omit the standalone Mesh executable. The broker
// includes the identical unprivileged daemon entry point for this migration.
// Existing files are never replaced, and no caller-supplied path is accepted.
func RepairMeshBinary() error {
	if os.Geteuid() != 0 {
		return errors.New("root required")
	}
	const broker = "/usr/local/libexec/lightningos-privileged"
	executable, err := os.Executable()
	if err != nil || executable != broker {
		return errors.New("unexpected repair executable")
	}
	return repairMeshBinaryFrom(broker, meshBinaryPath)
}

func repairMeshBinaryFrom(source, target string) error {
	if err := validateMeshRootDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	if err := validateRootOwnedRegularFile(source, 0755); err != nil {
		return err
	}
	if _, err := os.Lstat(target); err == nil {
		return validateRootOwnedRegularFile(target, 0755)
	} else if !os.IsNotExist(err) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(target), ".mesh-repair-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Chmod(0755); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	// Link publishes atomically without overwriting a concurrent installation.
	if err = os.Link(out.Name(), target); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
