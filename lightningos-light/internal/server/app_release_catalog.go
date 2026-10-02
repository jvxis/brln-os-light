package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	appLegacyCatalog = "jvxis/brln-os-light"
	appModernCatalog = "jvxis/brln-os-light-updates"
)

var errAppCatalogEmpty = errors.New("no suitable app release found")

// Old Managers query the legacy repository directly and cannot be changed
// retroactively. Keep its published releases capped at the 0.5.40 bridge;
// subsequent immutable releases live in the modern catalog with mirrored tags.
func appCatalogForVersion(version string) string {
	v := parseSemver(normalizeAppVersion(version))
	if v.Valid && (v.Major > 0 || v.Minor > 5 || (v.Minor == 5 && v.Patch >= 40)) {
		return appModernCatalog
	}
	return appLegacyCatalog
}

func appCatalogAllowsRelease(repository, version string) bool {
	v := parseSemver(normalizeAppVersion(version))
	if !v.Valid {
		return false
	}
	afterBridge := v.Major > 0 || v.Minor > 5 || (v.Minor == 5 && v.Patch > 40)
	return (repository == appLegacyCatalog && !afterBridge) || (repository == appModernCatalog && afterBridge)
}

func fetchAppReleaseForVersion(ctx context.Context, currentVersion string) (appReleaseInfo, error) {
	if appCatalogForVersion(currentVersion) == appModernCatalog {
		info, err := fetchAppCatalog(ctx, appModernCatalog)
		if err == nil || !errors.Is(err, errAppCatalogEmpty) {
			return info, err
		}
		// A not-yet-created or empty modern repository is expected while only
		// the bridge is published. Network/auth/server failures are not hidden.
	}
	return fetchAppCatalog(ctx, appLegacyCatalog)
}

func fetchAppCatalog(ctx context.Context, repository string) (appReleaseInfo, error) {
	if repository != appLegacyCatalog && repository != appModernCatalog {
		return appReleaseInfo{}, errors.New("unsupported app release catalog")
	}
	base := "https://api.github.com/repos/" + repository
	var releases []appGHRelease
	if err := readAppCatalogJSON(ctx, base+"/releases?per_page=100", &releases); err != nil {
		return appReleaseInfo{}, err
	}
	info, err := selectAppCatalogRelease(repository, releases)
	if err != nil {
		return appReleaseInfo{}, err
	}
	var commit appGHCommit
	if err := readAppCatalogJSON(ctx, base+"/commits/"+url.PathEscape(info.Tag), &commit); err != nil {
		// A missing commit is an inconsistent catalog, not an empty one.
		return appReleaseInfo{}, fmt.Errorf("failed to resolve release commit: %v", err)
	}
	info.Commit = strings.ToLower(strings.TrimSpace(commit.SHA))
	if !appCommitPattern.MatchString(info.Commit) {
		return appReleaseInfo{}, errors.New("github commit api returned an invalid commit")
	}
	info.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	return info, nil
}

func selectAppCatalogRelease(repository string, releases []appGHRelease) (appReleaseInfo, error) {
	var selected appReleaseInfo
	for _, release := range releases {
		version := normalizeAppVersion(release.TagName)
		if release.Draft || !release.Immutable || !appCatalogAllowsRelease(repository, version) || !appReleaseTagMatchesVersion(release.TagName, version) {
			continue
		}
		if selected.Version != "" && !isSemverNewer(selected.Version, version) {
			continue
		}
		channel := "stable"
		if release.Prerelease || strings.Contains(version, "beta") || strings.Contains(version, "rc") {
			channel = "beta"
		}
		selected = appReleaseInfo{Version: version, Tag: release.TagName, Channel: channel, ReleasePage: release.HtmlURL, Repository: repository}
	}
	if selected.Version == "" {
		return appReleaseInfo{}, errAppCatalogEmpty
	}
	return selected, nil
}

func readAppCatalogJSON(ctx context.Context, address string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "lightningos-light")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	client := &http.Client{Timeout: 6 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return errAppCatalogEmpty
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("github api returned %s", response.Status)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(destination)
}
