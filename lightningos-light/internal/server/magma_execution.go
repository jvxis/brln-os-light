package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

// Local execution states. These track what *we* have done, which is not the same
// as the Amboss status: the gap between the two is exactly where money can go
// missing, so every transition that moves funds is written before it is attempted.
const (
	magmaStateObserved       = "observed"
	magmaStateAccepting      = "accepting"
	magmaStateAccepted       = "accepted"
	magmaStateOpening        = "opening"
	magmaStateOpenBroadcast  = "open_broadcast"
	magmaStateConfirming     = "confirming"
	magmaStateConfirmed      = "confirmed"
	magmaStateRejected       = "rejected"
	magmaStateNeedsAttention = "needs_attention"

	// magmaInvoiceExpirySeconds matches the long expiry the production bot uses.
	// Amboss holds the payment (HODL_INVOICE_TIMEOUT is a real payment status),
	// so the invoice has to outlive the whole open-and-confirm cycle.
	magmaInvoiceExpirySeconds = 180000

	// magmaTokenSafetyWindow is when we start warning that the token is about to
	// expire. It is a warning, not a gate: if the token dies between OpenChannel
	// and sellerAddTransaction the order parks in `confirming`, and
	// reconcileExecution retries the confirmation on every poll once the token is
	// renewed. The sale is recoverable, so blocking the open would cost more than
	// it protects.
	magmaTokenSafetyWindow = 24 * time.Hour

	// magmaOpenFeeReserveSat is a rough on-chain fee cushion held back when
	// deciding whether a new order can be honoured. It only needs to be the right
	// order of magnitude: the exact fee is computed in the open preview.
	magmaOpenFeeReserveSat = 5000
)

// magmaLND is the slice of the LND client this app needs. Keeping it narrow lets
// the execution paths be tested without a node.
type magmaLND interface {
	CreateInvoice(ctx context.Context, amountSat int64, memo string, expirySeconds int64, opts *lndclient.CreateInvoiceOptions) (lndclient.CreatedInvoice, error)
	GetBalances(ctx context.Context) (lndclient.BalanceSummary, error)
	GetNodeDetails(ctx context.Context, pubkey string) (lndclient.NodeDetails, error)
	ConnectPeerWithTimeout(ctx context.Context, pubkey string, host string, perm bool, timeoutSec uint64) error
	OpenChannelWithOutpoints(ctx context.Context, params lndclient.OpenChannelParams) (string, error)
	PreviewOpenChannel(ctx context.Context, localFundingSat int64, pushSat int64, satPerVbyte int64) (lndclient.OpenChannelPreview, error)
	ListPendingChannels(ctx context.Context) ([]lndclient.PendingChannelInfo, error)
	ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error)
}

// magmaOnchainTxLister is split out because only the P&L needs it; keeping it
// optional means the core execution paths stay testable with a smaller fake.
type magmaOnchainTxLister interface {
	ListOnchainTransactions(ctx context.Context, limit int) ([]lndclient.OnchainTransaction, error)
}

var (
	errMagmaNoLND      = errors.New("LND client unavailable")
	errMagmaNotFound   = errors.New("order not found")
	errMagmaWrongState = errors.New("order is not in a state that allows this action")
)

// magmaExecutionRecord is the locally tracked half of an order.
type magmaExecutionRecord struct {
	OrderID        string
	BuyerPubkey    string
	SizeSat        int64
	RevenueSat     int64
	MagmaStatus    string
	LocalState     string
	PaymentRequest string
	InvoiceHash    string
	FundingTxid    string
	ChannelPoint   string
	AttemptCount   int
	LastError      string
}

func (s *MagmaService) execRecord(ctx context.Context, orderID string) (magmaExecutionRecord, error) {
	var record magmaExecutionRecord
	err := s.db.QueryRow(ctx, `
select order_id, buyer_pubkey, size_sat, revenue_sat, magma_status, local_state,
       invoice_payment_request, invoice_hash, funding_txid, channel_point,
       attempt_count, last_error
from magma_orders where order_id=$1
`, strings.TrimSpace(orderID)).Scan(
		&record.OrderID, &record.BuyerPubkey, &record.SizeSat, &record.RevenueSat,
		&record.MagmaStatus, &record.LocalState, &record.PaymentRequest, &record.InvoiceHash,
		&record.FundingTxid, &record.ChannelPoint, &record.AttemptCount, &record.LastError,
	)
	if err != nil {
		return magmaExecutionRecord{}, errMagmaNotFound
	}
	return record, nil
}

func (s *MagmaService) setLocalState(ctx context.Context, orderID, state, lastError string) error {
	_, err := s.db.Exec(ctx, `
update magma_orders set local_state=$2, last_error=$3, updated_at=now() where order_id=$1
`, orderID, state, lastError)
	return err
}

// requireActionMode keeps every fund-moving path behind an explicit mode. The app
// ships in monitor mode, so a fresh install cannot act even if an endpoint is
// called directly. Both assisted (operator clicks) and auto (policy decides) are
// allowed through here; the difference is who pulls the trigger, not what the
// action is permitted to do.
func (s *MagmaService) requireActionMode(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Installed || !settings.Enabled {
		return errors.New("Magma Inbound Sales is not running")
	}
	if settings.Mode != magmaModeAssisted && settings.Mode != magmaModeAuto {
		return errors.New("switch Magma Inbound Sales to assisted or auto mode to act on orders")
	}
	return nil
}

// usableToken returns the token when it is present and not already expired.
// Imminent expiry is surfaced as a warning by magmaTokenExpiryWarning instead of
// blocking, because an unconfirmed sale is recoverable once the token is renewed.
func (s *MagmaService) usableToken(ctx context.Context) (string, error) {
	token, err := s.token(ctx)
	if err != nil {
		return "", err
	}
	if err := magmaTokenUsable(token, time.Now()); err != nil {
		return "", err
	}
	return token, nil
}

// magmaTokenUsable is the credential gate, kept pure so it is directly testable.
// An opaque (non-JWT) token has no readable expiry and is allowed through: we
// cannot prove it is stale.
func magmaTokenUsable(token string, now time.Time) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("Amboss API token is not configured in the Fee Center")
	}
	expiry, ok := magmaTokenExpiry(token)
	if !ok {
		return nil
	}
	if !now.Before(expiry) {
		return errors.New("the Amboss API token has expired; renew it in the Fee Center")
	}
	return nil
}

// magmaTokenExpiryWarning reports an imminent expiry as advice rather than a
// blocker. A token that dies mid-sale parks the order in `confirming` and
// reconcileExecution finishes it once the token is renewed.
func magmaTokenExpiryWarning(token string, now time.Time) string {
	expiry, ok := magmaTokenExpiry(token)
	if !ok || !now.Before(expiry) {
		return ""
	}
	remaining := expiry.Sub(now)
	if remaining >= magmaTokenSafetyWindow {
		return ""
	}
	return fmt.Sprintf(
		"the Amboss API token expires in about %d hours. The channel open still works, "+
			"but the sale will only be confirmed to Amboss after you renew the token in the Fee Center",
		int(remaining.Hours()))
}

// MagmaCapacity is the on-chain picture behind an accept decision.
type MagmaCapacity struct {
	ConfirmedSat  int64 `json:"confirmed_sat"`
	CommittedSat  int64 `json:"committed_sat"`
	CommittedJobs int   `json:"committed_orders"`
	AvailableSat  int64 `json:"available_sat"`
}

// magmaCommittedStates are the orders where we already owe the buyer a channel:
// the invoice is out, or the funding is mid-flight. Their size is spoken for even
// though it has not left the wallet yet.
//
// The local state alone is not enough to decide that, because it only ever moves
// on our own actions. When a buyer walks away the order dies on the Amboss side
// and nothing here advances: local_state stays "accepted" forever. So every query
// over these states must also exclude the statuses Amboss treats as closed, or a
// dead sale keeps reserving wallet balance and an open slot for good.
var magmaCommittedStates = []string{magmaStateAccepting, magmaStateAccepted, magmaStateOpening}

// Capacity reports how much on-chain balance is genuinely free for a new sale.
//
// The subtlety is that accepting an order is a promise, not a spend. Two orders
// arriving minutes apart can each fit the wallet on their own and not fit
// together; checking only the raw balance would accept both and then fail to open
// the second, which Amboss records as SELLER_FAILED_TO_OPEN_CHANNEL.
func (s *MagmaService) Capacity(ctx context.Context) (MagmaCapacity, error) {
	if s.lnd == nil {
		return MagmaCapacity{}, errMagmaNoLND
	}
	balances, err := s.lnd.GetBalances(ctx)
	if err != nil {
		return MagmaCapacity{}, err
	}
	capacity := MagmaCapacity{ConfirmedSat: balances.OnchainConfirmedSat}
	if err := s.db.QueryRow(ctx, `
select coalesce(sum(size_sat),0), count(*) from magma_orders
where local_state = any($1) and not (magma_status = any($2))
`, magmaCommittedStates, magmaTerminalStatusList()).Scan(&capacity.CommittedSat, &capacity.CommittedJobs); err != nil {
		return MagmaCapacity{}, err
	}
	capacity.AvailableSat = capacity.ConfirmedSat - capacity.CommittedSat
	if capacity.AvailableSat < 0 {
		capacity.AvailableSat = 0
	}
	return capacity, nil
}

// ensureCapacityFor refuses an order the wallet cannot honour once every already
// promised channel is accounted for.
func (s *MagmaService) ensureCapacityFor(ctx context.Context, sizeSat int64) error {
	capacity, err := s.Capacity(ctx)
	if err != nil {
		return fmt.Errorf("could not verify the on-chain balance before accepting: %w", err)
	}
	needed := sizeSat + magmaOpenFeeReserveSat
	if capacity.AvailableSat >= needed {
		return nil
	}
	if capacity.CommittedSat > 0 {
		return fmt.Errorf(
			"not enough confirmed on-chain balance to honour this order: it needs ~%s sat, "+
				"and only %s sat of the %s sat confirmed balance is free "+
				"(%s sat is already promised to %d order(s) awaiting a channel open)",
			formatInt(needed), formatInt(capacity.AvailableSat), formatInt(capacity.ConfirmedSat),
			formatInt(capacity.CommittedSat), capacity.CommittedJobs)
	}
	return fmt.Errorf(
		"not enough confirmed on-chain balance to honour this order: it needs ~%s sat, confirmed balance is %s sat",
		formatInt(needed), formatInt(capacity.ConfirmedSat))
}

// AcceptOrder creates the invoice and hands it to Amboss. It deliberately
// re-reads the order from Amboss first: acting on a cached status could accept an
// order the buyer already withdrew.
func (s *MagmaService) AcceptOrder(ctx context.Context, orderID string) (MagmaOrder, error) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	return s.acceptOrderLocked(ctx, orderID)
}

// acceptOrderLocked requires the caller to hold workMu. Auto mode runs inside
// SyncOnce, which already holds it, and Go mutexes are not reentrant: calling
// the public method from there deadlocks the poller against itself.
func (s *MagmaService) acceptOrderLocked(ctx context.Context, orderID string) (MagmaOrder, error) {
	if err := s.requireActionMode(ctx); err != nil {
		return MagmaOrder{}, err
	}
	if s.lnd == nil {
		return MagmaOrder{}, errMagmaNoLND
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return MagmaOrder{}, err
	}
	record, err := s.execRecord(ctx, orderID)
	if err != nil {
		return MagmaOrder{}, err
	}
	if record.LocalState != magmaStateObserved {
		return MagmaOrder{}, fmt.Errorf("%w: local state is %s", errMagmaWrongState, record.LocalState)
	}

	live, err := s.liveOrder(ctx, token, record.OrderID)
	if err != nil {
		return MagmaOrder{}, err
	}
	if live.Status != "WAITING_FOR_SELLER_APPROVAL" {
		return MagmaOrder{}, fmt.Errorf("%w: Amboss reports %s", errMagmaWrongState, live.Status)
	}
	if live.RevenueSat <= 0 {
		return MagmaOrder{}, errors.New("Amboss reported a non-positive invoice amount")
	}

	// Balance is verified here, not at open time. Accepting is a promise: once the
	// buyer pays the invoice we are committed to funding the channel, and finding
	// out then that the wallet is short earns a SELLER_FAILED_TO_OPEN_CHANNEL.
	if err := s.ensureCapacityFor(ctx, live.SizeSat); err != nil {
		return MagmaOrder{}, err
	}

	// Write-ahead: if the invoice is created but sellerAcceptOrder never returns,
	// the order must not look untouched on the next attempt.
	if err := s.setLocalState(ctx, record.OrderID, magmaStateAccepting, ""); err != nil {
		return MagmaOrder{}, err
	}

	memo := fmt.Sprintf("Magma-Channel-Sale-Order-ID:%s", live.ID)
	invoice, err := s.lnd.CreateInvoice(ctx, live.RevenueSat, memo, magmaInvoiceExpirySeconds, nil)
	if err != nil {
		s.failOrder(ctx, record.OrderID, magmaStateObserved, fmt.Sprintf("failed to create invoice: %v", err))
		return MagmaOrder{}, fmt.Errorf("failed to create the invoice: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
update magma_orders set invoice_payment_request=$2, invoice_hash=$3, updated_at=now() where order_id=$1
`, record.OrderID, invoice.PaymentRequest, invoice.PaymentHash); err != nil {
		return MagmaOrder{}, err
	}

	if err := s.amboss.AcceptOrder(ctx, token, record.OrderID, invoice.PaymentRequest); err != nil {
		// The invoice stays on the node unpaid and expires on its own; going back
		// to observed lets the operator retry cleanly.
		s.failOrder(ctx, record.OrderID, magmaStateObserved, fmt.Sprintf("Amboss rejected the invoice: %v", err))
		return MagmaOrder{}, err
	}

	if err := s.setLocalState(ctx, record.OrderID, magmaStateAccepted, ""); err != nil {
		return MagmaOrder{}, err
	}
	s.appendEvent(ctx, record.OrderID, "accepted", "info",
		fmt.Sprintf("Invoice for %s sats sent to Amboss; waiting for the buyer to pay",
			formatInt(live.RevenueSat)), map[string]any{"payment_hash": invoice.PaymentHash})
	s.notifyTelegram(ctx, live, fmt.Sprintf(
		"Order %s accepted. Invoice for %s sats sent, waiting for the buyer to pay.",
		live.ID, formatInt(live.RevenueSat)))
	return s.orderByID(ctx, record.OrderID)
}

// RejectOrder declines explicitly rather than letting the order lapse.
func (s *MagmaService) RejectOrder(ctx context.Context, orderID string) (MagmaOrder, error) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	return s.rejectOrderLocked(ctx, orderID)
}

// rejectOrderLocked requires the caller to hold workMu.
func (s *MagmaService) rejectOrderLocked(ctx context.Context, orderID string) (MagmaOrder, error) {
	if err := s.requireActionMode(ctx); err != nil {
		return MagmaOrder{}, err
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return MagmaOrder{}, err
	}
	record, err := s.execRecord(ctx, orderID)
	if err != nil {
		return MagmaOrder{}, err
	}
	if record.LocalState != magmaStateObserved {
		return MagmaOrder{}, fmt.Errorf("%w: local state is %s", errMagmaWrongState, record.LocalState)
	}
	if err := s.amboss.RejectOrder(ctx, token, record.OrderID); err != nil {
		return MagmaOrder{}, err
	}
	if err := s.setLocalState(ctx, record.OrderID, magmaStateRejected, ""); err != nil {
		return MagmaOrder{}, err
	}
	s.appendEvent(ctx, record.OrderID, "rejected", "info", "Order rejected on Amboss", nil)
	return s.orderByID(ctx, record.OrderID)
}

// MagmaOpenPreview is what the operator sees before committing funds.
type MagmaOpenPreview struct {
	OrderID           string   `json:"order_id"`
	SizeSat           int64    `json:"size_sat"`
	RevenueSat        int64    `json:"revenue_sat"`
	SatPerVbyte       int64    `json:"sat_per_vbyte"`
	FastestSatPerVb   int64    `json:"fastest_sat_per_vb,omitempty"`
	HalfHourSatPerVb  int64    `json:"half_hour_sat_per_vb,omitempty"`
	HourSatPerVb      int64    `json:"hour_sat_per_vb,omitempty"`
	EstimatedFeeSat   int64    `json:"estimated_fee_sat"`
	TotalDebitSat     int64    `json:"total_debit_sat"`
	SpendableSat      int64    `json:"spendable_sat"`
	EnoughFunds       bool     `json:"enough_funds"`
	FeeShareOfRevenue int      `json:"fee_share_of_revenue_pct"`
	NetRevenueSat     int64    `json:"net_revenue_sat"`
	CanOpen           bool     `json:"can_open"`
	Warnings          []string `json:"warnings,omitempty"`
	Blockers          []string `json:"blockers,omitempty"`
}

// OpenChannelPreview estimates the on-chain cost of funding a sale and reports
// what would stop it. The fee share matters commercially: on-chain cost is paid
// by us and comes straight out of the sale.
func (s *MagmaService) OpenChannelPreview(ctx context.Context, orderID string, satPerVbyte int64) (MagmaOpenPreview, error) {
	if s.lnd == nil {
		return MagmaOpenPreview{}, errMagmaNoLND
	}
	record, err := s.execRecord(ctx, orderID)
	if err != nil {
		return MagmaOpenPreview{}, err
	}
	preview := MagmaOpenPreview{
		OrderID:    record.OrderID,
		SizeSat:    record.SizeSat,
		RevenueSat: record.RevenueSat,
	}

	var fees mempoolFeeRecommendation
	feeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	feeErr := fetchMempoolJSON(feeCtx, "https://mempool.space/api/v1/fees/recommended", &fees)
	cancel()
	if feeErr == nil {
		preview.FastestSatPerVb = int64(fees.FastestFee)
		preview.HalfHourSatPerVb = int64(fees.HalfHourFee)
		preview.HourSatPerVb = int64(fees.HourFee)
	}
	if satPerVbyte <= 0 {
		satPerVbyte = preview.FastestSatPerVb
	}
	if satPerVbyte <= 0 {
		satPerVbyte = 1
		preview.Warnings = append(preview.Warnings,
			"could not reach mempool.space for a fee estimate; using 1 sat/vB as a placeholder")
	}
	preview.SatPerVbyte = satPerVbyte

	if estimate, err := s.lnd.PreviewOpenChannel(ctx, record.SizeSat, 0, satPerVbyte); err == nil {
		preview.EstimatedFeeSat = estimate.FeeSat
		preview.TotalDebitSat = estimate.TotalDebitSat
		preview.SpendableSat = estimate.SpendableSat
		preview.EnoughFunds = estimate.EnoughFunds
		if !estimate.EnoughFunds {
			preview.Blockers = append(preview.Blockers, fmt.Sprintf(
				"not enough confirmed on-chain balance: need %s sat, have %s sat",
				formatInt(estimate.TotalDebitSat), formatInt(estimate.SpendableSat)))
		}
	} else {
		preview.Warnings = append(preview.Warnings, fmt.Sprintf("could not estimate the on-chain cost: %v", err))
	}

	if record.RevenueSat > 0 && preview.EstimatedFeeSat > 0 {
		preview.FeeShareOfRevenue = int(preview.EstimatedFeeSat * 100 / record.RevenueSat)
		preview.NetRevenueSat = record.RevenueSat - preview.EstimatedFeeSat
		if preview.FeeShareOfRevenue >= 100 {
			preview.Warnings = append(preview.Warnings,
				"the on-chain fee is larger than the whole sale; opening now loses money")
		} else if preview.FeeShareOfRevenue >= 50 {
			preview.Warnings = append(preview.Warnings, fmt.Sprintf(
				"the on-chain fee eats %d%% of this sale", preview.FeeShareOfRevenue))
		}
	}

	if record.LocalState != magmaStateAccepted {
		preview.Blockers = append(preview.Blockers, fmt.Sprintf(
			"order is in local state %s; only an accepted-and-paid order can be funded", record.LocalState))
	}
	if record.MagmaStatus != "WAITING_FOR_CHANNEL_OPEN" {
		preview.Blockers = append(preview.Blockers, fmt.Sprintf(
			"Amboss reports %s; the buyer payment must land first", record.MagmaStatus))
	}
	// An expired token blocks (the confirmation would fail outright); one that is
	// merely close to expiry only warns, because the confirmation is retried by
	// reconcileExecution once it is renewed.
	if token, err := s.token(ctx); err != nil || magmaTokenUsable(token, time.Now()) != nil {
		preview.Blockers = append(preview.Blockers,
			"the Amboss API token is missing or expired; renew it in the Fee Center")
	} else if warning := magmaTokenExpiryWarning(token, time.Now()); warning != "" {
		preview.Warnings = append(preview.Warnings, warning)
	}
	preview.CanOpen = len(preview.Blockers) == 0
	return preview, nil
}

// MagmaOpenRequest carries the operator's funding choices.
type MagmaOpenRequest struct {
	SatPerVbyte     int64    `json:"sat_per_vbyte"`
	Outpoints       []string `json:"outpoints,omitempty"`
	ConfirmPassword string   `json:"confirm_password,omitempty"`
}

// OpenChannelForOrder funds the sale. This is the only path here that spends
// on-chain, and it is guarded accordingly.
func (s *MagmaService) OpenChannelForOrder(ctx context.Context, orderID string, req MagmaOpenRequest) (MagmaOrder, error) {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	return s.openChannelForOrderLocked(ctx, orderID, req)
}

// openChannelForOrderLocked requires the caller to hold workMu.
func (s *MagmaService) openChannelForOrderLocked(ctx context.Context, orderID string, req MagmaOpenRequest) (MagmaOrder, error) {
	if err := s.requireActionMode(ctx); err != nil {
		return MagmaOrder{}, err
	}
	if s.lnd == nil {
		return MagmaOrder{}, errMagmaNoLND
	}
	if req.SatPerVbyte <= 0 {
		return MagmaOrder{}, errors.New("sat_per_vbyte must be positive")
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return MagmaOrder{}, err
	}
	record, err := s.execRecord(ctx, orderID)
	if err != nil {
		return MagmaOrder{}, err
	}

	// Anything at or past `opening` may already have a funded channel attached.
	// Reconcile against the node instead of funding a second one.
	if record.LocalState != magmaStateAccepted {
		return MagmaOrder{}, fmt.Errorf(
			"%w: local state is %s. Run a refresh so the channel state is reconciled before funding again",
			errMagmaWrongState, record.LocalState)
	}

	live, err := s.liveOrder(ctx, token, record.OrderID)
	if err != nil {
		return MagmaOrder{}, err
	}
	if live.Status != "WAITING_FOR_CHANNEL_OPEN" {
		return MagmaOrder{}, fmt.Errorf("%w: Amboss reports %s", errMagmaWrongState, live.Status)
	}
	if live.SizeSat <= 0 {
		return MagmaOrder{}, errors.New("Amboss reported a non-positive channel size")
	}
	if existing, err := s.existingChannelFor(ctx, live); err == nil && existing != "" {
		return MagmaOrder{}, fmt.Errorf(
			"a channel to this buyer already exists at %s; refresh to reconcile instead of opening another", existing)
	}

	s.connectToBuyer(ctx, token, live.BuyerPubkey)

	// Write-ahead. From here on a crash must never look like "nothing happened".
	if _, err := s.db.Exec(ctx, `
update magma_orders set local_state=$2, attempt_count=attempt_count+1, last_error='', updated_at=now()
where order_id=$1
`, record.OrderID, magmaStateOpening); err != nil {
		return MagmaOrder{}, err
	}
	s.appendEvent(ctx, record.OrderID, "opening", "info", fmt.Sprintf(
		"Opening a %s sat channel to %s at %d sat/vB",
		formatInt(live.SizeSat), magmaShortPubkey(live.BuyerPubkey), req.SatPerVbyte), nil)

	// OpenChannelWithOutpoints returns the full channel point (txid:vout), not a
	// bare txid. Storing it as a txid makes every later lookup miss, because the
	// searches append their own ":vout".
	openedPoint, err := s.lnd.OpenChannelWithOutpoints(ctx, lndclient.OpenChannelParams{
		PubkeyHex:       live.BuyerPubkey,
		LocalFundingSat: live.SizeSat,
		SatPerVbyte:     req.SatPerVbyte,
		Outpoints:       req.Outpoints,
		// Magma sells public inbound; a private channel would not satisfy the order.
		Private: false,
	})
	if err != nil {
		// The open may still have broadcast before the error surfaced, so this
		// does not go back to `accepted` where it could be funded twice.
		s.failOrder(ctx, record.OrderID, magmaStateNeedsAttention,
			fmt.Sprintf("channel open failed: %v", err))
		return MagmaOrder{}, fmt.Errorf("failed to open the channel: %w", err)
	}

	fundingTxid := magmaTxidFromPoint(openedPoint)
	if _, err := s.db.Exec(ctx, `
update magma_orders set local_state=$2, funding_txid=$3, updated_at=now() where order_id=$1
`, record.OrderID, magmaStateOpenBroadcast, fundingTxid); err != nil {
		return MagmaOrder{}, err
	}
	s.appendEvent(ctx, record.OrderID, "open_broadcast", "info",
		fmt.Sprintf("Funding transaction %s broadcast", fundingTxid), nil)

	// Best effort inline; the poller finishes the job if the outpoint is not
	// visible yet, which is normal rather than an error.
	if err := s.confirmChannelPoint(ctx, token, record.OrderID, fundingTxid); err != nil && s.logger != nil {
		s.logger.Printf("magma: order %s awaiting channel point: %v", record.OrderID, err)
	}
	return s.orderByID(ctx, record.OrderID)
}

// confirmChannelPoint resolves the funding txid to a full outpoint and reports it
// to Amboss. The vout is read from the node rather than assumed: real orders
// carry both :0 and :1.
func (s *MagmaService) confirmChannelPoint(ctx context.Context, token, orderID, fundingTxid string) error {
	channelPoint, err := s.findChannelPoint(ctx, fundingTxid)
	if err != nil {
		return err
	}
	if channelPoint == "" {
		return errors.New("channel point not visible yet")
	}
	if _, err := s.db.Exec(ctx, `
update magma_orders set local_state=$2, channel_point=$3, updated_at=now() where order_id=$1
`, orderID, magmaStateConfirming, channelPoint); err != nil {
		return err
	}
	if err := s.amboss.AddTransaction(ctx, token, orderID, channelPoint); err != nil {
		// Right after the broadcast, Amboss usually has not seen the transaction
		// yet and answers that the output does not exist. That is propagation, not
		// a wrong outpoint: the reconciliation retries and it lands within a cycle
		// or two. Alerting on it trains the operator to ignore real alerts.
		if magmaErrorIsPropagationRace(err) {
			s.appendEvent(ctx, orderID, "confirm_pending", "info", fmt.Sprintf(
				"Amboss has not seen funding transaction %s yet; retrying", fundingTxid), nil)
			return err
		}
		s.failOrder(ctx, orderID, magmaStateConfirming,
			fmt.Sprintf("failed to confirm the channel point to Amboss: %v", err))
		return err
	}
	if err := s.setLocalState(ctx, orderID, magmaStateConfirmed, ""); err != nil {
		return err
	}
	s.appendEvent(ctx, orderID, "confirmed", "info",
		fmt.Sprintf("Channel point %s confirmed to Amboss", channelPoint), nil)
	cfg := readTelegramBackupConfig()
	if cfg.configured() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID,
			fmt.Sprintf("⚡ Magma\nSale complete. Channel %s confirmed to Amboss for order %s.",
				channelPoint, orderID))
	}
	return nil
}

// findChannelPoint matches a funding txid against pending and open channels,
// mirroring how the production bot reads the outpoint back from the node.
func (s *MagmaService) findChannelPoint(ctx context.Context, fundingTxid string) (string, error) {
	if s.lnd == nil {
		return "", errMagmaNoLND
	}
	// Tolerates being handed a full channel point. Rows written before this was
	// understood store txid:vout here, and they must still reconcile.
	txid := magmaTxidFromPoint(fundingTxid)
	if txid == "" {
		return "", errors.New("funding txid required")
	}
	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return "", err
	}
	for _, channel := range pending {
		if strings.HasPrefix(channel.ChannelPoint, txid+":") {
			return channel.ChannelPoint, nil
		}
	}
	open, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return "", err
	}
	for _, channel := range open {
		if strings.HasPrefix(channel.ChannelPoint, txid+":") {
			return channel.ChannelPoint, nil
		}
	}
	return "", nil
}

// magmaErrorIsPropagationRace recognises Amboss complaining about a transaction
// or output it cannot see yet. Observed verbatim in production two seconds after
// a broadcast: "Output 1 not found in transaction", which resolved on its own
// ninety seconds later with the very same channel point.
func magmaErrorIsPropagationRace(err error) bool {
	if err == nil {
		return false
	}
	lowered := strings.ToLower(err.Error())
	return strings.Contains(lowered, "not found in transaction") ||
		strings.Contains(lowered, "transaction not found") ||
		strings.Contains(lowered, "output not found")
}

// magmaTxidFromPoint returns the transaction id from either a bare txid or a
// full channel point. LND's open call returns the latter, and the two are easy
// to confuse because both are "the thing identifying the funding".
func magmaTxidFromPoint(value string) string {
	trimmed := strings.TrimSpace(value)
	if txid, _, found := strings.Cut(trimmed, ":"); found {
		return strings.TrimSpace(txid)
	}
	return trimmed
}

// existingChannelFor reports a channel to the buyer that already matches this
// order's size, so a retry after a partial failure does not fund a duplicate.
func (s *MagmaService) existingChannelFor(ctx context.Context, order MagmaOrder) (string, error) {
	if s.lnd == nil {
		return "", errMagmaNoLND
	}
	pending, err := s.lnd.ListPendingChannels(ctx)
	if err != nil {
		return "", err
	}
	for _, channel := range pending {
		if strings.EqualFold(channel.RemotePubkey, order.BuyerPubkey) && channel.CapacitySat == order.SizeSat {
			return channel.ChannelPoint, nil
		}
	}
	open, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return "", err
	}
	for _, channel := range open {
		if strings.EqualFold(channel.RemotePubkey, order.BuyerPubkey) && channel.CapacitySat == order.SizeSat {
			return channel.ChannelPoint, nil
		}
	}
	return "", nil
}

// connectToBuyer is best effort, exactly as in the production bot: the peer may
// already be connected, and openchannel can succeed regardless.
func (s *MagmaService) connectToBuyer(ctx context.Context, token, pubkey string) {
	addresses := s.buyerAddresses(ctx, token, pubkey)
	for _, address := range addresses {
		connectCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, address, false, 30)
		cancel()
		if err == nil {
			return
		}
		if s.logger != nil {
			s.logger.Printf("magma: connect to %s via %s failed: %v", magmaShortPubkey(pubkey), address, err)
		}
	}
}

// buyerAddresses prefers our own gossip and falls back to Amboss, which may know
// a node our graph has not seen yet.
func (s *MagmaService) buyerAddresses(ctx context.Context, token, pubkey string) []string {
	seen := map[string]bool{}
	addresses := make([]string, 0, 4)
	add := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			return
		}
		seen[trimmed] = true
		addresses = append(addresses, trimmed)
	}
	if details, err := s.lnd.GetNodeDetails(ctx, pubkey); err == nil {
		for _, item := range details.Addresses {
			add(item.Addr)
		}
	}
	if remote, err := s.amboss.NodeAddresses(ctx, token, pubkey); err == nil {
		for _, item := range remote {
			add(item)
		}
	}
	return addresses
}

func (s *MagmaService) failOrder(ctx context.Context, orderID, state, message string) {
	if err := s.setLocalState(ctx, orderID, state, message); err != nil && s.logger != nil {
		s.logger.Printf("magma: failed to record error for order %s: %v", orderID, err)
	}
	level := "warning"
	if state == magmaStateNeedsAttention {
		level = "error"
	}
	s.appendEvent(ctx, orderID, "error", level, message, nil)
	cfg := readTelegramBackupConfig()
	if !cfg.configured() {
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	_ = sendTelegramMessage(sendCtx, cfg.BotToken, cfg.ChatID,
		fmt.Sprintf("⚠️ Magma\nOrder %s: %s", orderID, message))
}

// liveOrder re-reads a single order straight from Amboss. Fund-moving decisions
// never run off the local snapshot, which can be a full poll interval stale.
func (s *MagmaService) liveOrder(ctx context.Context, token, orderID string) (MagmaOrder, error) {
	orders, err := s.amboss.SellerOrders(ctx, token)
	if err != nil {
		return MagmaOrder{}, err
	}
	for _, order := range orders {
		if order.ID == orderID {
			return order, nil
		}
	}
	return MagmaOrder{}, fmt.Errorf("order %s is no longer listed on Amboss", orderID)
}

func (s *MagmaService) orderByID(ctx context.Context, orderID string) (MagmaOrder, error) {
	orders, err := s.ListOrders(ctx, 500)
	if err != nil {
		return MagmaOrder{}, err
	}
	for _, order := range orders {
		if order.ID == orderID {
			return order, nil
		}
	}
	return MagmaOrder{}, errMagmaNotFound
}

// reconcileExecution resumes orders that were interrupted between steps. This is
// what replaces the lock files the production bot uses: instead of blocking every
// future order until a file is deleted by hand, each order is picked back up from
// whatever the node and Amboss actually show.
func (s *MagmaService) reconcileExecution(ctx context.Context, token string) {
	rows, err := s.db.Query(ctx, `
select order_id, local_state, funding_txid, channel_point, buyer_pubkey, size_sat
from magma_orders
where local_state in ($1,$2,$3)
`, magmaStateOpening, magmaStateOpenBroadcast, magmaStateConfirming)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("magma: reconcile query failed: %v", err)
		}
		return
	}
	type pendingRow struct {
		orderID      string
		state        string
		fundingTxid  string
		channelPoint string
		buyerPubkey  string
		sizeSat      int64
	}
	pendingRows := make([]pendingRow, 0, 8)
	for rows.Next() {
		var row pendingRow
		if err := rows.Scan(&row.orderID, &row.state, &row.fundingTxid,
			&row.channelPoint, &row.buyerPubkey, &row.sizeSat); err != nil {
			continue
		}
		pendingRows = append(pendingRows, row)
	}
	rows.Close()

	for _, row := range pendingRows {
		switch row.state {
		case magmaStateConfirming:
			if row.channelPoint == "" {
				continue
			}
			if err := s.amboss.AddTransaction(ctx, token, row.orderID, row.channelPoint); err != nil {
				continue
			}
			_ = s.setLocalState(ctx, row.orderID, magmaStateConfirmed, "")
			s.appendEvent(ctx, row.orderID, "confirmed", "info",
				fmt.Sprintf("Channel point %s confirmed to Amboss on retry", row.channelPoint), nil)

		case magmaStateOpenBroadcast:
			if err := s.confirmChannelPoint(ctx, token, row.orderID, row.fundingTxid); err != nil && s.logger != nil {
				s.logger.Printf("magma: order %s still awaiting channel point: %v", row.orderID, err)
			}

		case magmaStateOpening:
			// We crashed or errored around OpenChannel and do not know whether a
			// transaction went out. Ask the node, never guess.
			point, err := s.existingChannelFor(ctx, MagmaOrder{
				BuyerPubkey: row.buyerPubkey,
				SizeSat:     row.sizeSat,
			})
			if err != nil || point == "" {
				continue
			}
			if _, err := s.db.Exec(ctx, `
update magma_orders set local_state=$2, channel_point=$3, funding_txid=$4, updated_at=now()
where order_id=$1
`, row.orderID, magmaStateConfirming, point, magmaTxidFromPoint(point)); err != nil {
				continue
			}
			s.appendEvent(ctx, row.orderID, "reconciled", "warning", fmt.Sprintf(
				"Found channel %s already funded for this order; resuming confirmation", point), nil)
			if err := s.amboss.AddTransaction(ctx, token, row.orderID, point); err == nil {
				_ = s.setLocalState(ctx, row.orderID, magmaStateConfirmed, "")
			}
		}
	}
}
