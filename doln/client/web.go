package main

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

//go:embed static/index.html
var staticFS embed.FS

type webServer struct {
	lnd      *lndClient
	resolver *resolver
	config   *clientConfig
	qlog     *queryLog
	startAt  time.Time
}

func startWebServer(addr string, lnd *lndClient, res *resolver, cfg *clientConfig, qlog *queryLog) {
	ws := &webServer{
		lnd:      lnd,
		resolver: res,
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
	ownPub := ""
	if pub, err := ws.lnd.getOwnPubkey(); err == nil {
		lndOk = true
		ownPub = pub
	}

	uptime := time.Since(ws.startAt).Truncate(time.Second).String()

	writeJSON(w, map[string]interface{}{
		"lnd_connected":  lndOk,
		"own_pubkey":     ownPub,
		"server_count":   len(ws.config.getPubkeys()),
		"uptime":         uptime,
		"queries_sent":   ws.qlog.totalCount(),
	})
}

func (ws *webServer) handleLog(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, ws.qlog.recent())
}

func (ws *webServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			ServerPubkeys []string `json:"server_pubkeys"`
			DNSTimeoutSec int      `json:"dns_timeout_seconds"`
			KeysendAmtSat int64    `json:"keysend_amount_sat"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Clean pubkeys
		cleaned := []string{}
		for _, pk := range req.ServerPubkeys {
			pk = strings.TrimSpace(pk)
			if pk != "" {
				cleaned = append(cleaned, pk)
			}
		}
		if len(cleaned) == 0 {
			http.Error(w, "at least one server pubkey required", 400)
			return
		}
		if req.DNSTimeoutSec <= 0 {
			req.DNSTimeoutSec = 10
		}
		if req.KeysendAmtSat <= 0 {
			req.KeysendAmtSat = 1
		}
		ws.config.update(cleaned, req.DNSTimeoutSec, req.KeysendAmtSat)
		ws.resolver.updateConfig(ws.config)
		if err := ws.config.save(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	writeJSON(w, map[string]interface{}{
		"server_pubkeys":     ws.config.getPubkeys(),
		"dns_timeout_seconds": ws.config.getTimeout(),
		"keysend_amount_sat":  ws.config.getKeysendAmt(),
	})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
