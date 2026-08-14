package appmanifest

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type catalogComposeDocument struct {
	appID string
	raw   string
}

type catalogCompose struct {
	Services map[string]catalogComposeService `yaml:"services"`
}

type catalogComposeService struct {
	Image       string   `yaml:"image"`
	User        string   `yaml:"user"`
	ReadOnly    bool     `yaml:"read_only"`
	CapDrop     []string `yaml:"cap_drop"`
	SecurityOpt []string `yaml:"security_opt"`
	NetworkMode string   `yaml:"network_mode"`
	PID         string   `yaml:"pid"`
	IPC         string   `yaml:"ipc"`
	Privileged  bool     `yaml:"privileged"`
	Devices     []any    `yaml:"devices"`
	Volumes     []string `yaml:"volumes"`
}

type catalogServicePolicy struct {
	nonRoot          bool
	readOnly         bool
	capDropAll       bool
	noNewPrivileges  bool
	allowHostNetwork bool
	exception        string
}

func hardenedCatalogService() catalogServicePolicy {
	return catalogServicePolicy{nonRoot: true, readOnly: true, capDropAll: true, noNewPrivileges: true}
}

// TestComposeCatalogSecurityInventory is intentionally exhaustive. Adding a
// Compose service without classifying it fails this test. Compatibility
// exceptions preserve known-working pre-hardening runtime behavior until that
// app has passed install/start/stop/network/persistence tests in a disposable
// node; an exception is visible policy, not an implicit security claim.
func TestComposeCatalogSecurityInventory(t *testing.T) {
	documents := testCatalogComposeDocuments(t)
	hardened := hardenedCatalogService()
	policies := map[string]catalogServicePolicy{
		CPUMinerID + "/cpuminer": hardened,

		RoboSatsID + "/tor":      {exception: "upstream Tor entrypoint owns its data initialization; preserve the 0.5.2 runtime until lifecycle and persistence tests pass"},
		RoboSatsID + "/robosats": {exception: "upstream client writes application state as root; preserve the known-working 0.5.2 runtime until migration tests pass"},
		RoboSatsID + "/proxy":    {exception: "official Caddy entrypoint initializes writable state; preserve the known-working runtime until migration tests pass"},

		BitcoinCoreID + "/bitcoind": {exception: "the storage guard and official entrypoint start as root to validate and repair external-volume ownership before dropping to UID 101"},

		BTCPayID + "/tor":          {exception: "optional upstream Tor companion retains its established writable-volume bootstrap behavior"},
		BTCPayID + "/btcpay-db":    {exception: "official Postgres entrypoint initializes and repairs the dedicated database volume before dropping privileges"},
		BTCPayID + "/nbxplorer":    {exception: "preserve upstream BTCPay stack runtime semantics until full install, sync, upgrade and persistence validation"},
		BTCPayID + "/btcpayserver": {exception: "preserve upstream BTCPay stack runtime semantics and dedicated restricted LND credential compatibility"},

		LNDgID + "/lndg-db": {noNewPrivileges: true, exception: "official Postgres entrypoint initializes the dedicated LNDg database volume before dropping privileges"},
		LNDgID + "/lndg":    {nonRoot: true, capDropAll: true, noNewPrivileges: true, exception: "LNDg still needs writable application paths during startup; mounts and LND credentials remain narrowly scoped"},

		LNbitsID + "/lnbits":   hardened,
		ElectrsID + "/electrs": hardened,

		MempoolID + "/mempool-web": hardened,
		MempoolID + "/mempool-api": hardened,
		MempoolID + "/mempool-db":  hardened,

		FedimintGuardianID + "/fedimintd": hardened,
		FedimintGatewayID + "/gatewayd":   hardened,

		TapdID + "/tapd": {readOnly: true, capDropAll: true, noNewPrivileges: true, allowHostNetwork: true, exception: "host networking is required for the local LND and LightningOS endpoints; root has no capabilities and only bounded writable paths"},

		PublicPoolID + "/public-pool":    hardened,
		PublicPoolID + "/public-pool-ui": hardened,

		BarkWalletID + "/web":   hardened,
		BarkWalletID + "/api":   hardened,
		BarkWalletID + "/barkd": hardened,
		BarkWalletID + "/proxy": hardened,
	}

	imageExceptions := map[string]string{
		"${CPUMINER_IMAGE}":  "resolved only from the immutable CPU Miner image-variant catalog after strict environment validation",
		BitcoinCoreImage:     "locally built from the authenticated official Bitcoin Core release and broker-attested",
		ElectrsImage:         "locally built from the pinned upstream source commit and broker-attested",
		LNDgImage:            "locally built from the pinned LNDg release and broker-attested",
		RoboSatsImage:        "versioned upstream compatibility release; digest migration requires multi-architecture lifecycle validation",
		RoboSatsTorImage:     "versioned upstream Tor release shared by the established RoboSats and optional BTCPay stacks",
		RoboSatsProxyImage:   "versioned official Caddy release; digest migration requires architecture validation",
		BTCPayServerImage:    "versioned official release refreshed on each requested start by catalog policy",
		BTCPayNbxplorerImage: "versioned official companion release validated with the BTCPay stack",
		BTCPayPostgresImage:  "versioned official database major retained for persistent-volume compatibility",
	}

	seen := make(map[string]bool)
	for _, document := range documents {
		var compose catalogCompose
		if err := yaml.Unmarshal([]byte(document.raw), &compose); err != nil {
			t.Fatalf("%s Compose is invalid: %v", document.appID, err)
		}
		if len(compose.Services) == 0 {
			t.Fatalf("%s Compose has no services", document.appID)
		}
		for serviceName, service := range compose.Services {
			key := document.appID + "/" + serviceName
			policy, ok := policies[key]
			if !ok {
				t.Fatalf("unclassified catalog service %s", key)
			}
			if seen[key] {
				t.Fatalf("catalog service %s appears more than once", key)
			}
			seen[key] = true

			if service.Privileged || service.PID == "host" || service.IPC == "host" || len(service.Devices) != 0 {
				t.Fatalf("%s requests an uncontrolled host privilege", key)
			}
			for _, volume := range service.Volumes {
				if strings.Contains(volume, "/var/run/docker.sock") || strings.Contains(volume, "/run/docker.sock") {
					t.Fatalf("%s mounts the Docker control socket", key)
				}
			}
			if strings.Contains(service.Image, ":latest") {
				t.Fatalf("%s uses mutable latest image %q", key, service.Image)
			}
			if !strings.Contains(service.Image, "@sha256:") && imageExceptions[service.Image] == "" {
				t.Fatalf("%s uses non-digest image %q without an attestation or compatibility policy", key, service.Image)
			}
			if service.NetworkMode == "host" && !policy.allowHostNetwork {
				t.Fatalf("%s unexpectedly uses host networking", key)
			}
			if policy.allowHostNetwork && service.NetworkMode != "host" {
				t.Fatalf("%s lost its documented host-network compatibility requirement", key)
			}
			if policy.nonRoot && (service.User == "" || strings.HasPrefix(service.User, "0:") || service.User == "0") {
				t.Fatalf("%s must run as an explicit non-root user", key)
			}
			if policy.readOnly && !service.ReadOnly {
				t.Fatalf("%s must use a read-only root filesystem", key)
			}
			if policy.capDropAll && !containsCatalogString(service.CapDrop, "ALL") {
				t.Fatalf("%s must drop all Linux capabilities", key)
			}
			if policy.noNewPrivileges && !containsCatalogString(service.SecurityOpt, "no-new-privileges:true") {
				t.Fatalf("%s must set no-new-privileges", key)
			}
			fullyHardened := policy.nonRoot && policy.readOnly && policy.capDropAll && policy.noNewPrivileges && !policy.allowHostNetwork
			if !fullyHardened && strings.TrimSpace(policy.exception) == "" {
				t.Fatalf("%s has a partial runtime policy without a documented compatibility exception", key)
			}
		}
	}
	for key := range policies {
		if !seen[key] {
			t.Fatalf("stale or ungenerated catalog policy %s", key)
		}
	}
}

func containsCatalogString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func testCatalogComposeDocuments(t *testing.T) []catalogComposeDocument {
	t.Helper()
	bitcoin, err := BitcoinCoreCompose("/mnt/bitcoin-ssd/bitcoin", BitcoinCoreExecutionRoot)
	if err != nil {
		t.Fatal(err)
	}
	bark, err := BarkWalletCompose(testBarkWalletComposePaths())
	if err != nil {
		t.Fatal(err)
	}
	electrs, err := ElectrsCompose(ElectrsRuntime{BitcoinMode: ElectrsBitcoinModeApp, Network: "bitcoin"})
	if err != nil {
		t.Fatal(err)
	}
	mempool, err := MempoolCompose(testMempoolRuntime())
	if err != nil {
		t.Fatal(err)
	}
	publicPool, err := PublicPoolCompose(PublicPoolComposePaths{DataDir: "/data/publicpool", CaddyfilePath: "/snapshot/publicpool/Caddyfile"}, PublicPoolBitcoinLocalApp)
	if err != nil {
		t.Fatal(err)
	}
	bitcoinRuntime := FedimintBitcoinRuntime{Mode: FedimintBitcoinModeRemote, URL: "http://bitcoin.example:8332", User: "rpcuser", Pass: "rpcpass"}
	guardian, err := FedimintGuardianCompose(FedimintGuardianRuntime{Bitcoin: bitcoinRuntime})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := FedimintGatewayCompose(FedimintGatewayRuntime{Bitcoin: bitcoinRuntime, GatewayPasswordHash: "$2b$12$abcdefghijklmnopqrstuv123456789012345678901234567890"})
	if err != nil {
		t.Fatal(err)
	}
	btcpayPaths := BTCPayComposePaths{DataDir: "/data/btcpay", NbxDir: "/data/nbxplorer", PgDir: "/data/postgres", DbInitPath: "/snapshot/btcpay/init.sql", LndDir: "/snapshot/btcpay/lnd"}

	return []catalogComposeDocument{
		{appID: CPUMinerID, raw: CPUMinerCompose()},
		{appID: RoboSatsID, raw: RoboSatsCompose("/snapshot/robosats/Caddyfile", "/snapshot/robosats/tls")},
		{appID: BitcoinCoreID, raw: bitcoin},
		{appID: BTCPayID, raw: BTCPayExecutionCompose(btcpayPaths, true, true)},
		{appID: LNDgID, raw: LNDgCompose(LNDgComposePaths{DataDir: "/data/lndg", PgDir: "/data/lndg-postgres", LogPath: "/data/lndg/controller.log", LndDir: "/snapshot/lndg/lnd", ChannelDBPath: "/snapshot/lndg/lnd/channel.db", EntrypointPath: "/snapshot/lndg/entrypoint.sh"})},
		{appID: LNbitsID, raw: LNbitsCompose(LNbitsComposePaths{DataDir: "/data/lnbits", TLSCertPath: "/snapshot/lnbits/tls.cert", MacaroonPath: "/snapshot/lnbits/lnbits.macaroon"})},
		{appID: ElectrsID, raw: electrs},
		{appID: MempoolID, raw: mempool},
		{appID: FedimintGuardianID, raw: guardian},
		{appID: FedimintGatewayID, raw: gateway},
		{appID: TapdID, raw: TapdCompose(TapdComposePaths{DataDir: "/data/tapd", ConfigPath: "/snapshot/tapd/tapd.conf", TLSCertPath: "/snapshot/tapd/tls.cert", MacaroonPath: "/snapshot/tapd/tapd.macaroon"})},
		{appID: PublicPoolID, raw: publicPool},
		{appID: BarkWalletID, raw: bark},
	}
}
