//go:build !linux

package privileged

func setLoopFileOwnership(path, ownerName, groupName string) error {
	return nil
}

func setLoopDirectoryOwnership(path, ownerName, groupName string) error {
	return nil
}

func setLoopTreeOwnership(roots []string, ownerName, groupName string) error {
	return nil
}
