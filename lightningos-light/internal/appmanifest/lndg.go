package appmanifest

import (
	"errors"
	"fmt"
)

const (
	LNDgID            = "lndg"
	LNDgRelease       = "1.11.0"
	LNDgSourceCommit  = "0fe400029240fc59431b56b6ce47e24b764396b1"
	LNDgSourceSHA256  = "390789734f729608cdc54f3b356e26f98e6184570f4677ba4df273980eea5df4"
	LNDgSourceURL     = "https://codeload.github.com/cryptosharks131/lndg/tar.gz/" + LNDgSourceCommit
	LNDgSourceDir     = "lndg-" + LNDgSourceCommit
	LNDgBaseImage     = "python:3.12-slim@sha256:229a2c5bfa27522db7815ea81f9bed70af17ccb9de9fc7ad142b1877b5830d36"
	LNDgImage         = "lightningos/lndg:" + LNDgRelease
	LNDgSupervisor    = "4.3.0"
	LNDgWhitenoise    = "6.12.0"
	LNDgPsycopgBinary = "2.9.12"

	LNDgImageApp AppImageVariant = "app"
)

func LNDgImageForVariant(variant AppImageVariant) (string, error) {
	if variant != LNDgImageApp {
		return "", errors.New("lndg image variant is not allowed")
	}
	return LNDgImage, nil
}

// LNDgDockerfile builds the LightningOS-owned image from the exact upstream
// release source. Upstream publishes no container image or signed release
// artifact, so the broker verifies the closed source archive digest before
// this Dockerfile ever receives the build context.
func LNDgDockerfile() string {
	return fmt.Sprintf(`FROM %s
ENV PYTHONUNBUFFERED=1
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libpq-dev postgresql-client \
    && rm -rf /var/lib/apt/lists/*
COPY %s/ /app/
WORKDIR /app
RUN python -m pip install --no-cache-dir -r requirements.txt \
    && python -m pip install --no-cache-dir supervisor==%s whitenoise==%s psycopg2-binary==%s
`, LNDgBaseImage, LNDgSourceDir, LNDgSupervisor, LNDgWhitenoise, LNDgPsycopgBinary)
}
