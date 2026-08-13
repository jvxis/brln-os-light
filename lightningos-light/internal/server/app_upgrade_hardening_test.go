package server

import (
	"strings"
	"testing"
)

func TestAppUpgradeReleaseIdentityIsClosedAndCheckedBeforeBuild(t *testing.T) {
	script := embeddedAppUpgradeScript
	required := []string{
		`REPO_URL="https://github.com/jvxis/brln-os-light.git"`,
		`--commit`,
		`--verify-only`,
		`Release tag does not resolve to the expected commit.`,
		`Release source version does not match the requested version.`,
		`mktemp -d /tmp/lightningos-release-verify.XXXXXX`,
		`/tmp/lightningos-release-verify.*)`,
		`worktree add --detach "$worktree_dir" "$EXPECTED_COMMIT"`,
		`"$NPM_BIN" ci`,
	}
	for _, value := range required {
		if !strings.Contains(script, value) {
			t.Fatalf("LightningOS upgrade helper lacks source gate %q", value)
		}
	}
	if strings.Contains(script, `--repo-url`) || strings.Contains(script, `"$NPM_BIN" install`) {
		t.Fatal("LightningOS helper still accepts a repository URL or performs unlocked npm install")
	}
	if strings.Contains(script, "\r") {
		t.Fatal("LightningOS helper contains CRLF and cannot execute through its Linux shebang")
	}
	commitCheck := strings.Index(script, `if [[ "$actual_commit" != "$EXPECTED_COMMIT" ]]`)
	verifyExit := strings.Index(script, `LightningOS release source verified; no application files or services were changed.`)
	logOpen := strings.Index(script, `exec > >(tee -a "$LOG_FILE") 2>&1`)
	build := strings.Index(script, `print_step "Building manager binary"`)
	if commitCheck < 0 || build <= commitCheck {
		t.Fatal("LightningOS source commit is not checked before build")
	}
	if verifyExit < 0 || logOpen <= verifyExit {
		t.Fatal("LightningOS verify-only path opens the upgrade log before exiting")
	}
}

func TestAppReleaseTagMustExactlyMatchNormalizedVersion(t *testing.T) {
	for _, tag := range []string{"0.5.3-Beta", "v0.5.3-beta", "V0.5.3-BETA"} {
		if !appReleaseTagMatchesVersion(tag, "0.5.3-beta") {
			t.Fatalf("valid release tag rejected: %q", tag)
		}
	}
	for _, tag := range []string{"0.5.2-Beta", "release/0.5.3-beta", "0.5.3 beta", "0.5.3_beta"} {
		if appReleaseTagMatchesVersion(tag, "0.5.3-beta") {
			t.Fatalf("unsafe or mismatched release tag accepted: %q", tag)
		}
	}
}
