package appmanifest

import "testing"

func TestComposeManifestCatalogIsClosed(t *testing.T) {
	for _, test := range []struct {
		id      string
		project string
		service string
	}{
		{id: CPUMinerID, project: CPUMinerProject, service: CPUMinerID},
		{id: RoboSatsID, project: RoboSatsProject, service: RoboSatsPrimaryService},
	} {
		manifest, err := ComposeManifestForApp(test.id)
		if err != nil || manifest.ID != test.id || manifest.Project != test.project || manifest.PrimaryService != test.service || manifest.StopTimeoutSeconds != 2 {
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
