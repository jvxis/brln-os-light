package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func main() {
	grpcHost := envOrDefault("LND_GRPC_HOST", "host.docker.internal:10009")
	certPath := envOrDefault("LND_CERT_PATH", "/data/lnd/tls.cert")
	macPath := envOrDefault("LND_MACAROON_PATH", "/data/lnd/data/chain/bitcoin/mainnet/admin.macaroon")
	adguardHost := envOrDefault("ADGUARD_DNS_HOST", "adguard")
	adguardPort := envOrDefault("ADGUARD_DNS_PORT", "53")
	amtStr := envOrDefault("KEYSEND_AMOUNT_SAT", "1")
	dataDir := envOrDefault("DATA_DIR", "/data/doln")
	webAddr := envOrDefault("WEB_LISTEN", "0.0.0.0:3000")

	keysendAmt, err := strconv.ParseInt(amtStr, 10, 64)
	if err != nil {
		log.Fatalf("invalid KEYSEND_AMOUNT_SAT: %v", err)
	}

	dnsAddr := fmt.Sprintf("%s:%s", adguardHost, adguardPort)
	log.Printf("DoLN Server starting — LND=%s DNS=%s keysend=%d sat", grpcHost, dnsAddr, keysendAmt)

	lnd, err := newLNDClient(grpcHost, certPath, macPath)
	if err != nil {
		log.Fatalf("failed to connect to LND: %v", err)
	}
	defer lnd.close()

	cfg := newServerConfig(adguardHost, adguardPort, keysendAmt, dataDir)
	resolver := newDNSResolver(cfg.dnsAddr())
	qlog := newQueryLog(50)
	handler := newHandler(lnd, resolver, cfg, qlog, dataDir)

	go handler.run()
	go startWebServer(webAddr, lnd, resolver, cfg, qlog)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("DoLN Server shutting down")
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
