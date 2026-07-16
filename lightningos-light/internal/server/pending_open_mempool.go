package server

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

const mempoolAPIBaseURL = "https://mempool.space/api"

var pendingOpenTxIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type pendingOpenMempoolTx struct {
	Fee    int64 `json:"fee"`
	Weight int64 `json:"weight"`
	Vin    []struct {
		Sequence uint64 `json:"sequence"`
	} `json:"vin"`
	Status struct {
		Confirmed bool `json:"confirmed"`
	} `json:"status"`
}

type pendingOpenFundingTxObservation struct {
	Status                   string
	FeeSat                   int64
	Vsize                    float64
	EffectiveFeeRateSatVbyte float64
	RBF                      *bool
}

func enrichPendingOpenFundingTransactions(ctx context.Context, rows []pendingChannelResponse) {
	cache := make(map[string]pendingOpenFundingTxObservation)
	for i := range rows {
		if rows[i].Status != "opening" {
			continue
		}
		txid := pendingOpenFundingTxID(rows[i].ChannelPoint)
		if txid == "" {
			rows[i].FundingTxStatus = "unavailable"
			continue
		}

		observation, ok := cache[txid]
		if !ok {
			observation = loadPendingOpenFundingTxObservation(ctx, mempoolAPIBaseURL, txid)
			cache[txid] = observation
		}
		rows[i].FundingTxStatus = observation.Status
		rows[i].FundingTxFeeSat = observation.FeeSat
		rows[i].FundingTxVsize = observation.Vsize
		rows[i].FundingTxEffectiveFeeRateSatVbyte = observation.EffectiveFeeRateSatVbyte
		rows[i].FundingTxRBF = observation.RBF
	}
}

func loadPendingOpenFundingTxObservation(ctx context.Context, baseURL, txid string) pendingOpenFundingTxObservation {
	observation := pendingOpenFundingTxObservation{Status: "unavailable"}
	clean := strings.ToLower(strings.TrimSpace(txid))
	if !pendingOpenTxIDPattern.MatchString(clean) {
		return observation
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/tx/"+clean, nil)
	if err != nil {
		return observation
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return observation
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		observation.Status = "not_found"
		return observation
	}
	if resp.StatusCode != http.StatusOK {
		return observation
	}

	var tx pendingOpenMempoolTx
	if err := json.NewDecoder(resp.Body).Decode(&tx); err != nil {
		return observation
	}
	if tx.Weight <= 0 || tx.Fee < 0 {
		return observation
	}

	observation.Status = "mempool"
	if tx.Status.Confirmed {
		observation.Status = "confirmed"
	}
	observation.FeeSat = tx.Fee
	observation.Vsize = float64(tx.Weight) / 4
	observation.EffectiveFeeRateSatVbyte = float64(tx.Fee) / observation.Vsize
	rbf := false
	for _, input := range tx.Vin {
		if input.Sequence < 0xfffffffe {
			rbf = true
			break
		}
	}
	observation.RBF = &rbf
	return observation
}

func pendingOpenFundingTxID(channelPoint string) string {
	parts := strings.Split(strings.TrimSpace(channelPoint), ":")
	if len(parts) != 2 {
		return ""
	}
	txid := strings.ToLower(strings.TrimSpace(parts[0]))
	if !pendingOpenTxIDPattern.MatchString(txid) {
		return ""
	}
	return txid
}
