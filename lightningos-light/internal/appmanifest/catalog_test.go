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
