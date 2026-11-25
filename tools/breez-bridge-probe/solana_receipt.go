package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

const (
	solanaMainnetGenesis = "5eykt4UsFv8P8NJdTREpY1vzqKqZKvdpKuc147dw2N9d"
	solanaDevnetGenesis  = "EtWTRABZaYq6iMfeYKouRu166VU2xqa1wcaWoxPkrZBG"
	splTokenProgram      = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
)

type solanaBalance struct {
	Mint    string `json:"mint"`
	Owner   string `json:"owner"`
	Program string `json:"programId"`
	Amount  struct {
		Micros   string `json:"amount"`
		Decimals int    `json:"decimals"`
	} `json:"uiTokenAmount"`
}

type solanaReceipt struct {
	Slot        uint64 `json:"slot"`
	Transaction struct {
		Signatures []string `json:"signatures"`
	} `json:"transaction"`
	Meta struct {
		Error  json.RawMessage `json:"err"`
		Before []solanaBalance `json:"preTokenBalances"`
		After  []solanaBalance `json:"postTokenBalances"`
	} `json:"meta"`
}

func solanaProofRequest(req bridgeCommand) (bridgeResult, error) {
	var endpoint, mint, genesis string
	switch req.Cluster {
	case "mainnet":
		endpoint, mint, genesis = "https://api.mainnet-beta.solana.com", solanaUSDCMint, solanaMainnetGenesis
	case "devnet":
		endpoint, mint, genesis = "https://api.devnet.solana.com", devnetUSDCMint, solanaDevnetGenesis
	default:
		return bridgeResult{}, errors.New("invalid Solana cluster")
	}
	return (sponsorRPC{endpoint: endpoint, http: sponsorHTTPClient()}).verifySolanaCredit(req, mint, genesis)
}

func (s sponsorRPC) verifySolanaCredit(req bridgeCommand, mint, genesis string) (bridgeResult, error) {
	if !validSolanaAddress(req.Recipient) || !validSolanaBase58(req.Reference, 64, 64, 88) || req.Amount <= 0 {
		return bridgeResult{}, errors.New("invalid Solana receipt parameters")
	}
	var actualGenesis string
	if err := s.call("getGenesisHash", nil, &actualGenesis); err != nil || actualGenesis != genesis {
		return bridgeResult{}, errors.New("could not verify the Solana cluster")
	}
	var tx solanaReceipt
	if err := s.call("getTransaction", []any{req.Reference, map[string]any{
		"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0,
	}}, &tx); err != nil {
		return bridgeResult{}, errors.New("finalized Solana transaction unavailable")
	}
	if tx.Slot == 0 || len(tx.Transaction.Signatures) == 0 || tx.Transaction.Signatures[0] != req.Reference ||
		!bytes.Equal(tx.Meta.Error, []byte("null")) || tx.Meta.Before == nil || tx.Meta.After == nil {
		return bridgeResult{}, errors.New("invalid or failed Solana transaction")
	}
	before, beforeErr := solanaBalanceSum(tx.Meta.Before, req.Recipient, mint)
	after, afterErr := solanaBalanceSum(tx.Meta.After, req.Recipient, mint)
	if beforeErr != nil || afterErr != nil || after < before || after-before != uint64(req.Amount) {
		return bridgeResult{}, errors.New("Solana transaction did not credit the quoted wallet and USDC amount")
	}
	return bridgeResult{
		Cluster: req.Cluster, Mint: mint, Status: "verified", AmountOut: req.Amount,
		DeliveryRef: req.Reference, Slot: tx.Slot,
	}, nil
}

func solanaBalanceSum(items []solanaBalance, owner, mint string) (uint64, error) {
	var sum uint64
	for _, item := range items {
		if item.Mint != mint || item.Owner != owner || item.Program != splTokenProgram {
			continue
		}
		amount, err := strconv.ParseUint(item.Amount.Micros, 10, 64)
		if err != nil || item.Amount.Decimals != 6 || amount > math.MaxUint64-sum {
			return 0, errors.New("invalid USDC balance data")
		}
		sum += amount
	}
	return sum, nil
}
