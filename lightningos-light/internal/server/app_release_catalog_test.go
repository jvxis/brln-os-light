package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"lightningos-light/internal/privileged"
)

func TestCatalogUpgradeHelperFitsBrokerRequest(t *testing.T) {
	params, err := json.Marshal(privileged.LightningOSUpgradeStartParams{
		Version: "0.5.41-beta", Tag: "0.5.41-Beta", Commit: strings.Repeat("a", 40),
		HelperContent: strings.ReplaceAll(embeddedAppUpgradeScript, "\r\n", "\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(privileged.Request{
		Version: privileged.ProtocolVersion, RequestID: "catalog_upgrade_test",
		Operation: privileged.OperationLightningOSUpgradeStart, Params: params,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privileged.DecodeRequest(bytes.NewReader(wire)); err != nil {
		t.Fatalf("actual embedded helper cannot traverse broker protocol: %v", err)
	}
}

func TestAppCatalogMandatoryBridgeSelection(t *testing.T) {
	for _, test := range []struct{ current, catalog string }{
		{"0.5.33-Beta", appLegacyCatalog}, {"0.5.39-beta", appLegacyCatalog},
		{"0.5.40-Beta", appModernCatalog}, {"0.5.41-beta", appModernCatalog},
		{"0.6.0", appModernCatalog}, {"1.0.0", appModernCatalog}, {"unknown", appLegacyCatalog},
	} {
		if got := appCatalogForVersion(test.current); got != test.catalog {
			t.Errorf("%s: catalog = %s, want %s", test.current, got, test.catalog)
		}
	}
	releases := []appGHRelease{
		{TagName: "0.5.41-Beta", Immutable: true},
		{TagName: "0.5.39-Beta", Immutable: true},
		{TagName: "0.5.40-Beta", Immutable: true},
	}
	legacy, err := selectAppCatalogRelease(appLegacyCatalog, releases)
	if err != nil || legacy.Version != "0.5.40-beta" {
		t.Fatalf("old installation must receive the bridge: %+v, %v", legacy, err)
	}
	modern, err := selectAppCatalogRelease(appModernCatalog, releases)
	if err != nil || modern.Version != "0.5.41-beta" {
		t.Fatalf("bridge installation must receive later releases: %+v, %v", modern, err)
	}
}

func TestAppCatalogRejectsUnpublishedMutableAndWrongCatalogReleases(t *testing.T) {
	releases := []appGHRelease{
		{TagName: "0.5.43-Beta", Immutable: true, Draft: true},
		{TagName: "0.5.42-Beta"},
		{TagName: "release/0.5.41-Beta", Immutable: true},
		{TagName: "0.5.40-Beta", Immutable: true},
	}
	if _, err := selectAppCatalogRelease(appModernCatalog, releases); !errors.Is(err, errAppCatalogEmpty) {
		t.Fatalf("unusable releases should not be offered: %v", err)
	}
	if _, err := selectAppCatalogRelease("another/repository", releases); !errors.Is(err, errAppCatalogEmpty) {
		t.Fatalf("unknown catalog must not be accepted: %v", err)
	}
}

type appCatalogTransport func(*http.Request) (*http.Response, error)

func (f appCatalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAppCatalogRequestsRespectInstalledVersionAndFallback(t *testing.T) {
	for _, test := range []struct {
		name, current, wantVersion, wantRepository string
		modernStatus                               int
	}{
		{"old-never-queries-modern", "0.5.33-Beta", "0.5.40-beta", appLegacyCatalog, 200},
		{"bridge-queries-modern", "0.5.40-Beta", "0.5.41-beta", appModernCatalog, 200},
		{"not-created-yet", "0.5.40-Beta", "0.5.40-beta", appLegacyCatalog, 404},
		{"server-error-not-hidden", "0.5.40-Beta", "", "", 503},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = previous })
			var requested []string
			http.DefaultTransport = appCatalogTransport(func(r *http.Request) (*http.Response, error) {
				requested = append(requested, r.URL.Path)
				status, body := 200, `{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
				modern := strings.Contains(r.URL.Path, appModernCatalog)
				if strings.HasSuffix(r.URL.Path, "/releases") {
					body = `[{"tag_name":"0.5.40-Beta","immutable":true}]`
					if modern {
						status = test.modernStatus
						body = `[{"tag_name":"0.5.41-Beta","immutable":true}]`
					}
				}
				return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})
			info, err := fetchAppReleaseForVersion(context.Background(), test.current)
			if test.wantVersion == "" {
				if err == nil || len(requested) != 1 {
					t.Fatalf("server failure must not fall back: %+v, %v, %v", info, err, requested)
				}
				return
			}
			if err != nil || info.Version != test.wantVersion || info.Repository != test.wantRepository {
				t.Fatalf("unexpected resolution: %+v, %v", info, err)
			}
			if test.current == "0.5.33-Beta" {
				for _, path := range requested {
					if strings.Contains(path, appModernCatalog) {
						t.Fatal("old client queried modern releases")
					}
				}
			}
			if !strings.Contains(requested[len(requested)-1], test.wantRepository+"/commits/") {
				t.Fatal("commit must be resolved in the same immutable release repository")
			}
		})
	}
}
