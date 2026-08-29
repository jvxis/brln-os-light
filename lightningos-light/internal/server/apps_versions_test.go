package server

import (
	"context"
	"errors"
	"testing"

	"lightningos-light/internal/appmanifest"
)

type fakeAppVersionRepository struct {
	versions    map[string]appVersionRecord
	marked      []string
	markApplied error
}

func (f *fakeAppVersionRepository) List(context.Context) (map[string]appVersionRecord, error) {
	return f.versions, nil
}

func (f *fakeAppVersionRepository) MarkApplied(_ context.Context, appID string) error {
	f.marked = append(f.marked, appID)
	return f.markApplied
}

func TestThirdPartyAppCatalogVersionsScope(t *testing.T) {
	catalog := thirdPartyAppCatalogVersions()
	for _, appID := range []string{
		appmanifest.BitcoinCoreID,
		appmanifest.BarkWalletID,
		appmanifest.ElectrsID,
		appmanifest.MempoolID,
		appmanifest.FedimintGuardianID,
		appmanifest.FedimintGatewayID,
		appmanifest.LNDgID,
		appmanifest.LNbitsID,
		appmanifest.BTCPayID,
		appmanifest.ElementsID,
		appmanifest.RoboSatsID,
		appmanifest.PublicPoolID,
		appmanifest.TapdID,
		appmanifest.LoopID,
	} {
		if catalog[appID] == "" {
			t.Fatalf("versioned third-party app %q has no catalog version", appID)
		}
	}
	for _, appID := range []string{
		appmanifest.PeerSwapID,
		appmanifest.CPUMinerID,
		"depix-buy",
		"fswap",
		"loopout-brln",
		"magma",
	} {
		if _, ok := catalog[appID]; ok {
			t.Fatalf("excluded app %q is present in the version catalog", appID)
		}
	}
}

func TestCatalogImageVersionTracksPinnedProductTag(t *testing.T) {
	if got := catalogImageVersion(appmanifest.BarkWalletWebImage); got != "0.7.2" {
		t.Fatalf("unexpected Bark Wallet catalog version: %q", got)
	}
	if got := catalogImageVersion(appmanifest.RoboSatsImage); got != "v0.8.4-alpha" {
		t.Fatalf("unexpected RoboSats catalog version: %q", got)
	}
	if got := catalogImageVersion("registry.example:5000/app"); got != "" {
		t.Fatalf("image without a tag produced version %q", got)
	}
}

func TestDecorateAppVersionsDistinguishesAvailableAppliedAndUnknown(t *testing.T) {
	apps := []appInfo{
		{ID: appmanifest.MempoolID, Installed: true},
		{ID: appmanifest.ElectrsID, Installed: true},
		{ID: appmanifest.BTCPayID, Installed: false},
		{ID: appmanifest.PeerSwapID, Installed: true},
	}
	versions := map[string]appVersionRecord{
		appmanifest.MempoolID:  {CatalogVersion: "3.4.0", AppliedVersion: "3.3.1"},
		appmanifest.ElectrsID:  {CatalogVersion: "0.11.1"},
		appmanifest.BTCPayID:   {CatalogVersion: "2.4.2", AppliedVersion: "2.3.0"},
		appmanifest.PeerSwapID: {CatalogVersion: "should-not-appear", AppliedVersion: "should-not-appear"},
	}

	decorateAppVersions(apps, versions)

	if apps[0].AvailableVersion != "3.4.0" || apps[0].InstalledVersion != "3.3.1" || !apps[0].UpdateAvailable {
		t.Fatalf("unexpected Mempool version metadata: %+v", apps[0])
	}
	if apps[1].AvailableVersion != "0.11.1" || apps[1].InstalledVersion != "" || apps[1].UpdateAvailable {
		t.Fatalf("legacy Electrs install must remain unknown: %+v", apps[1])
	}
	if apps[2].AvailableVersion != "2.4.2" || apps[2].InstalledVersion != "" || apps[2].UpdateAvailable {
		t.Fatalf("not-installed BTCPay must expose only the catalog version: %+v", apps[2])
	}
	if apps[3].AvailableVersion != "" || apps[3].InstalledVersion != "" || apps[3].UpdateAvailable {
		t.Fatalf("PeerSwap must not expose version metadata: %+v", apps[3])
	}
}

func TestRecordAppliedAppVersionOnlyTouchesVersionedThirdPartyApps(t *testing.T) {
	repository := &fakeAppVersionRepository{}
	server := &Server{appVersions: repository}

	server.recordAppliedAppVersion(context.Background(), appmanifest.PeerSwapID)
	server.recordAppliedAppVersion(context.Background(), appmanifest.MempoolID)

	if len(repository.marked) != 1 || repository.marked[0] != appmanifest.MempoolID {
		t.Fatalf("unexpected applied-version writes: %v", repository.marked)
	}
}

func TestRecordAppliedAppVersionDoesNotPropagateMetadataFailure(t *testing.T) {
	repository := &fakeAppVersionRepository{markApplied: errors.New("postgres unavailable")}
	server := &Server{appVersions: repository}

	server.recordAppliedAppVersion(context.Background(), appmanifest.MempoolID)

	if len(repository.marked) != 1 {
		t.Fatalf("expected one best-effort metadata write, got %v", repository.marked)
	}
}
