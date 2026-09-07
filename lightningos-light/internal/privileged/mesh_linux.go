package privileged

import (
	"errors"
	"golang.org/x/sys/unix"
)

func validateMeshRootDirectory(path string) error {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return err
	}
	if st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 {
		return errors.New("mesh directory is not root controlled")
	}
	return nil
}
