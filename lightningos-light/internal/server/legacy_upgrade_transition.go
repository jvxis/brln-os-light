package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	legacyTransitionTargetVersion = "0.5.9-beta"
	legacyTransitionUnitName      = "lightningos-legacy-privilege-transition"
	legacyTransitionRetryCount    = 60
	legacyTransitionRetryDelay    = 10 * time.Second
)

type legacyTransitionState int

var legacyTransitionStagingPathPattern = regexp.MustCompile(`^/var/lib/lightningos/upgrade-staging/upgrade-app-[0-9a-f]{16}\.sh$`)

const (
	legacyTransitionNotApplicable legacyTransitionState = iota
	legacyTransitionPending
	legacyTransitionStarted
)

func (s *Server) startLegacyPrivilegeTransitionReconciler() {
	if !legacyPrivilegeTransitionCandidate(s.cfg, currentAppVersion(s.cfg.UI.StaticDir)) {
		return
	}

	go func() {
		for attempt := 0; attempt < legacyTransitionRetryCount; attempt++ {
			if !legacyPrivilegeTransitionCandidate(s.cfg, currentAppVersion(s.cfg.UI.StaticDir)) {
				return
			}
			ctx, cancel := context.WithTimeout(s.shutdownContext(), 12*time.Second)
			if appUpgradeRunning(ctx) {
				cancel()
				if !s.waitLegacyPrivilegeTransitionRetry(attempt) {
					return
				}
				continue
			}
			info, err := getAppReleaseInfo(ctx, true)
			if err == nil {
				var state legacyTransitionState
				state, err = startLegacyPrivilegeTransition(ctx, s.cfg, info, embeddedAppUpgradeScript)
				if state == legacyTransitionStarted {
					if s.logger != nil {
						s.logger.Printf("legacy 0.5.2 upgrade bridge to 0.5.9 started")
					}
				}
				if state == legacyTransitionNotApplicable {
					cancel()
					return
				}
			}
			cancel()

			if err != nil && s.logger != nil && (attempt == 0 || attempt == legacyTransitionRetryCount-1) {
				s.logger.Printf("legacy 0.5.2 upgrade bridge to 0.5.9 pending: %v", err)
			}
			if !s.waitLegacyPrivilegeTransitionRetry(attempt) {
				return
			}
		}
	}()
}

func (s *Server) waitLegacyPrivilegeTransitionRetry(attempt int) bool {
	if attempt+1 == legacyTransitionRetryCount {
		return false
	}
	select {
	case <-s.shutdownContext().Done():
		return false
	case <-time.After(legacyTransitionRetryDelay):
		return true
	}
}

func validateLegacyTransitionRelease(info appReleaseInfo, currentVersion string) error {
	current := normalizeAppVersion(currentVersion)
	if current != legacyTransitionTargetVersion || info.Version != legacyTransitionTargetVersion {
		return fmt.Errorf("legacy privilege transition is limited to the %s release", legacyTransitionTargetVersion)
	}
	if !appReleaseTagMatchesVersion(info.Tag, info.Version) {
		return errors.New("legacy privilege transition release tag is invalid")
	}
	if !appCommitPattern.MatchString(strings.ToLower(strings.TrimSpace(info.Commit))) {
		return errors.New("legacy privilege transition release commit is invalid")
	}
	return nil
}

func legacyTransitionHelperDigest(content string) (string, error) {
	if strings.TrimSpace(content) == "" {
		return "", errors.New("legacy privilege transition helper is empty")
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:]), nil
}

func buildLegacyTransitionRootCommand(info appReleaseInfo, sourcePath, digest string, uid, gid uint32) (string, error) {
	if err := validateLegacyTransitionRelease(info, info.Version); err != nil {
		return "", err
	}
	if path.Dir(sourcePath) != "/var/lib/lightningos/upgrade-staging" || path.Clean(sourcePath) != sourcePath || !legacyTransitionStagingPathPattern.MatchString(sourcePath) {
		return "", errors.New("legacy privilege transition staging path is invalid")
	}
	if len(digest) != sha256.Size*2 {
		return "", errors.New("legacy privilege transition helper digest is invalid")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", errors.New("legacy privilege transition helper digest is invalid")
	}

	const installedPath = "/usr/local/sbin/lightningos-upgrade-app-transition"
	metadata := fmt.Sprintf("%d:%d:700", uid, gid)
	commit := strings.ToLower(strings.TrimSpace(info.Commit))
	return strings.Join([]string{
		"set -Eeuo pipefail",
		fmt.Sprintf("src=%s", sourcePath),
		fmt.Sprintf("dest=%s", installedPath),
		`cleanup() { /usr/bin/rm -f -- "$dest" "$src"; }`,
		"trap cleanup EXIT",
		`[ -f "$src" ] && [ ! -L "$src" ]`,
		fmt.Sprintf(`[ "$(/usr/bin/stat -c '%%u:%%g:%%a' "$src")" = %s ]`, metadata),
		fmt.Sprintf(`printf '%%s  %%s\n' %s "$src" | /usr/bin/sha256sum -c -`, digest),
		`if [ -e "$dest" ] || [ -L "$dest" ]; then [ -f "$dest" ] && [ ! -L "$dest" ] && [ "$(/usr/bin/stat -c '%u:%g' "$dest")" = 0:0 ]; fi`,
		`/usr/bin/install -o root -g root -m 0755 "$src" "$dest"`,
		fmt.Sprintf(`printf '%%s  %%s\n' %s "$dest" | /usr/bin/sha256sum -c -`, digest),
		fmt.Sprintf(`"$dest" --version %s --tag %s --commit %s`, info.Version, info.Tag, commit),
	}, "; "), nil
}
