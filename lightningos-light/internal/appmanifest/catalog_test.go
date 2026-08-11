package appmanifest

import (
	"strings"
	"testing"
)

func TestComposeManifestCatalogIsClosed(t *testing.T) {
	for _, test := range []struct {
		id      string
		project string
		service string
		timeout int
	}{
		{id: CPUMinerID, project: CPUMinerProject, service: CPUMinerID, timeout: 2},
		{id: RoboSatsID, project: RoboSatsProject, service: RoboSatsPrimaryService, timeout: 2},
		{id: BitcoinCoreID, project: BitcoinCoreProject, service: BitcoinCorePrimaryService, timeout: BitcoinCoreStopTimeout},
		{id: BTCPayID, project: BTCPayProject, service: BTCPayPrimaryService, timeout: BTCPayStopTimeout},
	} {
		manifest, err := ComposeManifestForApp(test.id)
		if err != nil || manifest.ID != test.id || manifest.Project != test.project || manifest.PrimaryService != test.service || manifest.StopTimeoutSeconds != test.timeout {
			t.Fatalf("manifest/error=%#v/%v", manifest, err)
		}
	}
	for _, id := range []string{"", "publicpool", "robosats;reboot", "../robosats"} {
		if _, err := ComposeManifestForApp(id); err == nil {
			t.Fatalf("expected %q to be rejected", id)
		}
	}
}

func TestRoboSatsImagesReturnsIndependentClosedList(t *testing.T) {
	images := RoboSatsImages()
	if len(images) != 3 || images[0] != RoboSatsImage || images[1] != RoboSatsTorImage || images[2] != RoboSatsProxyImage {
		t.Fatalf("unexpected images: %#v", images)
	}
	images[0] = "evil/root:latest"
	if RoboSatsImages()[0] != RoboSatsImage {
		t.Fatal("caller mutated the catalog image list")
	}
}

func TestCatalogImageVariantsAreClosedByApp(t *testing.T) {
	for _, test := range []struct {
		appID   string
		variant AppImageVariant
		image   string
	}{
		{appID: CPUMinerID, variant: CPUMinerImageBaseline, image: "jvx1971/cpu-lottery-miner:v1"},
		{appID: RoboSatsID, variant: RoboSatsImageClient, image: RoboSatsImage},
		{appID: RoboSatsID, variant: RoboSatsImageTor, image: RoboSatsTorImage},
		{appID: RoboSatsID, variant: RoboSatsImageProxy, image: RoboSatsProxyImage},
		{appID: BitcoinCoreID, variant: BitcoinCoreImageNode, image: BitcoinCoreImage},
		{appID: BTCPayID, variant: BTCPayImageServer, image: BTCPayServerImage},
		{appID: BTCPayID, variant: BTCPayImageNbxplorer, image: BTCPayNbxplorerImage},
		{appID: BTCPayID, variant: BTCPayImagePostgres, image: BTCPayPostgresImage},
		{appID: BTCPayID, variant: BTCPayImageTor, image: BTCPayTorImage},
		{appID: LNDgID, variant: LNDgImageApp, image: LNDgImage},
		{appID: LNbitsID, variant: LNbitsImageApp, image: LNbitsImage},
	} {
		image, err := CatalogImageForVariant(test.appID, test.variant)
		if err != nil || image != test.image {
			t.Fatalf("image/error for %s/%s = %q/%v", test.appID, test.variant, image, err)
		}
	}
	for _, test := range []struct {
		appID   string
		variant AppImageVariant
	}{
		{appID: RoboSatsID, variant: CPUMinerImageBaseline},
		{appID: CPUMinerID, variant: RoboSatsImageClient},
		{appID: RoboSatsID, variant: "latest;reboot"},
		{appID: "mempool", variant: "client"},
		{appID: BitcoinCoreID, variant: "latest"},
		{appID: BTCPayID, variant: "latest;reboot"},
		{appID: LNbitsID, variant: "latest"},
	} {
		if _, err := CatalogImageForVariant(test.appID, test.variant); err == nil {
			t.Fatalf("expected %s/%s to be rejected", test.appID, test.variant)
		}
	}
	variants := RoboSatsImageVariants()
	variants[0] = "evil"
	if RoboSatsImageVariants()[0] != RoboSatsImageClient {
		t.Fatal("caller mutated the RoboSats image variant list")
	}
	btcpayVariants := BTCPayImageVariants(true)
	if len(btcpayVariants) != 4 || btcpayVariants[0] != BTCPayImageServer || btcpayVariants[3] != BTCPayImageTor {
		t.Fatalf("unexpected BTCPay variants: %#v", btcpayVariants)
	}
	btcpayVariants[0] = "evil"
	if BTCPayImageVariants(false)[0] != BTCPayImageServer || len(BTCPayImageVariants(false)) != 3 {
		t.Fatal("caller mutated the BTCPay image variants or Tor was not optional")
	}
}

func TestLNbitsCatalogPinsOfficialStableManifest(t *testing.T) {
	if LNbitsRelease != "1.5.6" {
		t.Fatalf("unexpected LNbits release: %q", LNbitsRelease)
	}
	if LNbitsImage != "lnbits/lnbits:v1.5.6@sha256:"+LNbitsManifestSHA256 {
		t.Fatalf("LNbits image is not pinned to the catalog digest: %q", LNbitsImage)
	}
	if len(LNbitsManifestSHA256) != 64 || strings.Contains(LNbitsImage, ":latest") {
		t.Fatalf("LNbits image has an invalid or mutable selector: %q", LNbitsImage)
	}
}

// 2.4.2 closes the actively exploited LND macaroon disclosure fixed by
// upstream on 2026-08-07. NBXplorer 2.6.10 is the matching release upstream
// explicitly recommends to integrators. Keep this guard when advancing the
// catalog so the security floor cannot regress silently.
func TestBTCPayCatalogSecurityFloor(t *testing.T) {
	if BTCPayRelease != "2.4.2" || BTCPayServerImage != "btcpayserver/btcpayserver:2.4.2" {
		t.Fatalf("BTCPay catalog fell below the 2.4.2 security floor: %q", BTCPayServerImage)
	}
	if BTCPayNbxplorerRelease != "2.6.10" || BTCPayNbxplorerImage != "nicolasdorier/nbxplorer:2.6.10" {
		t.Fatalf("unexpected NBXplorer security companion: %q", BTCPayNbxplorerImage)
	}
}

func TestBTCPayExecutionCatalogHidesMacaroonExtension(t *testing.T) {
	paths := BTCPayComposePaths{
		DataDir:    "/data/btcpay",
		NbxDir:     "/data/nbxplorer",
		PgDir:      "/data/postgres",
		DbInitPath: "/manager/init-nbxplorer.sql",
		LndDir:     "/manager/lnd",
	}
	managerCompose := BTCPayCompose(paths, false, false)
	executionCompose := BTCPayExecutionCompose(paths, false, false)
	if !strings.Contains(managerCompose, "macaroonfilepath=/etc/lnd/"+BTCPayMacaroonFile) {
		t.Fatal("manager catalog lost the dedicated BTCPay macaroon")
	}
	if !strings.Contains(executionCompose, "macaroonfilepath=/etc/lnd/"+BTCPaySnapshotAuthFile) {
		t.Fatal("execution catalog lost the extensionless BTCPay credential")
	}
	if strings.Contains(executionCompose, ".macaroon") || strings.Contains(executionCompose, "admin.macaroon") {
		t.Fatal("execution catalog exposes a forbidden macaroon filename")
	}
}

func TestCatalogImageRefreshPolicyIsClosed(t *testing.T) {
	refresh, err := CatalogImageRequiresRefresh(BTCPayID, BTCPayImageServer)
	if err != nil || !refresh {
		t.Fatalf("BTCPay server release must refresh: refresh=%v err=%v", refresh, err)
	}
	for _, test := range []struct {
		appID   string
		variant AppImageVariant
	}{
		{BTCPayID, BTCPayImageNbxplorer},
		{BTCPayID, BTCPayImagePostgres},
		{RoboSatsID, RoboSatsImageClient},
		{BitcoinCoreID, BitcoinCoreImageNode},
	} {
		refresh, err = CatalogImageRequiresRefresh(test.appID, test.variant)
		if err != nil || refresh {
			t.Fatalf("unexpected refresh policy for %s/%s: %v/%v", test.appID, test.variant, refresh, err)
		}
	}
	if _, err := CatalogImageRequiresRefresh(BTCPayID, "server;reboot"); err == nil {
		t.Fatal("untrusted refresh variant was accepted")
	}
}

func TestCatalogExternalTCPPortIsClosedByApp(t *testing.T) {
	port, err := CatalogExternalTCPPort(RoboSatsID)
	if err != nil || port != RoboSatsPort {
		t.Fatalf("port/error = %d/%v", port, err)
	}
	for _, appID := range []string{"", CPUMinerID, "robosats;reboot"} {
		if _, err := CatalogExternalTCPPort(appID); err == nil {
			t.Fatalf("expected %q to be rejected", appID)
		}
	}
}
