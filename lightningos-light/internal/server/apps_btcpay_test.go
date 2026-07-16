package server

import (
	"context"
	"errors"
	"strings"
	"testing"
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
	wiring, err := buildBtcpayRemoteWiring("bitcoin.br-ln.com:8085", "user", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wiring.Source != "remote" {
		t.Fatalf("unexpected source: %s", wiring.Source)
	}
	if wiring.RPCURL != "http://bitcoin.br-ln.com:8085/" {
		t.Fatalf("unexpected rpc url: %s", wiring.RPCURL)
	}
	if wiring.NodeEndpoint != "bitcoin.br-ln.com:8333" {
		t.Fatalf("unexpected node endpoint: %s", wiring.NodeEndpoint)
	}
	if wiring.ProbeP2P != "bitcoin.br-ln.com:8333" {
		t.Fatalf("unexpected p2p probe: %s", wiring.ProbeP2P)
	}
	if wiring.JoinBitcoinNetwork {
		t.Fatal("remote wiring must not join the bitcoincore network")
	}
}

func TestBuildBtcpayRemoteWiringHTTPS(t *testing.T) {
	wiring, err := buildBtcpayRemoteWiring("https://bitcoin.example.com:8332", "user", "pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wiring.RPCURL != "https://bitcoin.example.com:8332/" {
		t.Fatalf("expected https scheme preserved, got: %s", wiring.RPCURL)
	}
}

func TestBuildBtcpayRemoteWiringMissingCredentials(t *testing.T) {
	if _, err := buildBtcpayRemoteWiring("bitcoin.br-ln.com:8085", "", ""); err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if _, err := buildBtcpayRemoteWiring("", "user", "pass"); err == nil {
		t.Fatal("expected error for missing host")
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
	wiring := btcpayBitcoinWiring{Source: "remote"}
	compose := btcpayComposeContents(paths, wiring)

	for _, required := range []string{
		btcpayImage,
		btcpayNbxplorerImage,
		btcpayPostgresImage,
		"NBXPLORER_NETWORK: mainnet",
		"NBXPLORER_CHAINS: btc",
		"NBXPLORER_BTCRPCURL: ${NBXPLORER_BTCRPCURL}",
		"NBXPLORER_BTCNODEENDPOINT: ${NBXPLORER_BTCNODEENDPOINT}",
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
}

func TestBtcpayComposeContentsAppSource(t *testing.T) {
	paths := btcpayAppPaths()
	wiring := btcpayBitcoinWiring{Source: "app", JoinBitcoinNetwork: true}
	compose := btcpayComposeContents(paths, wiring)

	for _, required := range []string{
		"bitcoincore_default",
		"external: true",
		"- bitcoincore",
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("app-source compose missing %q", required)
		}
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
