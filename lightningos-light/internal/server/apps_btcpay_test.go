package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBtcpayDefinition(t *testing.T) {
	def := btcpayDefinition()
	if def.ID != "btcpay" {
		t.Fatalf("unexpected app id: %s", def.ID)
	}
	if def.Port != btcpayPort {
		t.Fatalf("unexpected port: %d", def.Port)
	}
}

func TestBuildBtcpayRemoteWiring(t *testing.T) {
	onion := strings.Repeat("a", 56) + ".onion"
	wiring, err := buildBtcpayRemoteWiring(
		"bitcoin.br-ln.com:8085",
		"user",
		"pass",
		[]bitcoinNetworkLocalAddress{{Address: onion, Port: 8333}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wiring.Source != "remote" {
		t.Fatalf("unexpected source: %s", wiring.Source)
	}
	if wiring.RPCURL != "http://bitcoin.br-ln.com:8085/" {
		t.Fatalf("unexpected rpc url: %s", wiring.RPCURL)
	}
	if wiring.NodeEndpoint != onion+":8333" {
		t.Fatalf("unexpected node endpoint: %s", wiring.NodeEndpoint)
	}
	if wiring.ProbeP2P != "" {
		t.Fatalf("onion P2P must not be probed without Tor: %s", wiring.ProbeP2P)
	}
	if !wiring.UseTorProxy {
		t.Fatal("remote onion wiring must enable the Tor proxy")
	}
	if wiring.JoinBitcoinNetwork {
		t.Fatal("remote wiring must not join the bitcoincore network")
	}
}

func TestBuildBtcpayRemoteWiringHTTPS(t *testing.T) {
	wiring, err := buildBtcpayRemoteWiring("https://bitcoin.example.com:8332", "user", "pass", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wiring.RPCURL != "https://bitcoin.example.com:8332/" {
		t.Fatalf("expected https scheme preserved, got: %s", wiring.RPCURL)
	}
	if wiring.NodeEndpoint != "bitcoin.example.com:8333" || wiring.ProbeP2P != "bitcoin.example.com:8333" {
		t.Fatalf("expected clearnet P2P fallback, got node=%s probe=%s", wiring.NodeEndpoint, wiring.ProbeP2P)
	}
	if wiring.UseTorProxy {
		t.Fatal("clearnet fallback must not enable Tor")
	}
}

func TestBuildBtcpayRemoteWiringMissingCredentials(t *testing.T) {
	if _, err := buildBtcpayRemoteWiring("bitcoin.br-ln.com:8085", "", "", nil); err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if _, err := buildBtcpayRemoteWiring("", "user", "pass", nil); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestSelectBtcpayOnionEndpointRejectsInvalidAddresses(t *testing.T) {
	addresses := []bitcoinNetworkLocalAddress{
		{Address: "short.onion", Port: 8333},
		{Address: strings.Repeat("a", 56) + ".onion", Port: 0},
		{Address: strings.Repeat("1", 56) + ".onion", Port: 8333},
		{Address: "203.0.113.10", Port: 8333},
	}
	if endpoint, ok := selectBtcpayOnionEndpoint(addresses); ok {
		t.Fatalf("invalid onion address selected: %s", endpoint)
	}
}

func TestBtcpayLightningConnectionString(t *testing.T) {
	conn := btcpayLightningConnectionString()
	for _, required := range []string{
		"type=lnd-rest",
		"server=https://host.docker.internal:8080/",
		"macaroonfilepath=/etc/lnd/btcpay.macaroon",
		"certfilepath=/etc/lnd/tls.cert",
	} {
		if !strings.Contains(conn, required) {
			t.Fatalf("connection string missing %q: %s", required, conn)
		}
	}
	if strings.Contains(conn, "allowinsecure") {
		t.Fatalf("connection string must not allow insecure transport: %s", conn)
	}
	if strings.Contains(conn, "admin.macaroon") {
		t.Fatalf("connection string must not reference the admin macaroon: %s", conn)
	}
}

func TestBtcpayMacaroonPermissions(t *testing.T) {
	perms := btcpayMacaroonPermissions()
	expected := map[string]bool{
		"address:read":   true,
		"address:write":  true,
		"info:read":      true,
		"invoices:read":  true,
		"invoices:write": true,
		"onchain:read":   true,
	}
	if len(perms) != len(expected) {
		t.Fatalf("unexpected permission count: %d", len(perms))
	}
	for _, perm := range perms {
		key := perm.Entity + ":" + perm.Action
		if !expected[key] {
			t.Fatalf("unexpected permission: %s", key)
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		t.Fatalf("missing permissions: %v", expected)
	}
}

func TestBtcpayComposeContentsRemoteSource(t *testing.T) {
	paths := btcpayAppPaths()
	wiring := btcpayBitcoinWiring{Source: "remote", UseTorProxy: true}
	compose := btcpayComposeContents(paths, wiring)
	assertValidBtcpayComposeYAML(t, compose)

	for _, required := range []string{
		btcpayImage,
		btcpayNbxplorerImage,
		btcpayPostgresImage,
		btcpayTorImage,
		"NBXPLORER_NETWORK: mainnet",
		"NBXPLORER_CHAINS: btc",
		"NBXPLORER_BTCRPCURL: ${NBXPLORER_BTCRPCURL}",
		"NBXPLORER_BTCNODEENDPOINT: ${NBXPLORER_BTCNODEENDPOINT}",
		"NBXPLORER_SOCKSENDPOINT: ${NBXPLORER_SOCKSENDPOINT}",
		"container_name: btcpay-tor",
		"- tor",
		"BTCPAY_NETWORK: mainnet",
		"BTCPAY_CHAINS: btc",
		"BTCPAY_BTCEXPLORERURL: http://nbxplorer:32838/",
		"BTCPAY_EXPLORERPOSTGRES",
		"\"23000:23000\"",
		"/root/.nbxplorer:ro",
		"/etc/lnd:ro",
		"init-nbxplorer.sql",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose missing %q", required)
		}
	}
	if strings.Contains(compose, "bitcoincore_default") {
		t.Fatal("remote compose must not join bitcoincore network")
	}
	// NBXplorer and postgres must never be published on the host.
	if strings.Contains(compose, "32838:32838") || strings.Contains(compose, "5432:5432") {
		t.Fatal("internal service ports must not be published")
	}
	if strings.Contains(compose, "ZMQ") || strings.Contains(compose, "zmqpub") {
		t.Fatal("BTCPay stack must not require ZMQ")
	}
	if strings.Contains(compose, "9050:9050") {
		t.Fatal("the BTCPay Tor SOCKS port must remain internal")
	}
}

func TestBtcpayComposeContentsAppSource(t *testing.T) {
	paths := btcpayAppPaths()
	wiring := btcpayBitcoinWiring{Source: "app", JoinBitcoinNetwork: true}
	compose := btcpayComposeContents(paths, wiring)
	assertValidBtcpayComposeYAML(t, compose)

	for _, required := range []string{
		"bitcoincore_default",
		"external: true",
		"- bitcoincore",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("app-source compose missing %q", required)
		}
	}
	if strings.Contains(compose, "btcpay-tor") || strings.Contains(compose, "NBXPLORER_SOCKSENDPOINT") {
		t.Fatal("local Bitcoin source must not start the BTCPay Tor proxy")
	}
}

func assertValidBtcpayComposeYAML(t *testing.T, compose string) {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(compose), &parsed); err != nil {
		t.Fatalf("invalid generated compose YAML: %v\n%s", err, compose)
	}
	if _, ok := parsed["services"]; !ok {
		t.Fatal("generated compose YAML has no services section")
	}
}

func TestEnsureBtcpayEnvIncludesTorSocksEndpoint(t *testing.T) {
	paths := btcpayPaths{EnvPath: t.TempDir() + "/.env"}
	wiring := btcpayBitcoinWiring{
		RPCURL:       "http://bitcoin.example:8085/",
		RPCUser:      "user",
		RPCPass:      "pass",
		NodeEndpoint: strings.Repeat("a", 56) + ".onion:8333",
		UseTorProxy:  true,
	}
	if err := ensureBtcpayEnv(paths, wiring, "db-pass"); err != nil {
		t.Fatalf("ensure env: %v", err)
	}
	raw, err := os.ReadFile(paths.EnvPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if !strings.Contains(string(raw), "NBXPLORER_SOCKSENDPOINT="+btcpayTorSOCKSEndpoint) {
		t.Fatalf("Tor SOCKS endpoint missing from env: %s", raw)
	}
}

func TestBtcpayDbInitContents(t *testing.T) {
	sql := btcpayDbInitContents()
	if !strings.Contains(sql, "CREATE DATABASE nbxplorer") {
		t.Fatalf("init sql missing nbxplorer database: %s", sql)
	}
	if !strings.Contains(sql, "LC_CTYPE 'C'") {
		t.Fatalf("init sql missing C locale: %s", sql)
	}
}

func TestEnsureBtcpayNbxplorerDatabaseCreatesMissingDatabase(t *testing.T) {
	var commands [][]string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		command := append([]string{name}, args...)
		commands = append(commands, command)
		switch {
		case len(args) > 2 && args[2] == "pg_isready":
			return "btcpay-db:5432 - accepting connections\n", nil
		case len(args) > 2 && args[2] == "psql":
			return "", nil
		case len(args) > 2 && args[2] == "createdb":
			return "", nil
		default:
			return "", errors.New("unexpected command")
		}
	}

	if err := ensureBtcpayNbxplorerDatabase(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commands) != 3 {
		t.Fatalf("expected readiness, lookup, and create commands; got %d: %v", len(commands), commands)
	}
	createdb := strings.Join(commands[2], " ")
	for _, required := range []string{"--owner=btcpay", "--template=template0", "--encoding=UTF8", "--lc-collate=C", "--lc-ctype=C", "nbxplorer"} {
		if !strings.Contains(createdb, required) {
			t.Fatalf("createdb command missing %q: %s", required, createdb)
		}
	}
}

func TestEnsureBtcpayNbxplorerDatabaseKeepsExistingDatabase(t *testing.T) {
	var commands [][]string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, append([]string{name}, args...))
		switch {
		case len(args) > 2 && args[2] == "pg_isready":
			return "btcpay-db:5432 - accepting connections\n", nil
		case len(args) > 2 && args[2] == "psql":
			return "1\n", nil
		default:
			return "", errors.New("unexpected command")
		}
	}

	if err := ensureBtcpayNbxplorerDatabase(context.Background(), run); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("existing database must not be recreated; got commands: %v", commands)
	}
}

func TestBtcpayRegistryAcceptsApp(t *testing.T) {
	apps := []appHandler{
		stubApp{def: btcpayDefinition()},
		stubApp{def: lndgDefinition()},
		stubApp{def: lnbitsDefinition()},
		stubApp{def: mempoolDefinition()},
	}
	if err := validateAppRegistry(apps); err != nil {
		t.Fatalf("registry rejected btcpay: %v", err)
	}
}
