package privileged

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const lndgImageAttestationFile = "image-attestation"

type lndgImageAttestation struct {
	ImageID      string
	Release      string
	Commit       string
	SourceSHA256 string
	BaseImage    string
}

func (manager *ComposeAppManager) prepareLNDgImage(ctx context.Context, unit string) (AppImageState, error) {
	state, err := manager.lndgImageStatus(ctx, unit)
	if err != nil || state.Status == "ready" || state.Status == "preparing" {
		return state, err
	}
	if state.Status == "failed" {
		return state, errors.New("lndg image preparation previously failed")
	}
	attestationPath, err := manager.ensureLNDgImageRoot()
	if err != nil {
		return AppImageState{}, err
	}
	args := []string{
		"--quiet",
		"--collect",
		"--unit=" + unit,
		"--property=Type=exec",
		"--property=RuntimeMaxSec=10min",
		"/bin/sh",
		"-c",
		lndgImageBuildScript(attestationPath),
	}
	if _, err := manager.Runner.Run(ctx, systemdRunPath, args...); err != nil {
		return AppImageState{}, errors.New("lndg image preparation could not be scheduled")
	}
	return AppImageState{Status: "preparing"}, nil
}

func (manager *ComposeAppManager) lndgImageStatus(ctx context.Context, unit string) (AppImageState, error) {
	attestationPath := manager.lndgImageAttestationPath()
	if attestation, err := readLNDgImageAttestation(attestationPath); err == nil {
		if attestation.Release == appmanifest.LNDgRelease &&
			attestation.Commit == appmanifest.LNDgSourceCommit &&
			attestation.SourceSHA256 == appmanifest.LNDgSourceSHA256 &&
			attestation.BaseImage == appmanifest.LNDgBaseImage {
			output, inspectErr := manager.Runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.LNDgImage)
			if inspectErr == nil && strings.TrimSpace(output) == attestation.ImageID {
				return AppImageState{Status: "ready"}, nil
			}
		}
	}

	output, showErr := manager.Runner.Run(ctx, systemctlPath, "show",
		"--property=LoadState", "--property=ActiveState", "--property=SubState", "--property=Result", "--no-pager", unit)
	values := parseSystemdProperties(output)
	if values["LoadState"] == "not-found" || (showErr != nil && values["LoadState"] == "") {
		return AppImageState{Status: "absent"}, nil
	}
	switch values["ActiveState"] {
	case "active", "activating", "reloading":
		return AppImageState{Status: "preparing"}, nil
	case "failed", "inactive", "deactivating":
		return AppImageState{Status: "failed"}, nil
	default:
		return AppImageState{}, errors.New("lndg image preparation state is invalid")
	}
}

func (manager *ComposeAppManager) lndgImageAttestationPath() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.LNDgID, lndgImageAttestationFile)
}

func (manager *ComposeAppManager) ensureLNDgImageRoot() (string, error) {
	attestationPath := manager.lndgImageAttestationPath()
	if err := ensureDirectoryTreeNoSymlink(filepath.Dir(attestationPath), 0700); err != nil {
		return "", errors.New("failed to secure lndg image root")
	}
	return attestationPath, nil
}

func readLNDgImageAttestation(path string) (lndgImageAttestation, error) {
	var attestation lndgImageAttestation
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return attestation, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return attestation, errors.New("invalid lndg image attestation")
		}
		if _, exists := values[key]; exists {
			return attestation, errors.New("duplicate lndg image attestation field")
		}
		values[key] = value
	}
	if len(values) != 5 || !dockerImageIDPattern.MatchString(values["image_id"]) {
		return attestation, errors.New("invalid lndg image attestation fields")
	}
	attestation = lndgImageAttestation{
		ImageID:      values["image_id"],
		Release:      values["release"],
		Commit:       values["commit"],
		SourceSHA256: values["source_sha256"],
		BaseImage:    values["base_image"],
	}
	if attestation.Release == "" || attestation.Commit == "" || attestation.SourceSHA256 == "" || attestation.BaseImage == "" {
		return lndgImageAttestation{}, errors.New("incomplete lndg image attestation")
	}
	return attestation, nil
}

func lndgImageBuildScript(attestationPath string) string {
	dockerfile := base64.StdEncoding.EncodeToString([]byte(appmanifest.LNDgDockerfile()))
	appRoot := filepath.Dir(attestationPath)
	buildRoot := filepath.Dir(appRoot)
	return fmt.Sprintf(`set -eu
umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
build_root=%s
attestation=%s
work="$(/usr/bin/mktemp -d "$build_root/lndg-image.XXXXXX")"
cleanup() {
  case "$work" in
    "$build_root"/lndg-image.*) /bin/rm -rf -- "$work" ;;
    *) exit 99 ;;
  esac
}
trap cleanup EXIT INT TERM
/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/source.tar.gz" %s
printf '%%s  %%s\n' %s source.tar.gz | (cd "$work" && /usr/bin/sha256sum --check --strict -)
/usr/bin/tar --no-same-owner --no-same-permissions -xzf "$work/source.tar.gz" -C "$work"
[ -d "$work/%s" ] && [ ! -L "$work/%s" ]
[ -f "$work/%s/requirements.txt" ] && [ ! -L "$work/%s/requirements.txt" ]
printf '%%s' %s | /usr/bin/base64 -d > "$work/Dockerfile"
/bin/chmod 0600 "$work/Dockerfile"
/usr/bin/docker build --pull --no-cache --tag %s --file "$work/Dockerfile" "$work"
/usr/bin/docker run --rm %s python -c 'import django, grpc, pandas, psycopg2, supervisor, whitenoise' >/dev/null
image_id="$(/usr/bin/docker image inspect --format '{{.Id}}' %s)"
printf 'image_id=%%s\nrelease=%s\ncommit=%s\nsource_sha256=%s\nbase_image=%s\n' "$image_id" > "$work/image-attestation"
/bin/chmod 0600 "$work/image-attestation"
/bin/mv -f "$work/image-attestation" "$attestation"
`, shellLiteral(buildRoot), shellLiteral(attestationPath), shellLiteral(appmanifest.LNDgSourceURL),
		shellLiteral(appmanifest.LNDgSourceSHA256), appmanifest.LNDgSourceDir, appmanifest.LNDgSourceDir,
		appmanifest.LNDgSourceDir, appmanifest.LNDgSourceDir, shellLiteral(dockerfile), shellLiteral(appmanifest.LNDgImage),
		shellLiteral(appmanifest.LNDgImage), shellLiteral(appmanifest.LNDgImage), appmanifest.LNDgRelease,
		appmanifest.LNDgSourceCommit, appmanifest.LNDgSourceSHA256, appmanifest.LNDgBaseImage)
}
