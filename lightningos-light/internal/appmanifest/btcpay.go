package appmanifest

import "fmt"

const (
	BTCPayID             = "btcpay"
	BTCPayProject        = "btcpay"
	BTCPayComposeFile    = "docker-compose.yaml"
	BTCPayEnvFile        = ".env"
	BTCPayPrimaryService = "btcpayserver"
	BTCPayPort           = 23000
	BTCPayRelease        = "2.4.1"

	// BTCPayServerImage is the newest stable tag published by the official
	// BTCPay Docker project. Upstream does not publish a usable `latest` tag.
	// Every user-requested install/start still refreshes this closed release
	// tag before Compose runs; future releases change only BTCPayRelease.
	BTCPayServerImage    = "btcpayserver/btcpayserver:" + BTCPayRelease
	BTCPayNbxplorerImage = "nicolasdorier/nbxplorer:2.6.8"
	BTCPayPostgresImage  = "postgres:16"
	BTCPayTorImage       = RoboSatsTorImage

	BTCPayImageServer    AppImageVariant = "server"
	BTCPayImageNbxplorer AppImageVariant = "nbxplorer"
	BTCPayImagePostgres  AppImageVariant = "postgres"
	BTCPayImageTor       AppImageVariant = "tor"
)

var btcpayImages = map[AppImageVariant]string{
	BTCPayImageServer:    BTCPayServerImage,
	BTCPayImageNbxplorer: BTCPayNbxplorerImage,
	BTCPayImagePostgres:  BTCPayPostgresImage,
	BTCPayImageTor:       BTCPayTorImage,
}

func BTCPayImageVariants(useTor bool) []AppImageVariant {
	variants := []AppImageVariant{BTCPayImageServer, BTCPayImageNbxplorer, BTCPayImagePostgres}
	if useTor {
		variants = append(variants, BTCPayImageTor)
	}
	return variants
}

func BTCPayImageForVariant(variant AppImageVariant) (string, error) {
	image, ok := btcpayImages[variant]
	if !ok {
		return "", fmt.Errorf("btcpay image variant is not allowed")
	}
	return image, nil
}
