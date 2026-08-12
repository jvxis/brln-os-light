//go:build linux

package privileged

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

func setLoopFileOwnership(path, ownerName, groupName string) error {
	return setLoopPathOwnership(path, ownerName, groupName, false)
}

func setLoopDirectoryOwnership(path, ownerName, groupName string) error {
	return setLoopPathOwnership(path, ownerName, groupName, true)
}

func setLoopPathOwnership(path, ownerName, groupName string, wantDirectory bool) error {
	owner, err := user.Lookup(ownerName)
	if err != nil {
		return errors.New("fixed Lightning Loop owner is unavailable")
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return errors.New("fixed Lightning Loop group is unavailable")
	}
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return errors.New("fixed Lightning Loop owner is invalid")
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return errors.New("fixed Lightning Loop group is invalid")
	}
	info, err := os.Lstat(path)
	validType := info != nil && ((!wantDirectory && info.Mode().IsRegular()) || (wantDirectory && info.IsDir()))
	if err != nil || !validType || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Lightning Loop ownership target is unsafe")
	}
	// Lchown never follows a symlink if the app user races the final pathname.
	return os.Lchown(path, uid, gid)
}

func setLoopTreeOwnership(roots []string, ownerName, groupName string) error {
	owner, err := user.Lookup(ownerName)
	if err != nil {
		return errors.New("fixed Lightning Loop owner is unavailable")
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return errors.New("fixed Lightning Loop group is unavailable")
	}
	uid, err := strconv.Atoi(owner.Uid)
	if err != nil {
		return errors.New("fixed Lightning Loop owner is invalid")
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return errors.New("fixed Lightning Loop group is invalid")
	}
	for _, root := range roots {
		if err := filepathWalkNoSymlinks(root, func(path string, entry fs.DirEntry) error {
			if entry.Type()&os.ModeSymlink != 0 {
				return errors.New("Lightning Loop ownership tree contains a symlink")
			}
			info, err := entry.Info()
			if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
				return errors.New("Lightning Loop ownership tree contains an unsafe entry")
			}
			return os.Lchown(path, uid, gid)
		}); err != nil {
			return err
		}
	}
	return nil
}

func filepathWalkNoSymlinks(root string, visit func(string, fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return visit(path, entry)
	})
}
