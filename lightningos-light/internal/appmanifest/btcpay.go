package appmanifest

import "fmt"

const (
	BTCPayID               = "btcpay"
	BTCPayProject          = "btcpay"
	BTCPayComposeFile      = "docker-compose.yaml"
	BTCPayEnvFile          = ".env"
	BTCPayDBInitFile       = "init-nbxplorer.sql"
	BTCPayLNDDir           = "lnd"
	BTCPayMacaroonFile     = "btcpay.macaroon"
	BTCPaySnapshotAuthFile = "btcpay.auth"
	BTCPayTLSCertFile      = "tls.cert"
	BTCPayPrimaryService   = "btcpayserver"
	BTCPayPort             = 23000
	BTCPayNbxplorerPort    = 32838
	BTCPayRelease          = "2.4.2"
	BTCPayNbxplorerRelease = "2.6.10"

	// BTCPayServerImage is the newest stable tag published by the official
	// BTCPay Docker project. Upstream does not publish a usable `latest` tag.
	// Every user-requested install/start still refreshes this closed release
	// tag before Compose runs; future releases change only BTCPayRelease.
	BTCPayServerImage    = "btcpayserver/btcpayserver:" + BTCPayRelease
	BTCPayNbxplorerImage = "nicolasdorier/nbxplorer:" + BTCPayNbxplorerRelease
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

type BTCPayComposePaths struct {
	DataDir    string
	NbxDir     string
	PgDir      string
	DbInitPath string
	LndDir     string
}

func BTCPayDBInit() string {
	return `CREATE DATABASE nbxplorer TEMPLATE 'template0' LC_CTYPE 'C' LC_COLLATE 'C' ENCODING 'UTF8';
GRANT ALL PRIVILEGES ON DATABASE nbxplorer TO btcpay;
`
}

func BTCPayLightningConnectionString() string {
	return btcpayLightningConnectionString(BTCPayMacaroonFile)
}

func btcpayLightningConnectionString(authFile string) string {
	return "type=lnd-rest;server=https://host.docker.internal:8080/;macaroonfilepath=/etc/lnd/" + authFile + ";certfilepath=/etc/lnd/" + BTCPayTLSCertFile
}

// BTCPayCompose is the single closed Compose catalog used by both the manager
// and the privileged broker for validating manager-owned input.
func BTCPayCompose(paths BTCPayComposePaths, joinBitcoinNetwork bool, useTorProxy bool) string {
	return btcpayCompose(paths, joinBitcoinNetwork, useTorProxy, BTCPayMacaroonFile)
}

// BTCPayExecutionCompose is the broker-only catalog form. It exposes the same
// dedicated credential under a filename without the extension targeted by the
// BTCPay 2.4.1 credential-disclosure exploit.
func BTCPayExecutionCompose(paths BTCPayComposePaths, joinBitcoinNetwork bool, useTorProxy bool) string {
	return btcpayCompose(paths, joinBitcoinNetwork, useTorProxy, BTCPaySnapshotAuthFile)
}

func btcpayCompose(paths BTCPayComposePaths, joinBitcoinNetwork bool, useTorProxy bool, authFile string) string {
	nbxNetworks := ""
	torService := ""
	torDependency := ""
	torEnvironment := ""
	torVolumes := ""
	networksBlock := fmt.Sprintf(`
networks:
  default:
    name: %s_default
`, BTCPayID)
	if useTorProxy {
		torService = fmt.Sprintf(`  tor:
    image: %s
    container_name: btcpay-tor
    restart: unless-stopped
    volumes:
      - btcpay-tor-data:/var/lib/tor
      - btcpay-tor-log:/var/log/tor

`, BTCPayTorImage)
		torDependency = "      - tor\n"
		torEnvironment = "      NBXPLORER_SOCKSENDPOINT: ${NBXPLORER_SOCKSENDPOINT}\n"
		torVolumes = `volumes:
  btcpay-tor-data:
  btcpay-tor-log:
`
	}
	if joinBitcoinNetwork {
		nbxNetworks = `    networks:
      - default
      - bitcoincore
`
		networksBlock = fmt.Sprintf(`
networks:
  default:
    name: %s_default
  bitcoincore:
    external: true
    name: bitcoincore_default
`, BTCPayID)
	}
	return fmt.Sprintf(`services:
%[14]s
  btcpay-db:
    image: %[1]s
    container_name: btcpay-db
    restart: unless-stopped
    environment:
      POSTGRES_USER: btcpay
      POSTGRES_PASSWORD: ${BTCPAY_DB_PASSWORD}
      POSTGRES_DB: btcpayserver
    expose:
      - "5432"
    volumes:
      - %[2]s:/var/lib/postgresql/data
      - %[3]s:/docker-entrypoint-initdb.d/init-nbxplorer.sql:ro

  nbxplorer:
    image: %[4]s
    container_name: btcpay-nbxplorer
    restart: unless-stopped
    depends_on:
      - btcpay-db
%[15]s    environment:
      NBXPLORER_NETWORK: mainnet
      NBXPLORER_CHAINS: btc
      NBXPLORER_BIND: 0.0.0.0:%[5]d
      NBXPLORER_DATADIR: /datadir
      NBXPLORER_SIGNALFILESDIR: /datadir
      NBXPLORER_BTCRPCURL: ${NBXPLORER_BTCRPCURL}
      NBXPLORER_BTCRPCUSER: ${NBXPLORER_BTCRPCUSER}
      NBXPLORER_BTCRPCPASSWORD: ${NBXPLORER_BTCRPCPASSWORD}
      NBXPLORER_BTCNODEENDPOINT: ${NBXPLORER_BTCNODEENDPOINT}
%[16]s      NBXPLORER_POSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=nbxplorer;MaxPoolSize=20;Database=nbxplorer
    expose:
      - "%[5]d"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - %[6]s:/datadir
%[7]s
  btcpayserver:
    image: %[8]s
    container_name: btcpay-server
    restart: unless-stopped
    depends_on:
      - nbxplorer
    environment:
      BTCPAY_NETWORK: mainnet
      BTCPAY_CHAINS: btc
      BTCPAY_BIND: 0.0.0.0:%[9]d
      BTCPAY_ROOTPATH: /
      BTCPAY_DATADIR: /datadir
      BTCPAY_BTCEXPLORERURL: http://nbxplorer:%[5]d/
      BTCPAY_POSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=btcpayserver;Database=btcpayserver
      BTCPAY_EXPLORERPOSTGRES: User ID=btcpay;Password=${BTCPAY_DB_PASSWORD};Host=btcpay-db;Port=5432;Application Name=btcpayserver;MaxPoolSize=80;Database=nbxplorer
      BTCPAY_BTCLIGHTNING: "%[10]s"
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "%[9]d:%[9]d"
    volumes:
      - %[11]s:/datadir
      - %[6]s:/root/.nbxplorer:ro
      - %[12]s:/etc/lnd:ro
%[17]s%[13]s`,
		BTCPayPostgresImage,
		paths.PgDir,
		paths.DbInitPath,
		BTCPayNbxplorerImage,
		BTCPayNbxplorerPort,
		paths.NbxDir,
		nbxNetworks,
		BTCPayServerImage,
		BTCPayPort,
		btcpayLightningConnectionString(authFile),
		paths.DataDir,
		paths.LndDir,
		networksBlock,
		torService,
		torDependency,
		torEnvironment,
		torVolumes,
	)
}
