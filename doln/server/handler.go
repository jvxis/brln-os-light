package main

import (
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type handler struct {
	lnd      *lndClient
	resolver *dnsResolver
	config   *serverConfig
	qlog     *queryLog
	dataDir  string
}

func newHandler(lnd *lndClient, resolver *dnsResolver, cfg *serverConfig, qlog *queryLog, dataDir string) *handler {
	return &handler{
		lnd:      lnd,
		resolver: resolver,
		config:   cfg,
		qlog:     qlog,
		dataDir:  dataDir,
	}
}

func (h *handler) run() {
	settleIndex := h.loadSettleIndex()
	log.Printf("subscribing to invoices from settle_index=%d", settleIndex)

	for {
		stream, err := h.lnd.subscribeInvoices(settleIndex)
		if err != nil {
			log.Printf("failed to subscribe to invoices: %v — retrying in 5s", err)
			time.Sleep(5 * time.Second)
			continue
		}

		for {
			invoice, err := stream.Recv()
			if err != nil {
				log.Printf("invoice stream error: %v — reconnecting in 5s", err)
				time.Sleep(5 * time.Second)
				break
			}

			if !invoice.IsKeysend || invoice.State != 1 {
				continue
			}

			htlcs := invoice.Htlcs
			if len(htlcs) == 0 {
				continue
			}

			records := htlcs[0].CustomRecords
			dnsQuery, hasDNS := records[tlvDNSPayload]
			returnPubkey, hasPubkey := records[tlvReturnPubkey]
			requestID, hasReqID := records[tlvRequestID]

			if !hasDNS || !hasPubkey || !hasReqID {
				continue
			}

			if len(returnPubkey) != 33 {
				log.Printf("invalid return pubkey length: %d", len(returnPubkey))
				continue
			}

			if len(requestID) != 16 {
				log.Printf("invalid request ID length: %d", len(requestID))
				continue
			}

			go h.handleQuery(dnsQuery, returnPubkey, requestID)

			settleIndex = invoice.SettleIndex
			h.saveSettleIndex(settleIndex)
		}
	}
}

func (h *handler) handleQuery(query, returnPubkey, requestID []byte) {
	start := time.Now()
	srcPub := hex.EncodeToString(returnPubkey[:8])
	reqIDHex := hex.EncodeToString(requestID)
	domain := parseDomain(query)

	log.Printf("DNS query for %s from %s reqID=%s", domain, srcPub, reqIDHex)

	responseBytes, err := h.resolver.resolve(query)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		log.Printf("DNS resolve error: %v", err)
		h.qlog.add(queryEntry{
			Timestamp:    nowTimestamp(),
			SourcePubkey: srcPub,
			Domain:       domain,
			Status:       "error",
			LatencyMs:    latency,
		})
		return
	}

	records := map[uint64][]byte{
		tlvDNSPayload: responseBytes,
		tlvRequestID:  requestID,
	}

	keysendAmt := h.config.keysendAmt()
	if err := h.lnd.sendKeysend(returnPubkey, keysendAmt, records); err != nil {
		log.Printf("failed to send keysend response: %v", err)
		h.qlog.add(queryEntry{
			Timestamp:    nowTimestamp(),
			SourcePubkey: srcPub,
			Domain:       domain,
			Status:       "error",
			LatencyMs:    latency,
		})
		return
	}

	latency = time.Since(start).Milliseconds()
	log.Printf("DNS response sent to %s reqID=%s (%d bytes, %dms)", srcPub, reqIDHex, len(responseBytes), latency)

	h.qlog.add(queryEntry{
		Timestamp:    nowTimestamp(),
		SourcePubkey: srcPub,
		Domain:       domain,
		Status:       "resolved",
		LatencyMs:    latency,
		ResponseSize: len(responseBytes),
	})
}

func parseDomain(queryBytes []byte) string {
	msg := new(dns.Msg)
	if err := msg.Unpack(queryBytes); err != nil {
		return "unknown"
	}
	if len(msg.Question) > 0 {
		return strings.TrimSuffix(msg.Question[0].Name, ".")
	}
	return "unknown"
}

func (h *handler) settleIndexPath() string {
	return filepath.Join(h.dataDir, "settle_index")
}

func (h *handler) loadSettleIndex() uint64 {
	data, err := os.ReadFile(h.settleIndexPath())
	if err != nil {
		return 0
	}
	idx, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return idx
}

func (h *handler) saveSettleIndex(idx uint64) {
	_ = os.MkdirAll(h.dataDir, 0750)
	_ = os.WriteFile(h.settleIndexPath(), []byte(strconv.FormatUint(idx, 10)), 0600)
}
