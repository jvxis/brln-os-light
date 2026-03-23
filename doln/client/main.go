package main

import (
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	grpcHost := envOrDefault("LND_GRPC_HOST", "host.docker.internal:10009")
	certPath := envOrDefault("LND_CERT_PATH", "/data/lnd/tls.cert")
	macPath := envOrDefault("LND_MACAROON_PATH", "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon")
	serverPubkeys := envOrDefault("DOLN_SERVER_PUBKEYS", "")
	socks5Listen := envOrDefault("SOCKS5_LISTEN", "0.0.0.0:1080")
	timeoutStr := envOrDefault("DNS_TIMEOUT_SECONDS", "10")
	amtStr := envOrDefault("KEYSEND_AMOUNT_SAT", "1")
	dataDir := envOrDefault("DATA_DIR", "/data/doln")
	webAddr := envOrDefault("WEB_LISTEN", "0.0.0.0:1081")

	keysendAmt, err := strconv.ParseInt(amtStr, 10, 64)
	if err != nil {
		log.Fatalf("invalid KEYSEND_AMOUNT_SAT: %v", err)
	}

	timeoutSec, err := strconv.Atoi(timeoutStr)
	if err != nil {
		log.Fatalf("invalid DNS_TIMEOUT_SECONDS: %v", err)
	}

	pubkeys := []string{}
	for _, pk := range strings.Split(serverPubkeys, ",") {
		pk = strings.TrimSpace(pk)
		if pk != "" {
			pubkeys = append(pubkeys, pk)
		}
	}
	if len(pubkeys) == 0 {
		log.Fatal("DOLN_SERVER_PUBKEYS must contain at least one pubkey")
	}

	log.Printf("DoLN Client starting — LND=%s SOCKS5=%s servers=%d timeout=%ds", grpcHost, socks5Listen, len(pubkeys), timeoutSec)

	lnd, err := newLNDClient(grpcHost, certPath, macPath)
	if err != nil {
		log.Fatalf("failed to connect to LND: %v", err)
	}
	defer lnd.close()

	ownPubkey, err := lnd.getOwnPubkey()
	if err != nil {
		log.Fatalf("failed to get own pubkey: %v", err)
	}
	log.Printf("own pubkey: %s", ownPubkey)

	cfg := newClientConfig(pubkeys, timeoutSec, keysendAmt, dataDir)
	pending := newPendingStore()
	qlog := newQueryLog(50)
	res := newResolver(lnd, pending, cfg, ownPubkey, qlog)

	go subscribeResponses(lnd, pending, dataDir)
	go startSOCKS5(socks5Listen, res)
	go startWebServer(webAddr, lnd, res, cfg, qlog)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("DoLN Client shutting down")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
