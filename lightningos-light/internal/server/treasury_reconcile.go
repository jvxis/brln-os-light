package server

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type treasuryOrderRecord struct {
	treasuryOrder
	private     treasuryPrivateOrder
	bridgeID    string
	invoiceHash string
}

type treasuryRow interface{ Scan(...any) error }

func readTreasuryOrder(row treasuryRow) (treasuryOrderRecord, error) {
	var record treasuryOrderRecord
	var public, private []byte
	var state string
	if err := row.Scan(&record.bridgeID, &record.invoiceHash, &state, &public, &private); err != nil {
		return record, errors.New("treasury order unavailable")
	}
	if json.Unmarshal(public, &record.treasuryOrder) != nil || json.Unmarshal(private, &record.private) != nil {
		return record, errors.New("could not read treasury order")
	}
	record.State = state
	return record, nil
}

const selectTreasuryOrder = `SELECT bridge_id, COALESCE(invoice_hash,''), state, public_data, private_data
	FROM stablecoin_treasury_orders WHERE id=$1`

func (s *Server) reconcileTreasuryOrder(ctx context.Context, record treasuryOrderRecord) (treasuryOrder, error) {
	if record.State == "completed" || record.State == "refunded" {
		return record.treasuryOrder, nil
	}
	order := record.treasuryOrder
	if record.Direction == treasuryToBase && record.State != "quoted" {
		details, found, err := s.lnd.TrackPaymentDetails(ctx, record.invoiceHash)
		if err == nil && found {
			switch strings.ToUpper(details.Status) {
			case "SUCCEEDED":
				order.State, order.ActualFeeMsat = "funded", details.FeeMsat
			case "FAILED":
				if record.State == "paying" {
					order.State = "failed"
				}
			}
		}
	}
	key, err := readEnvFileValue(secretsPath, treasuryKeyEnv)
	if err == nil && key != "" {
		if reply, err := treasuryBridgeCall(ctx, treasuryBridgeRequest{
			Action: "status", APIKey: key, BridgeID: record.bridgeID, StatusToken: record.private.Token,
		}); err == nil {
			s.applyTreasuryBridgeProof(ctx, &order, record, reply)
		}
	}
	return s.persistTreasuryObservation(ctx, record, order)
}

func (s *Server) applyTreasuryBridgeProof(ctx context.Context, order *treasuryOrder, record treasuryOrderRecord, reply treasuryBridgeReply) {
	if reply.Status == "created" {
		// Pending provider recognition has been observed after quote expiry.
		// Keep an already-dispatched Lightning order reserved in this state.
		return
	}
	switch reply.Status {
	case "pending":
		order.State = "pending"
	case "refunding":
		order.State = "refunding"
	case "failed", "expired":
		order.State = "review"
	case "completed":
		order.State = "review"
		if reply.AmountIn != order.AmountIn || reply.AmountOut <= 0 || reply.AmountOut > treasuryMaximumUSDCMicros*2 {
			return
		}
		ref := treasuryNormalizeHex(reply.DeliveryRef, 32)
		if ref == "" {
			return
		}
		rpc := newTreasuryRPC()
		if order.Direction == treasuryToBase {
			verified, err := rpc.verifyTransfer(ctx, ref, "", order.Wallet, reply.AmountOut)
			if err != nil || !verified {
				return
			}
		} else {
			if record.private.FundingRef != "" && treasuryNormalizeHex(reply.DepositRef, 32) != record.private.FundingRef {
				return
			}
			deposit, err := rpc.verifyTransfer(ctx, reply.DepositRef, order.Wallet, order.Deposit, order.AmountIn)
			paidMSat, lookupErr := s.lnd.LookupTreasuryInvoice(ctx, ref[2:])
			if err != nil || !deposit || lookupErr != nil || paidMSat != reply.AmountOut*1000 {
				return
			}
			order.ActualBaseFeeWei = rpc.actualBaseFee(ctx, reply.DepositRef)
		}
		order.State, order.DeliveredAmount, order.DeliveryRef = "completed", reply.AmountOut, ref
	case "refunded":
		order.State = "review"
		if order.Direction == treasuryToBase {
			received, err := s.lnd.LookupTreasuryInvoice(ctx, record.private.RefundHash)
			if err != nil || received <= 0 {
				return
			}
			order.State = "refunded"
			order.RefundedAmount = received / 1000
		} else {
			rpc := newTreasuryRPC()
			if record.private.FundingRef != "" && treasuryNormalizeHex(reply.DepositRef, 32) != record.private.FundingRef {
				return
			}
			deposit, err := rpc.verifyTransfer(ctx, reply.DepositRef, order.Wallet, order.Deposit, order.AmountIn)
			refunded, refundErr := rpc.transferCredit(ctx, reply.RefundRef, "", order.Wallet, order.AmountIn)
			if err != nil || !deposit || refundErr != nil || refunded <= 0 {
				return
			}
			order.State, order.RefundedAmount = "refunded", refunded
			order.ActualBaseFeeWei = rpc.actualBaseFee(ctx, reply.DepositRef)
		}
	}
}

func (s *Server) persistTreasuryObservation(ctx context.Context, previous treasuryOrderRecord, order treasuryOrder) (treasuryOrder, error) {
	if previous.treasuryOrder == order {
		return order, nil
	}
	raw, err := json.Marshal(order)
	if err != nil {
		return previous.treasuryOrder, err
	}
	updated, err := s.db.Exec(ctx, `UPDATE stablecoin_treasury_orders
		SET state=$2, public_data=$3, delivery_ref=NULLIF($4,'') WHERE id=$1 AND state=$5`,
		order.ID, order.State, raw, order.DeliveryRef, previous.State)
	if err != nil {
		return previous.treasuryOrder, errors.New("could not reconcile treasury order")
	}
	if updated.RowsAffected() == 0 {
		fresh, err := readTreasuryOrder(s.db.QueryRow(ctx, selectTreasuryOrder, order.ID))
		return fresh.treasuryOrder, err
	}
	return order, nil
}

func (s *Server) claimTreasuryPayment(ctx context.Context, id, session string) (treasuryOrderRecord, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return treasuryOrderRecord{}, errors.New("treasury state unavailable")
	}
	defer tx.Rollback(ctx)
	var ready bool
	if err := tx.QueryRow(ctx, `SELECT installed AND enabled FROM stablecoin_treasury_config
		WHERE singleton FOR UPDATE`).Scan(&ready); err != nil || !ready {
		return treasuryOrderRecord{}, errors.New("stablecoin treasury is stopped")
	}
	order, err := readTreasuryOrder(tx.QueryRow(ctx, selectTreasuryOrder+" FOR UPDATE", id))
	if err != nil || order.Direction != treasuryToBase || order.State != "quoted" ||
		order.private.Session != session || time.Until(order.Expires) < 5*time.Second {
		return treasuryOrderRecord{}, errors.New("quote expired, already used or belongs to a different session")
	}
	var reserved int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM((public_data->>'amount_in')::bigint +
		(public_data->>'max_routing_sat')::bigint),0)
		FROM stablecoin_treasury_orders WHERE direction='to_base' AND
		state NOT IN ('failed','expired','quoted') AND
		(created_at > now()-interval '1 day' OR state NOT IN ('completed','refunded'))`).Scan(&reserved); err != nil ||
		order.AmountIn+order.MaxFee > treasuryLightningDailyLimit-reserved {
		return treasuryOrderRecord{}, errors.New("treasury Lightning limit reached")
	}
	if _, err := tx.Exec(ctx, `UPDATE stablecoin_treasury_orders SET state='paying' WHERE id=$1`, id); err != nil || tx.Commit(ctx) != nil {
		return treasuryOrderRecord{}, errors.New("could not reserve treasury payment")
	}
	order.State = "paying"
	return order, nil
}
