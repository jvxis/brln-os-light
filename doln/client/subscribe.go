package main

import (
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func subscribeResponses(lnd *lndClient, pending *pendingStore, dataDir string) {
	settleIndex := loadSettleIndex(dataDir)
	log.Printf("subscribing to invoice responses from settle_index=%d", settleIndex)

	for {
		stream, err := lnd.subscribeInvoices(settleIndex)
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
			dnsResp, hasDNS := records[tlvDNSPayload]
			reqIDBytes, hasReqID := records[tlvRequestID]

			if !hasDNS || !hasReqID || len(reqIDBytes) != 16 {
				continue
			}

			var reqID [16]byte
			copy(reqID[:], reqIDBytes)

			if pending.resolve(reqID, dnsResp) {
				log.Printf("DNS response received reqID=%s (%d bytes)", hex.EncodeToString(reqID[:]), len(dnsResp))
			}

			settleIndex = invoice.SettleIndex
			saveSettleIndex(dataDir, settleIndex)
		}
	}
}

func settleIndexPath(dataDir string) string {
	return filepath.Join(dataDir, "settle_index")
}

func loadSettleIndex(dataDir string) uint64 {
	data, err := os.ReadFile(settleIndexPath(dataDir))
	if err != nil {
		return 0
	}
	idx, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return idx
}

func saveSettleIndex(dataDir string, idx uint64) {
	_ = os.MkdirAll(dataDir, 0750)
	_ = os.WriteFile(settleIndexPath(dataDir), []byte(strconv.FormatUint(idx, 10)), 0600)
}
