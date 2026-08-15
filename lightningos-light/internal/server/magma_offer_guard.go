package server

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// An offer published on Amboss is a promise to open channels on demand. If the
// wallet cannot cover what an offer still has to sell, every order it attracts
// ends as a seller failure on the account - the expensive kind of mistake,
// because the reputation cost outlives the missed sale.
//
// This guard takes such offers down and puts them back up when the balance
// returns. It only ever touches offers it took down itself: an offer the
// operator disabled by hand stays down, and one they enabled by hand stays up.

// MagmaOfferState is the local memory Amboss cannot provide. The API reports
// whether an offer is enabled, never why, so "disabled by the operator" and
// "disabled by us for lack of funds" are indistinguishable without this.
type MagmaOfferState struct {
	OfferID        string     `json:"offer_id"`
	AutoDisabledAt *time.Time `json:"auto_disabled_at,omitempty"`
	AvailableSat   int64      `json:"available_sat,omitempty"`
	RequiredSat    int64      `json:"required_sat,omitempty"`
	// HeldForOthers marks an offer still down because the balance that returned
	// is already spoken for by other offers, rather than absent altogether.
	HeldForOthers bool `json:"held_for_others,omitempty"`
}

func (s *MagmaService) markOfferAutoDisabled(ctx context.Context, offerID string, availableSat, requiredSat int64) {
	if _, err := s.db.Exec(ctx, `
insert into magma_offer_state (offer_id, auto_disabled_at, available_sat, required_sat, updated_at)
values ($1, now(), $2, $3, now())
on conflict (offer_id) do update set
  auto_disabled_at = coalesce(magma_offer_state.auto_disabled_at, now()),
  available_sat = excluded.available_sat,
  required_sat = excluded.required_sat,
  updated_at = now()
`, offerID, availableSat, requiredSat); err != nil && s.logger != nil {
		s.logger.Printf("magma: could not record the auto-disable of offer %s: %v", offerID, err)
	}
}

// clearOfferAutoDisable removes our claim over an offer. Called whenever the
// operator toggles it by hand, in either direction: their intent replaces ours.
func (s *MagmaService) clearOfferAutoDisable(ctx context.Context, offerID string) {
	if s == nil || s.db == nil {
		return
	}
	if _, err := s.db.Exec(ctx,
		`delete from magma_offer_state where offer_id=$1`, offerID); err != nil && s.logger != nil {
		s.logger.Printf("magma: could not clear the auto-disable mark of offer %s: %v", offerID, err)
	}
}

func (s *MagmaService) loadOfferStates(ctx context.Context) map[string]MagmaOfferState {
	states := make(map[string]MagmaOfferState, 4)
	if s == nil || s.db == nil {
		return states
	}
	rows, err := s.db.Query(ctx,
		`select offer_id, auto_disabled_at, available_sat, required_sat from magma_offer_state`)
	if err != nil {
		return states
	}
	defer rows.Close()
	for rows.Next() {
		var state MagmaOfferState
		if err := rows.Scan(&state.OfferID, &state.AutoDisabledAt,
			&state.AvailableSat, &state.RequiredSat); err != nil {
			continue
		}
		states[state.OfferID] = state
	}
	return states
}

// magmaOfferRemaining is what an offer still has to sell. Amboss leaves
// total_size untouched as orders land, so the outstanding promise is the total
// minus what its orders already locked.
func magmaOfferRemaining(offer MagmaOffer) int64 {
	remaining := offer.RemainingSat
	if remaining <= 0 && offer.SoldSat == 0 {
		remaining = offer.TotalSizeSat
	}
	if remaining < 0 {
		return 0
	}
	return remaining
}

// syncOfferBalanceGuard is the sweep. It runs in auto mode only: taking an
// offer down without being asked is the kind of thing auto mode exists to do,
// and the kind of surprise the other modes should not spring on an operator who
// expects to approve each step.
func (s *MagmaService) syncOfferBalanceGuard(ctx context.Context, token string) {
	// A sale in flight makes the wallet an unreliable witness. Between broadcast
	// and confirmation the inputs are gone from the confirmed balance while the
	// change has not landed yet, so the balance reads far lower than it is. On
	// 2026-08-15 that took a 8,071,425 sat offer down for four minutes: 4,844,175
	// sat of our own change was invisible, and the offer came back untouched the
	// moment the funding transaction confirmed.
	//
	// Beyond the arithmetic, taking an offer off the market in the middle of
	// selling through it is simply the wrong moment to do anything.
	if s.saleInFlight(ctx) {
		return
	}
	policy, err := s.loadPolicy(ctx)
	if err != nil {
		return
	}
	capacity, err := s.Capacity(ctx)
	if err != nil {
		// Without a balance reading there is no basis to disable anything, and
		// guessing would take live offers down over a transient RPC failure.
		return
	}
	// Count the unconfirmed too. Any channel open - Magma or not - spends
	// confirmed inputs and returns the change unconfirmed, so a confirmed-only
	// view reports a collapse on every single open. Being generous here is safe:
	// the per-order accept decision still runs on AvailableSat, which is not.
	availableSat := capacity.AvailableSat + capacity.UnconfirmedSat
	offers, err := s.amboss.Offers(ctx, token)
	if err != nil {
		return
	}
	states := s.loadOfferStates(ctx)

	// Everything is measured against the balance left after the reserve the
	// operator asked to keep untouched.
	budget := availableSat - policy.MinOnchainReserve
	if budget < 0 {
		budget = 0
	}

	enabled := make([]MagmaOffer, 0, len(offers))
	suspended := make([]MagmaOffer, 0, len(offers))
	for _, offer := range offers {
		switch {
		case offer.Status == "ENABLED":
			enabled = append(enabled, offer)
		case states[offer.ID].AutoDisabledAt != nil:
			suspended = append(suspended, offer)
		}
	}

	// Keep the offers that fit, smallest first: that saves as many as the balance
	// can honour instead of letting one oversized offer sink the rest.
	sort.Slice(enabled, func(i, j int) bool {
		left, right := magmaOfferRemaining(enabled[i]), magmaOfferRemaining(enabled[j])
		if left != right {
			return left < right
		}
		return enabled[i].ID < enabled[j].ID
	})
	for _, offer := range enabled {
		remaining := magmaOfferRemaining(offer)
		if remaining <= budget {
			budget -= remaining
			continue
		}
		required := remaining + policy.MinOnchainReserve
		if err := s.amboss.ToggleOffer(ctx, token, offer.ID); err != nil {
			if s.logger != nil {
				s.logger.Printf("magma: could not disable offer %s for lack of funds: %v", offer.ID, err)
			}
			continue
		}
		s.markOfferAutoDisabled(ctx, offer.ID, availableSat, required)
		s.notifyOfferGuard(ctx, offer, "disabled", remaining, policy.MinOnchainReserve,
			required, availableSat)
	}

	// Restore in a fixed order so the same balance always brings back the same
	// offers: longest suspended first, then the cheapest to cover, then the id.
	sort.Slice(suspended, func(i, j int) bool {
		leftAt, rightAt := states[suspended[i].ID].AutoDisabledAt, states[suspended[j].ID].AutoDisabledAt
		if leftAt != nil && rightAt != nil && !leftAt.Equal(*rightAt) {
			return leftAt.Before(*rightAt)
		}
		left, right := magmaOfferRemaining(suspended[i]), magmaOfferRemaining(suspended[j])
		if left != right {
			return left < right
		}
		return suspended[i].ID < suspended[j].ID
	})
	for _, offer := range suspended {
		remaining := magmaOfferRemaining(offer)
		if remaining > budget {
			// Still short. The mark stays so the offer keeps its place in line.
			continue
		}
		// The policy may have moved while the offer was down; bringing back one
		// that now produces only rejectable orders would trade a funding problem
		// for a reputation one.
		if magmaOfferHasBlockingConflict(magmaOfferConflicts(offer, policy)) {
			continue
		}
		if err := s.amboss.ToggleOffer(ctx, token, offer.ID); err != nil {
			if s.logger != nil {
				s.logger.Printf("magma: could not re-enable offer %s: %v", offer.ID, err)
			}
			continue
		}
		budget -= remaining
		s.clearOfferAutoDisable(ctx, offer.ID)
		s.notifyOfferGuard(ctx, offer, "enabled", remaining, policy.MinOnchainReserve,
			remaining+policy.MinOnchainReserve, availableSat)
	}
}

// magmaSaleInFlightStates covers everything from the moment an order is accepted
// until the channel is confirmed. magmaCommittedStates stops at "opening", which
// is exactly one step too early: open_broadcast and confirming are the window
// where the change is still unconfirmed.
var magmaSaleInFlightStates = []string{
	magmaStateAccepting, magmaStateAccepted, magmaStateOpening,
	magmaStateOpenBroadcast, magmaStateConfirming,
}

func (s *MagmaService) saleInFlight(ctx context.Context) bool {
	var inFlight bool
	if err := s.db.QueryRow(ctx, `
select exists(
  select 1 from magma_orders
  where local_state = any($1) and not (magma_status = any($2))
)`, magmaSaleInFlightStates, magmaTerminalStatusList()).Scan(&inFlight); err != nil {
		// Unreadable state is not a licence to act: hold the offers as they are.
		return true
	}
	return inFlight
}

func magmaOfferHasBlockingConflict(conflicts []MagmaOfferConflict) bool {
	for _, conflict := range conflicts {
		if conflict.Blocking {
			return true
		}
	}
	return false
}

func (s *MagmaService) notifyOfferGuard(ctx context.Context, offer MagmaOffer, action string,
	remaining, reserve, required, available int64) {
	var header string
	if action == "disabled" {
		header = fmt.Sprintf(
			"Offer disabled automatically: the on-chain balance no longer covers the %s sat it still has to sell",
			formatInt(remaining))
	} else {
		header = "Offer re-enabled automatically: the on-chain balance covers it again"
	}
	body := fmt.Sprintf("%s\nOffer remaining: %s sat\nPolicy reserve: %s sat\nRequired: %s sat\nAvailable: %s sat",
		header, formatInt(remaining), formatInt(reserve), formatInt(required), formatInt(available))

	if s.logger != nil {
		s.logger.Printf("magma: offer %s %s automatically (remaining=%d required=%d available=%d)",
			offer.ID, action, remaining, required, available)
	}
	// Offers have no order to hang an event on, so this goes straight to Telegram.
	s.notifyTelegram(ctx, MagmaOrder{}, body)
}
