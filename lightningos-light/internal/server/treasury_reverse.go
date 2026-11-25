package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/ethereum/go-ethereum/rlp"
	"golang.org/x/crypto/sha3"
)

const treasuryGasOracle = "0x420000000000000000000000000000000000000f"

type treasurySigningRequest struct {
	Version          int    `json:"version"`
	ID               string `json:"id"`
	From             string `json:"from"`
	Recipient        string `json:"recipient"`
	Micros           string `json:"micros"`
	ChainID          int    `json:"chainId"`
	ExpiresAt        int64  `json:"expiresAt"`
	Nonce            uint64 `json:"nonce"`
	GasLimit         string `json:"gasLimit"`
	MaxFee           string `json:"maxFeePerGas"`
	Priority         string `json:"maxPriorityFeePerGas"`
	MaxExecution     string `json:"maximumGasWei"`
	L1Estimate       string `json:"l1EstimateWei"`
	OperatorEstimate string `json:"operatorEstimateWei"`
	EstimatedTotal   string `json:"estimatedTotalWei"`
	AvailableETH     string `json:"availableEthWei"`
}

func (rpc treasuryRPC) quantity(ctx context.Context, method string, params []any) (uint64, error) {
	var value string
	if err := rpc.call(ctx, method, params, &value); err != nil || !strings.HasPrefix(value, "0x") {
		return 0, errors.New("could not estimate Base network fees")
	}
	return strconv.ParseUint(value[2:], 16, 64)
}

func (rpc treasuryRPC) prepareTransfer(ctx context.Context, order treasuryOrder) (treasurySigningRequest, error) {
	var chain string
	if err := rpc.call(ctx, "eth_chainId", nil, &chain); err != nil || chain != treasuryBaseChainID {
		return treasurySigningRequest{}, errors.New("could not verify Base chain ID")
	}
	nonce, err := rpc.quantity(ctx, "eth_getTransactionCount", []any{order.Wallet, "pending"})
	if err != nil || nonce >= 1<<53 {
		return treasurySigningRequest{}, errors.New("could not verify the Base account nonce")
	}
	data, err := treasuryTransferData(order.Deposit, order.AmountIn)
	if err != nil {
		return treasurySigningRequest{}, err
	}
	gas, err := rpc.quantity(ctx, "eth_estimateGas", []any{map[string]string{
		"from": order.Wallet, "to": treasuryBaseUSDC, "data": data,
	}})
	if err != nil || gas < 21000 || gas > 160000 {
		return treasurySigningRequest{}, errors.New("could not estimate the USDC transfer; check the wallet balance")
	}
	gas += gas / 4
	price, err := rpc.quantity(ctx, "eth_gasPrice", nil)
	if err != nil || price == 0 || price > 5_000_000_000 {
		return treasurySigningRequest{}, errors.New("Base gas price unavailable or above the treasury limit")
	}
	priority, err := rpc.quantity(ctx, "eth_maxPriorityFeePerGas", nil)
	if err != nil || priority == 0 || priority > price {
		return treasurySigningRequest{}, errors.New("Base priority fee unavailable")
	}
	maxFee := 2 * price
	l1, err := rpc.quantity(ctx, "eth_call", []any{map[string]string{
		"to": treasuryGasOracle, "data": fmt.Sprintf("0xf1c7a58b%064x", 512),
	}, "latest"})
	if err != nil || l1 > 1_000_000_000_000_000 {
		return treasurySigningRequest{}, errors.New("could not estimate the Base data fee")
	}
	operator, err := rpc.quantity(ctx, "eth_call", []any{map[string]string{
		"to": treasuryGasOracle, "data": fmt.Sprintf("0x275aedd2%064x", gas),
	}, "latest"})
	if err != nil || operator > 1_000_000_000_000_000 {
		return treasurySigningRequest{}, errors.New("could not estimate the Base operator fee")
	}
	var balanceHex string
	if err := rpc.call(ctx, "eth_getBalance", []any{order.Wallet, "latest"}, &balanceHex); err != nil || !strings.HasPrefix(balanceHex, "0x") {
		return treasurySigningRequest{}, errors.New("could not verify the ETH balance")
	}
	balance, ok := new(big.Int).SetString(balanceHex[2:], 16)
	if !ok || balance.Sign() < 0 {
		return treasurySigningRequest{}, errors.New("invalid ETH balance")
	}
	execution := gas * maxFee
	return treasurySigningRequest{
		Version: 1, ID: order.ID, From: order.Wallet, Recipient: order.Deposit, Micros: strconv.FormatInt(order.AmountIn, 10),
		ChainID: 8453, ExpiresAt: order.Expires.UnixMilli(), Nonce: nonce, GasLimit: strconv.FormatUint(gas, 10),
		MaxFee: strconv.FormatUint(maxFee, 10), Priority: strconv.FormatUint(priority, 10),
		MaxExecution: strconv.FormatUint(execution, 10), L1Estimate: strconv.FormatUint(l1, 10),
		OperatorEstimate: strconv.FormatUint(operator, 10), EstimatedTotal: strconv.FormatUint(execution+l1+operator, 10),
		AvailableETH: balance.String(),
	}, nil
}

func treasuryKeccak(parts ...[]byte) []byte {
	digest := sha3.NewLegacyKeccak256()
	for _, part := range parts {
		_, _ = digest.Write(part)
	}
	return digest.Sum(nil)
}

// Only a type-2 EIP-1559 transfer of the exact USDC amount and recipient can
// reach broadcast. Canonical RLP decoding and the existing secp256k1 library
// establish the sender from the chain-bound signing hash.
func checkTreasurySignedTransfer(raw string, signing treasurySigningRequest) (string, error) {
	failure := errors.New("signed transaction differs from the approved Base transfer")
	if len(raw) > 1026 || !strings.HasPrefix(raw, "0x02") || signing.ChainID != 8453 {
		return "", failure
	}
	tx, err := hex.DecodeString(raw[2:])
	if err != nil {
		return "", failure
	}
	var fields []rlp.RawValue
	if rlp.DecodeBytes(tx[1:], &fields) != nil || len(fields) != 12 {
		return "", failure
	}
	var chain, nonce, tip, price, gas, value, parity uint64
	var to, data []byte
	var accessList []rlp.RawValue
	var r, s big.Int
	for i, item := range []any{&chain, &nonce, &tip, &price, &gas, &to, &value, &data, &accessList, &parity, &r, &s} {
		if rlp.DecodeBytes(fields[i], item) != nil {
			return "", failure
		}
	}
	if chain != 8453 || nonce != signing.Nonce || value != 0 || len(accessList) != 0 || parity > 1 ||
		"0x"+hex.EncodeToString(to) != treasuryBaseUSDC ||
		strconv.FormatUint(tip, 10) != signing.Priority || strconv.FormatUint(price, 10) != signing.MaxFee ||
		strconv.FormatUint(gas, 10) != signing.GasLimit {
		return "", failure
	}
	amount, err := strconv.ParseInt(signing.Micros, 10, 64)
	if err != nil {
		return "", failure
	}
	wantData, err := treasuryTransferData(signing.Recipient, amount)
	if err != nil || "0x"+hex.EncodeToString(data) != wantData {
		return "", failure
	}
	curveOrder := btcec.S256().N
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(curveOrder), 1)
	if r.Sign() <= 0 || r.Cmp(curveOrder) >= 0 || s.Sign() <= 0 || s.Cmp(halfOrder) > 0 {
		return "", failure
	}
	unsigned, err := rlp.EncodeToBytes(fields[:9])
	if err != nil {
		return "", failure
	}
	sig := make([]byte, 65)
	sig[0] = byte(27 + parity)
	r.FillBytes(sig[1:33])
	s.FillBytes(sig[33:])
	pub, _, err := ecdsa.RecoverCompact(sig, treasuryKeccak([]byte{2}, unsigned))
	if err != nil {
		return "", failure
	}
	sender := treasuryKeccak(pub.SerializeUncompressed()[1:])[12:]
	expected, _ := hex.DecodeString(strings.TrimPrefix(treasuryBaseAddress(signing.From), "0x"))
	if !bytes.Equal(sender, expected) {
		return "", failure
	}
	return "0x" + hex.EncodeToString(treasuryKeccak(tx)), nil
}

func (s *Server) prepareTreasuryBase(ctx context.Context, id, session string) (treasurySigningRequest, error) {
	record, err := readTreasuryOrder(s.db.QueryRow(ctx, selectTreasuryOrder, id))
	if err != nil || record.Direction != treasuryToLightning || record.State != "quoted" ||
		record.private.Session != session || time.Until(record.Expires) < 12*time.Second {
		return treasurySigningRequest{}, errors.New("Base quote unavailable or expired")
	}
	signing, err := newTreasuryRPC().prepareTransfer(ctx, record.treasuryOrder)
	if err != nil {
		return treasurySigningRequest{}, err
	}
	record.private.BaseSigning = &signing
	private, err := json.Marshal(record.private)
	if err != nil {
		return treasurySigningRequest{}, err
	}
	tag, err := s.db.Exec(ctx, `UPDATE stablecoin_treasury_orders SET private_data=$2
		WHERE id=$1 AND state='quoted' AND expires_at > now() AND EXISTS (
		    SELECT 1 FROM stablecoin_treasury_config WHERE singleton AND installed AND enabled)`, id, private)
	if err != nil || tag.RowsAffected() != 1 {
		return treasurySigningRequest{}, errors.New("quote is no longer available to sign")
	}
	return signing, nil
}

func (s *Server) submitTreasuryBase(ctx context.Context, id, session, raw string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", errors.New("treasury state unavailable")
	}
	defer tx.Rollback(ctx)
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT installed AND enabled FROM stablecoin_treasury_config
		WHERE singleton FOR UPDATE`).Scan(&enabled); err != nil || !enabled {
		return "", errors.New("stablecoin treasury is stopped")
	}
	record, err := readTreasuryOrder(tx.QueryRow(ctx, selectTreasuryOrder+" FOR UPDATE", id))
	if err != nil || record.Direction != treasuryToLightning || record.State != "quoted" || record.private.Session != session ||
		record.private.BaseSigning == nil || time.Until(record.Expires) < 2*time.Second {
		return "", errors.New("signed Base quote unavailable, expired or already used")
	}
	signing := record.private.BaseSigning
	available, have := new(big.Int).SetString(signing.AvailableETH, 10)
	total, expect := new(big.Int).SetString(signing.EstimatedTotal, 10)
	if !have || !expect || available.Cmp(total) < 0 {
		return "", errors.New("insufficient ETH on Base for estimated network fees")
	}
	hash, err := checkTreasurySignedTransfer(raw, *signing)
	if err != nil {
		return "", err
	}
	record.private.FundingRef = hash
	private, err := json.Marshal(record.private)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE stablecoin_treasury_orders SET state='broadcasting',private_data=$2 WHERE id=$1`, id, private); err != nil || tx.Commit(ctx) != nil {
		return "", errors.New("could not reserve the Base broadcast")
	}
	// Once claimed, observe the exact signed transaction even if the RPC
	// disconnects before acknowledging the broadcast.
	var returned string
	_ = newTreasuryRPC().call(ctx, "eth_sendRawTransaction", []any{raw}, &returned)
	return hash, nil
}
