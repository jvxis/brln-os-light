package appmanifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPeerSwapCatalogPinsUpdatedBinariesInLegacyPackageDirectory(t *testing.T) {
	if PeerSwapAssetDirectory != "version_5_0" {
		t.Fatalf("the package directory is an intentional compatibility path, got %q", PeerSwapAssetDirectory)
	}
	if PeerSwapVersion != "v6.0.0" || len(PeerSwapCommit) != 40 {
		t.Fatalf("unexpected PeerSwap provenance: %s %s", PeerSwapVersion, PeerSwapCommit)
	}
	if PeerSwapWebVersion != "v6.0.0.1" || len(PeerSwapWebCommit) != 40 {
		t.Fatalf("unexpected PSWeb provenance: %s %s", PeerSwapWebVersion, PeerSwapWebCommit)
	}
	for _, binary := range PeerSwapBinaries() {
		if len(binary.SHA256) != 64 {
			t.Fatalf("invalid %s checksum", binary.Name)
		}
	}
}

func TestPeerSwapServicesUseDedicatedIdentityAndRemoteSourceIsIndependent(t *testing.T) {
	paths := DefaultPeerSwapPaths()
	local := PeerSwapServiceUnit(paths, PeerSwapElementsModeLocal)
	for _, want := range []string{"User=" + PeerSwapUser, "ProtectSystem=strict", "ProtectHome=true", "NoNewPrivileges=true", "ReadWritePaths=" + paths.RuntimeDir, ElementsService + ".service", "--configfile " + paths.ConfigPath, "TimeoutStopSec=15s"} {
		if !strings.Contains(local, want) {
			t.Fatalf("local service missing %q", want)
		}
	}
	if strings.Contains(local, "User=losop") || strings.Contains(local, "SupplementaryGroups=lnd") {
		t.Fatal("PeerSwap must not run as the human operator or inherit the LND group")
	}
	remote := PeerSwapServiceUnit(paths, PeerSwapElementsModeRemote)
	if strings.Contains(remote, ElementsService+".service") {
		t.Fatal("remote PeerSwap must not depend on local Elements")
	}
	web := PeerSwapWebServiceUnit(paths)
	if !strings.Contains(web, "-datadir "+paths.RuntimeDir) || !strings.Contains(web, "User="+PeerSwapUser) {
		t.Fatal("PeerSwap web service must use the dedicated runtime")
	}
}

func TestMergePeerSwapConfigPreservesOperatorOptionsAndForcesManagedValues(t *testing.T) {
	paths := DefaultPeerSwapPaths()
	desired := peerSwapTestConfig(paths, PeerSwapElementsModeLocal)
	existing := "# operator config\nhost=0.0.0.0:42069\nlnd.macaroonpath=/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon\nbitcoinswaps=true\ncustom=one\ncustom=two\n"
	merged, err := MergePeerSwapConfig(existing, desired, PeerSwapElementsModeLocal, paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# operator config", "host=127.0.0.1:42069", "lnd.macaroonpath=" + paths.LNDMacaroonPath, "bitcoinswaps=true", "custom=one", "custom=two"} {
		if !strings.Contains(merged, want) {
			t.Fatalf("merged config missing %q: %s", want, merged)
		}
	}
	if strings.Contains(merged, "admin.macaroon") || strings.Contains(merged, "host=0.0.0.0") {
		t.Fatalf("unsafe managed value survived: %s", merged)
	}
}

func TestValidatePeerSwapSourceSupportsLocalAndRemote(t *testing.T) {
	if err := ValidatePeerSwapSource(PeerSwapElementsModeLocal, "", "", "", "peerswap"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePeerSwapSource(PeerSwapElementsModeRemote, "https://elements.example:7041", "rpc", "secret", "peerswap_node"); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePeerSwapSource(PeerSwapElementsModeLocal, "https://elements.example", "rpc", "secret", "peerswap"); err == nil {
		t.Fatal("local source must reject remote credentials")
	}
}

func TestValidatePeerSwapWebConfigPinsRuntimeAndAllowsExternalElementsVolume(t *testing.T) {
	paths := DefaultPeerSwapPaths()
	raw, err := json.Marshal(map[string]any{
		"DataDir": paths.RuntimeDir, "LightningDir": paths.LNDDir, "Chain": "mainnet",
		"ElementsUser": "rpc", "ElementsPass": "secret", "ElementsWallet": "peerswap_node",
		"ElementsDir": "/media/liquid/elements", "ElementsDirMapped": "/media/liquid/elements",
		"ElementsHost": "https://elements.example", "ElementsPort": "7041", "BitcoinSwaps": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePeerSwapWebConfig(string(raw), paths); err != nil {
		t.Fatalf("valid external Elements volume rejected: %v", err)
	}
	unsafe := strings.Replace(string(raw), paths.LNDDir, "/data/lnd", 1)
	if err := ValidatePeerSwapWebConfig(unsafe, paths); err == nil {
		t.Fatal("arbitrary Lightning directory accepted")
	}
	if err := ValidatePeerSwapWebConfig(string(raw)+"{}", paths); err == nil {
		t.Fatal("trailing JSON document accepted")
	}
}

func peerSwapTestConfig(paths PeerSwapPaths, mode string) string {
	host := "http://127.0.0.1"
	if mode == PeerSwapElementsModeRemote {
		host = "https://elements.example"
	}
	return "host=127.0.0.1:42069\n" +
		"lnd.host=127.0.0.1:10009\n" +
		"lnd.tlscertpath=" + paths.LNDTLSCertPath + "\n" +
		"lnd.macaroonpath=" + paths.LNDMacaroonPath + "\n" +
		"elementsd.rpcuser=rpc\n" +
		"elementsd.rpcpass=secret\n" +
		"elementsd.rpchost=" + host + "\n" +
		"elementsd.rpcport=7041\n" +
		"elementsd.rpcwallet=peerswap\n" +
		"elementsd.datadir=/media/liquid/elements\n" +
		"elementsd.liquidswaps=true\n" +
		"bitcoinswaps=false\n"
}
