package appmanifest

import "errors"

const (
	LNbitsID                             = "lnbits"
	LNbitsRelease                        = "1.5.6"
	LNbitsManifestSHA256                 = "6e37fbf9b847c066d7e022e19a018b3df7f12602a370f117857165d36bfb165b"
	LNbitsImage                          = "lnbits/lnbits:v" + LNbitsRelease + "@sha256:" + LNbitsManifestSHA256
	LNbitsMacaroonFile                   = "lnbits.macaroon"
	LNbitsTLSCertFile                    = "tls.cert"
	LNbitsImageApp       AppImageVariant = "app"
)

// LNbitsImageForVariant selects only the stable official LNbits image pinned
// by its registry manifest digest. The digest is multi-architecture, so Docker
// still selects the correct linux/amd64 or linux/arm64 child manifest without
// trusting a mutable tag.
func LNbitsImageForVariant(variant AppImageVariant) (string, error) {
	if variant != LNbitsImageApp {
		return "", errors.New("lnbits image variant is not allowed")
	}
	return LNbitsImage, nil
}
