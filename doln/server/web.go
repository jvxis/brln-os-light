package main

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/lightningnetwork/lnd/lnrpc"
)

//go:embed static/index.html
var staticFS embed.FS

type webServer struct {
	lnd      *lndClient
	resolver *dnsResolver
	config   *serverConfig
	qlog     *queryLog
	startAt  time.Time
}

func startWebServer(addr string, lnd *lndClient, resolver *dnsResolver, cfg *serverConfig, qlog *queryLog) {
	ws := &webServer{
		lnd:      lnd,
		resolver: resolver,
		config:   cfg,
		qlog:     qlog,
		startAt:  time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.handleIndex)
	mux.HandleFunc("/api/status", ws.handleStatus)
	mux.HandleFunc("/api/log", ws.handleLog)
	mux.HandleFunc("/api/config", ws.handleConfig)

	log.Printf("Web UI listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Printf("web server error: %v", err)
	}
}

func (ws *webServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (ws *webServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	lndOk := false
	if _, err := ws.lnd.lightning.GetInfo(ws.lnd.ctx(), &lnrpc.GetInfoRequest{}); err == nil {
		lndOk = true
	}

	adguardOk := false
	testQuery := []byte{
		// Minimal DNS query for example.com A record
		0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x07, 0x65, 0x78, 0x61,
		0x6d, 0x70, 0x6c, 0x65, 0x03, 0x63, 0x6f, 0x6d,
		0x00, 0x00, 0x01, 0x00, 0x01,
	}
	if _, err := ws.resolver.resolve(testQuery); err == nil {
		adguardOk = true
	}

	uptime := time.Since(ws.startAt).Truncate(time.Second).String()

	writeJSON(w, map[string]interface{}{
		"lnd_connected":     lndOk,
		"adguard_connected": adguardOk,
		"uptime":            uptime,
		"queries_handled":   ws.qlog.totalCount(),
	})
}

func (ws *webServer) handleLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, ws.qlog.recent())
}

func (ws *webServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			AdguardDNSHost string `json:"adguard_dns_host"`
			AdguardDNSPort string `json:"adguard_dns_port"`
			KeysendAmtSat  int64  `json:"keysend_amount_sat"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if req.AdguardDNSHost == "" {
			req.AdguardDNSHost = "adguard"
		}
		if req.AdguardDNSPort == "" {
			req.AdguardDNSPort = "53"
		}
		if req.KeysendAmtSat <= 0 {
			req.KeysendAmtSat = 1
		}
		ws.config.update(req.AdguardDNSHost, req.AdguardDNSPort, req.KeysendAmtSat)
		ws.resolver.updateAddr(ws.config.dnsAddr())
		if err := ws.config.save(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, map[string]interface{}{
		"adguard_dns_host":  ws.config.AdguardDNSHost,
		"adguard_dns_port":  ws.config.AdguardDNSPort,
		"keysend_amount_sat": ws.config.keysendAmt(),
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
