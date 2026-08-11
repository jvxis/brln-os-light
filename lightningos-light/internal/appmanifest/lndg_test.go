package appmanifest

import (
	"strings"
	"testing"
)

func TestLNDgImageCatalogIsClosed(t *testing.T) {
	image, err := LNDgImageForVariant(LNDgImageApp)
	if err != nil || image != LNDgImage {
		t.Fatalf("image/error=%q/%v", image, err)
	}
	if _, err := LNDgImageForVariant("latest"); err == nil {
		t.Fatal("expected an unknown LNDg image variant to be rejected")
	}
	if !strings.Contains(LNDgBaseImage, "@sha256:") || strings.HasSuffix(LNDgImage, ":latest") {
		t.Fatalf("unpinned LNDg image catalog: base=%q image=%q", LNDgBaseImage, LNDgImage)
	}
}

func TestLNDgDockerfileUsesOnlyVerifiedSourceContext(t *testing.T) {
	dockerfile := LNDgDockerfile()
	for _, required := range []string{
		"FROM " + LNDgBaseImage,
		"COPY " + LNDgSourceDir + "/ /app/",
		"supervisor==" + LNDgSupervisor,
		"whitenoise==" + LNDgWhitenoise,
		"psycopg2-binary==" + LNDgPsycopgBinary,
	} {
		if !strings.Contains(dockerfile, required) {
			t.Fatalf("Dockerfile missing %q", required)
		}
	}
	for _, forbidden := range []string{"git clone", "git fetch", "master", ":latest"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Fatalf("Dockerfile contains mutable source selector %q", forbidden)
		}
	}
}
