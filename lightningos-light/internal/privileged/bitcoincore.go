package privileged

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const bitcoinCoreImageAttestationFile = "image-attestation"

var dockerImageIDPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type bitcoinCoreImageAttestation struct {
	ImageID       string
	Release       string
	ArchiveSHA256 string
	BaseImage     string
	Signatures    int
}

func (manager *ComposeAppManager) prepareBitcoinCoreImage(ctx context.Context, unit string) (AppImageState, error) {
	artifact, err := appmanifest.BitcoinCoreArtifactForGOARCH(runtime.GOARCH)
	if err != nil {
		return AppImageState{}, err
	}
	state, err := manager.bitcoinCoreImageStatus(ctx, artifact, unit)
	if err != nil || state.Status == "ready" || state.Status == "preparing" {
		return state, err
	}
	if state.Status == "failed" {
		return state, errors.New("bitcoin core image preparation previously failed")
	}
	attestationPath, err := manager.ensureBitcoinCoreImageRoot()
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
		bitcoinCoreImageBuildScript(artifact, attestationPath),
	}
	if _, err := manager.Runner.Run(ctx, systemdRunPath, args...); err != nil {
		return AppImageState{}, errors.New("bitcoin core image preparation could not be scheduled")
	}
	return AppImageState{Status: "preparing"}, nil
}

func (manager *ComposeAppManager) bitcoinCoreImageStatus(ctx context.Context, artifact appmanifest.BitcoinCoreReleaseArtifact, unit string) (AppImageState, error) {
	attestationPath := manager.bitcoinCoreImageAttestationPath()
	if attestation, err := readBitcoinCoreImageAttestation(attestationPath); err == nil {
		if attestation.Release == appmanifest.BitcoinCoreRelease &&
			attestation.ArchiveSHA256 == artifact.ArchiveSHA256 &&
			attestation.BaseImage == artifact.BaseImage &&
			attestation.Signatures >= appmanifest.BitcoinCoreSignatureThreshold {
			output, inspectErr := manager.Runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.BitcoinCoreImage)
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
		return AppImageState{}, errors.New("bitcoin core image preparation state is invalid")
	}
}

func (manager *ComposeAppManager) bitcoinCoreImageAttestationPath() string {
	root := manager.PrivilegedAppsRoot
	if root == "" {
		root = defaultPrivilegedAppsRoot
	}
	return filepath.Join(root, appmanifest.BitcoinCoreID, bitcoinCoreImageAttestationFile)
}

func (manager *ComposeAppManager) ensureBitcoinCoreImageRoot() (string, error) {
	attestationPath := manager.bitcoinCoreImageAttestationPath()
	if err := ensureDirectoryTreeNoSymlink(filepath.Dir(attestationPath), 0700); err != nil {
		return "", errors.New("failed to secure bitcoin core image root")
	}
	return attestationPath, nil
}

func readBitcoinCoreImageAttestation(path string) (bitcoinCoreImageAttestation, error) {
	var attestation bitcoinCoreImageAttestation
	raw, err := readRegularFile(path, 4096)
	if err != nil {
		return attestation, err
	}
	values := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return attestation, errors.New("invalid bitcoin core image attestation")
		}
		if _, exists := values[key]; exists {
			return attestation, errors.New("duplicate bitcoin core image attestation field")
		}
		values[key] = value
	}
	if len(values) != 5 {
		return attestation, errors.New("invalid bitcoin core image attestation fields")
	}
	signatures, err := strconv.Atoi(values["signatures"])
	if err != nil || signatures < 0 {
		return attestation, errors.New("invalid bitcoin core image signature count")
	}
	if !dockerImageIDPattern.MatchString(values["image_id"]) {
		return attestation, errors.New("invalid bitcoin core image id")
	}
	attestation = bitcoinCoreImageAttestation{
		ImageID:       values["image_id"],
		Release:       values["release"],
		ArchiveSHA256: values["archive_sha256"],
		BaseImage:     values["base_image"],
		Signatures:    signatures,
	}
	if attestation.Release == "" || attestation.ArchiveSHA256 == "" || attestation.BaseImage == "" {
		return bitcoinCoreImageAttestation{}, errors.New("incomplete bitcoin core image attestation")
	}
	return attestation, nil
}

func bitcoinCoreImageBuildScript(artifact appmanifest.BitcoinCoreReleaseArtifact, attestationPath string) string {
	dockerfile := base64.StdEncoding.EncodeToString([]byte(appmanifest.BitcoinCoreDockerfile(artifact.BaseImage)))
	entrypoint := base64.StdEncoding.EncodeToString([]byte(appmanifest.BitcoinCoreEntrypoint()))
	appRoot := filepath.Dir(attestationPath)
	buildRoot := filepath.Dir(appRoot)
	keyDownloads := make([]string, 0, len(appmanifest.BitcoinCoreTrustedBuilders()))
	for _, builder := range appmanifest.BitcoinCoreTrustedBuilders() {
		keyDownloads = append(keyDownloads, fmt.Sprintf(`/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/%s.gpg" https://raw.githubusercontent.com/bitcoin-core/guix.sigs/main/builder-keys/%s.gpg
primary_fingerprints="$(/usr/bin/gpg --batch --with-colons --import-options show-only --import "$work/%s.gpg" 2>/dev/null | /usr/bin/awk -F: '$1=="pub" {want_fpr=1; next} want_fpr && $1=="fpr" {print $10; want_fpr=0}')"
[ "$primary_fingerprints" = %s ]
/usr/bin/gpg --batch --quiet --import "$work/%s.gpg"`, builder.Name, builder.Name, builder.Name, shellLiteral(builder.Fingerprint), builder.Name))
	}

	return fmt.Sprintf(`set -eu
umask 077
export PATH=/usr/sbin:/usr/bin:/sbin:/bin
build_root=%s
app_root=%s
attestation=%s
work="$(/usr/bin/mktemp -d "$build_root/bitcoincore-image.XXXXXX")"
cleanup() {
  case "$work" in
    "$build_root"/bitcoincore-image.*) /bin/rm -rf -- "$work" ;;
    *) exit 99 ;;
  esac
}
trap cleanup EXIT INT TERM
GNUPGHOME="$work/gnupg"
/usr/bin/install -d -m 0700 "$GNUPGHOME"
export GNUPGHOME
/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/SHA256SUMS" https://bitcoincore.org/bin/bitcoin-core-%s/SHA256SUMS
/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/SHA256SUMS.asc" https://bitcoincore.org/bin/bitcoin-core-%s/SHA256SUMS.asc
%s
set +e
gpg_status_output="$(/usr/bin/gpg --batch --status-fd 1 --verify "$work/SHA256SUMS.asc" "$work/SHA256SUMS" 2>/dev/null)"
gpg_status=$?
set -e
case "$gpg_status" in 0|2) ;; *) exit 20 ;; esac
signature_count="$(printf '%%s\n' "$gpg_status_output" | /usr/bin/awk '$1=="[GNUPG:]" && $2=="VALIDSIG" {print $3}' | /usr/bin/sort -u | /usr/bin/wc -l | /usr/bin/tr -d ' ')"
[ "$signature_count" -ge %d ]
/usr/bin/grep -Fx %s "$work/SHA256SUMS" >/dev/null
/usr/bin/curl --proto '=https' --tlsv1.2 -fsSLo "$work/%s" https://bitcoincore.org/bin/bitcoin-core-%s/%s
printf '%%s  %%s\n' %s %s | (cd "$work" && /usr/bin/sha256sum --check --strict -)
/usr/bin/tar --no-same-owner --no-same-permissions -xzf "$work/%s" -C "$work"
[ -f "$work/bitcoin-%s/bin/bitcoind" ] && [ ! -L "$work/bitcoin-%s/bin/bitcoind" ]
[ -f "$work/bitcoin-%s/bin/bitcoin-cli" ] && [ ! -L "$work/bitcoin-%s/bin/bitcoin-cli" ]
printf '%%s' %s | /usr/bin/base64 -d > "$work/Dockerfile"
printf '%%s' %s | /usr/bin/base64 -d > "$work/entrypoint.sh"
/bin/chmod 0600 "$work/Dockerfile"
/bin/chmod 0755 "$work/entrypoint.sh"
/usr/bin/docker build --pull --no-cache --network=none --tag %s --file "$work/Dockerfile" "$work"
/usr/bin/docker run --rm %s --version | /usr/bin/grep -F %s >/dev/null
image_id="$(/usr/bin/docker image inspect --format '{{.Id}}' %s)"
printf 'image_id=%%s\nrelease=%s\narchive_sha256=%s\nbase_image=%s\nsignatures=%%s\n' "$image_id" "$signature_count" > "$work/image-attestation"
/bin/chmod 0600 "$work/image-attestation"
/bin/mv -f "$work/image-attestation" "$attestation"
`, shellLiteral(buildRoot), shellLiteral(appRoot), shellLiteral(attestationPath),
		appmanifest.BitcoinCoreRelease, appmanifest.BitcoinCoreRelease, strings.Join(keyDownloads, "\n"),
		appmanifest.BitcoinCoreSignatureThreshold, shellLiteral(artifact.ArchiveSHA256+"  "+artifact.Archive),
		artifact.Archive, appmanifest.BitcoinCoreRelease, artifact.Archive,
		shellLiteral(artifact.ArchiveSHA256), shellLiteral(artifact.Archive), artifact.Archive,
		appmanifest.BitcoinCoreRelease, appmanifest.BitcoinCoreRelease, appmanifest.BitcoinCoreRelease, appmanifest.BitcoinCoreRelease,
		shellLiteral(dockerfile), shellLiteral(entrypoint), shellLiteral(appmanifest.BitcoinCoreImage),
		shellLiteral(appmanifest.BitcoinCoreImage), shellLiteral("Bitcoin Core daemon version v"+appmanifest.BitcoinCoreRelease+".0 bitcoind"),
		shellLiteral(appmanifest.BitcoinCoreImage), appmanifest.BitcoinCoreRelease, artifact.ArchiveSHA256, artifact.BaseImage)
}

func shellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
