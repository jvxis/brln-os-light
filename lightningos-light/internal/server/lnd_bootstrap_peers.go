package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Full legacy template values, not pubkey prefixes, domains or a blanket
// addpeer rule. Operator peers and alternative endpoints must survive.
var legacyLNDBootstrapPeers = [...]string{
	"023e24602891c28a7872ea1ad5c1bb41abe4206ae1599bb981e3278a121e7895d6@fmvh2654bp7fcoysr5qikkxsbheps4efpsi5cg2mj7csv5c5t4o3brad.onion:9735",
	"03477b0f9679de60b3a803b47294e37b4c14a383564afded973114134623d2ec82@owczcn2vcq5gs5bn5rv3vadtcob3yq34ywnqwglnkejsftdlkc5a4vyd.onion:9735",
}

const lndBootstrapPeersConfigLimit = 1024 * 1024

// Change ONLY the first key byte to '#'. Same-size comments preserve every
// offset/EOL and remain valid even if interrupted between the two peer edits.
// The original configuration is retained in a private migration backup.
func disableLegacyLNDBootstrapPeers(raw []byte) ([]byte, int) {
	var out strings.Builder
	section := ""
	count := 0
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
		}
		if section == "" || section == "application options" {
			key, value, ok := strings.Cut(trimmed, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "addpeer") {
				value = strings.TrimSpace(value)
				if decoded, err := strconv.Unquote(value); err == nil {
					value = decoded
				}
				for _, peer := range legacyLNDBootstrapPeers {
					if value == peer {
						keyStart := strings.Index(line, trimmed)
						line = line[:keyStart] + "#" + line[keyStart+1:]
						count++
						break
					}
				}
			}
		}
		out.WriteString(line)
	}
	if count == 0 {
		return raw, 0
	}
	return []byte(out.String()), count
}

// The backup contains credentials from lnd.conf: keep it private, reject links
// and unsafe existing state, and make it durable BEFORE changing the config.
func backupLNDBootstrapPeersConfig(directory string, raw []byte) error {
	parent, err := os.Lstat(filepath.Dir(directory))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return errors.New("LND migration backup parent is unavailable or unsafe")
	}
	if err := os.Mkdir(directory, 0700); err != nil && !os.IsExist(err) {
		return errors.New("LND migration backup directory could not be created")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS == "linux" && info.Mode().Perm()&0077 != 0 {
		return errors.New("LND migration backup directory is unsafe")
	}
	if err := syncLNDBootstrapMigrationDirectory(filepath.Dir(directory)); err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	return writeLNDBootstrapMigrationRecord(filepath.Join(directory, fmt.Sprintf("default-addpeer-v1-%x.conf", digest)), raw)
}

func writeLNDBootstrapMigrationRecord(path string, raw []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || runtime.GOOS == "linux" && info.Mode().Perm()&0077 != 0 || info.Size() != int64(len(raw)) {
			return errors.New("LND migration backup is unsafe or incomplete")
		}
		stored, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(stored, raw) {
			return errors.New("LND migration backup does not match the original")
		}
		if runtime.GOOS == "linux" {
			file, err := os.Open(path)
			if err != nil {
				return errors.New("LND migration backup is unavailable")
			}
			defer file.Close()
			if err := file.Sync(); err != nil {
				return errors.New("LND migration backup sync failed")
			}
		}
		return syncLNDBootstrapMigrationDirectory(filepath.Dir(path))
	} else if !os.IsNotExist(err) {
		return errors.New("LND migration record is unavailable")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".default-addpeer-v1-")
	if err != nil {
		return errors.New("LND migration backup could not be created")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if n, err := file.Write(raw); err != nil || n != len(raw) {
		return errors.New("LND migration backup write failed")
	}
	if err := file.Sync(); err != nil {
		return errors.New("LND migration backup sync failed")
	}
	if err := file.Close(); err != nil {
		return errors.New("LND migration backup close failed")
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return errors.New("LND migration backup publication failed")
	}
	return syncLNDBootstrapMigrationDirectory(filepath.Dir(path))
}

func syncLNDBootstrapMigrationDirectory(path string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return errors.New("LND migration directory is unavailable")
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return errors.New("LND migration directory sync failed")
	}
	return nil
}

type lndBootstrapPeerMigration struct {
	configPath string
	backupDir  string
	// Isolated fault injection seam; production always uses the sparse writer.
	patch func(string, []byte, []byte) error
}

// Caller holds lndConfigMutationMu. Parent directory links used by existing
// installs remain supported; an individual linked config is left for review.
func (migration lndBootstrapPeerMigration) run(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	pathDigest := sha256.Sum256([]byte(migration.configPath))
	done := filepath.Join(migration.backupDir, fmt.Sprintf("default-addpeer-v1-%x.done", pathDigest))
	if info, err := os.Lstat(done); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != 5 || runtime.GOOS == "linux" && info.Mode().Perm()&0077 != 0 {
			return 0, errors.New("LND bootstrap-peer completion record is unsafe")
		}
		raw, err := os.ReadFile(done)
		if err != nil || string(raw) != "done\n" {
			return 0, errors.New("LND bootstrap-peer completion record is invalid")
		}
		// Explicit operator choices after completion are not silently undone.
		return 0, nil
	} else if !os.IsNotExist(err) {
		return 0, errors.New("LND bootstrap-peer migration state is unavailable")
	}
	info, err := os.Lstat(migration.configPath)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > lndBootstrapPeersConfigLimit {
		return 0, errors.New("LND configuration is unavailable or unsupported for bootstrap-peer migration")
	}
	raw, err := os.ReadFile(migration.configPath)
	if err != nil || len(raw) > lndBootstrapPeersConfigLimit {
		return 0, errors.New("LND configuration could not be read for bootstrap-peer migration")
	}
	updated, count := disableLegacyLNDBootstrapPeers(raw)
	if count == 0 {
		return 0, nil
	}
	if err := validateLNDConfigSecurity(string(raw), string(updated)); err != nil {
		return 0, errors.New("LND bootstrap-peer migration requires configuration review")
	}
	if err := backupLNDBootstrapPeersConfig(migration.backupDir, raw); err != nil {
		return 0, err
	}
	// Respect concurrent edits/replacement rather than overwriting a stale read.
	currentInfo, err := os.Lstat(migration.configPath)
	if err != nil || !os.SameFile(info, currentInfo) {
		return 0, errors.New("LND configuration was replaced during bootstrap-peer migration")
	}
	current, err := os.ReadFile(migration.configPath)
	if err != nil || !bytes.Equal(current, raw) {
		return 0, errors.New("LND configuration changed during bootstrap-peer migration")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	patch := migration.patch
	if patch == nil {
		patch = func(path string, before, after []byte) error {
			return writeLNDBootstrapPeerComments(path, before, after, info)
		}
	}
	if err := patch(migration.configPath, raw, updated); err != nil {
		return 0, err
	}
	stored, err := os.ReadFile(migration.configPath)
	if err != nil || !bytes.Equal(stored, updated) {
		return 0, io.ErrUnexpectedEOF
	}
	if err := writeLNDBootstrapMigrationRecord(done, []byte("done\n")); err != nil {
		return 0, err
	}
	return count, nil
}

func writeLNDBootstrapPeerComments(path string, previous, updated []byte, expected os.FileInfo) error {
	if len(previous) != len(updated) {
		return errors.New("LND bootstrap-peer migration must preserve byte offsets")
	}
	// Recompute the allowed transformation, not a general config write API.
	want, count := disableLegacyLNDBootstrapPeers(previous)
	if count == 0 || !bytes.Equal(want, updated) {
		return errors.New("LND bootstrap-peer migration contains an unexpected change")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || expected == nil || !os.SameFile(expected, info) {
		return errors.New("LND bootstrap-peer configuration identity is unsafe")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return errors.New("LND bootstrap-peer configuration is not writable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("LND bootstrap-peer configuration was replaced")
	}
	current, err := io.ReadAll(io.LimitReader(file, lndBootstrapPeersConfigLimit+1))
	if err != nil || !bytes.Equal(current, previous) {
		return errors.New("LND bootstrap-peer configuration changed before write")
	}
	for offset := range previous {
		if previous[offset] == updated[offset] {
			continue
		}
		// Each individual patch comments one recognized option, never shifts
		// another byte. A partial failure leaves a valid config for retry.
		if n, err := file.WriteAt([]byte{'#'}, int64(offset)); err != nil || n != 1 {
			return errors.New("LND bootstrap-peer comment write failed; cleanup can be retried")
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

// Run from the NEW Manager, not only a new upgrade shell helper: supported old
// updaters also install/start this binary. Failure is non-fatal to the Manager;
// never restart LND (even when it is currently down) to apply this cleanup.
func (s *Server) reconcileLNDBootstrapPeers() {
	lndConfigMutationMu.Lock()
	defer lndConfigMutationMu.Unlock()
	count, err := (lndBootstrapPeerMigration{
		configPath: s.lndConfigOperations().path,
		backupDir:  "/var/lib/lightningos/lnd-config-migrations",
	}).run(s.shutdownContext())
	if s.logger == nil {
		return
	}
	if err != nil {
		// Do not log raw errors/configuration/backup contents or private values.
		s.logger.Print("LND default bootstrap-peer cleanup deferred; review lnd.conf permissions/configuration and private migration backups; LND was not restarted")
	} else if count > 0 {
		s.logger.Printf("LND default bootstrap-peer cleanup: disabled %d legacy entries; effective at next operator-scheduled LND restart; LND was not restarted", count)
	}
}
