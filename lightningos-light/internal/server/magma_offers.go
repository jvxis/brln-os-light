package server

import (
	"context"
	"errors"
	"fmt"
)

// Offer management. The offer is what buyers see and what Amboss enforces; the
// policy is what this node can execute right now. They overlap, and the overlap
// is where a silent misconfiguration lives: a policy tighter than the offer
// accepts nothing, and nothing in either screen says so on its own.
//
// The policy deliberately stays global. Per-offer economics would duplicate what
// each order already carries: Amboss freezes the offer's terms into the order as
// locked_fee_rate, locked_min_block_length and locked_fee_rate_cap. And the
// operational limits — on-chain reserve, daily caps, concurrent opens — belong to
// the node, not to any single offer.

// MagmaOfferConflict is one way the offer and the policy disagree.
type MagmaOfferConflict struct {
	// Blocking means no order from this offer could ever be accepted in auto mode.
	Blocking bool   `json:"blocking"`
	Message  string `json:"message"`
}

// MagmaOffersView is what the offers screen renders.
type MagmaOffersView struct {
	Offers    []MagmaOffer                    `json:"offers"`
	Conflicts map[string][]MagmaOfferConflict `json:"conflicts,omitempty"`
	// ConditionOptions and OperatorOptions come from the API enums so the form
	// cannot drift from what Amboss accepts.
	ConditionOptions []string `json:"condition_options"`
	OperatorOptions  []string `json:"operator_options"`
	// ModeWarning fires when offers are live but the app is not set up to answer
	// the orders they generate.
	ModeWarning string `json:"mode_warning,omitempty"`
	// OfferStates says which disabled offers are down because we took them down,
	// which the Amboss API cannot tell apart from a manual one.
	OfferStates map[string]MagmaOfferState `json:"offer_states,omitempty"`
}

func (s *MagmaService) requireInstalled(ctx context.Context) error {
	settings, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	if !settings.Installed {
		return errors.New("Magma Inbound Sales is not installed")
	}
	return nil
}

func (s *MagmaService) magmaOfferModeWarning(ctx context.Context, offers []MagmaOffer) string {
	enabled := 0
	for _, offer := range offers {
		if offer.Status == "ENABLED" {
			enabled++
		}
	}
	if enabled == 0 {
		return ""
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		return ""
	}
	switch {
	case !settings.Enabled:
		return fmt.Sprintf(
			"%d offer(s) are live on Amboss while this app is off. Orders will arrive and nothing here will notice them; an order left unanswered is recorded against your account.",
			enabled)
	case settings.Mode == magmaModeMonitor:
		return fmt.Sprintf(
			"%d offer(s) are live on Amboss. In monitor mode you are alerted but nothing is accepted automatically, so each order needs you to act in time.",
			enabled)
	}
	return ""
}

func magmaOfferConditionOptions() []string {
	return []string{"NODE_CAPACITY", "NODE_CHANNELS", "NODE_SOCKETS", "PARALLEL_CHANNELS", "TERMINAL_WEB_RANK"}
}

func magmaOfferOperatorOptions() []string {
	return []string{
		"EQUAL_TO", "NOT_EQUAL_TO", "GREATER_THAN", "GREATER_THAN_OR_EQUAL_TO",
		"LESS_THAN", "LESS_THAN_OR_EQUAL_TO", "CONTAINS", "DOES_NOT_CONTAIN",
	}
}

// magmaOfferConflicts compares one offer against the global policy. It reports
// rather than reconciles: auto-syncing would silently overwrite a limit the
// operator set on purpose, and the useful part is naming the contradiction.
func magmaOfferConflicts(offer MagmaOffer, policy MagmaPolicy) []MagmaOfferConflict {
	conflicts := make([]MagmaOfferConflict, 0, 4)

	// Size: the offer's smallest channel must fit inside the policy window,
	// otherwise every order it produces is rejected on arrival.
	if policy.MaxChannelSizeSat > 0 && offer.MinSizeSat > policy.MaxChannelSizeSat {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"the smallest channel this offer sells (%s sat) is above the policy maximum of %s sat, so every order would be rejected",
			formatInt(offer.MinSizeSat), formatInt(policy.MaxChannelSizeSat))})
	}
	effectiveMax := offer.MaxSizeSat
	if effectiveMax == 0 {
		effectiveMax = offer.TotalSizeSat
	}
	if policy.MinChannelSizeSat > 0 && effectiveMax < policy.MinChannelSizeSat {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"the largest channel this offer sells (%s sat) is below the policy minimum of %s sat, so every order would be rejected",
			formatInt(effectiveMax), formatInt(policy.MinChannelSizeSat))})
	}
	// A partial overlap still sells, just not across the whole advertised range.
	if policy.MinChannelSizeSat > 0 && offer.MinSizeSat < policy.MinChannelSizeSat &&
		effectiveMax >= policy.MinChannelSizeSat {
		conflicts = append(conflicts, MagmaOfferConflict{Message: fmt.Sprintf(
			"orders below %s sat are advertised but the policy would reject them",
			formatInt(policy.MinChannelSizeSat))})
	}
	if policy.MaxChannelSizeSat > 0 && effectiveMax > policy.MaxChannelSizeSat &&
		offer.MinSizeSat <= policy.MaxChannelSizeSat {
		conflicts = append(conflicts, MagmaOfferConflict{Message: fmt.Sprintf(
			"orders above %s sat are advertised but the policy would reject them",
			formatInt(policy.MaxChannelSizeSat))})
	}

	// What is left to sell is a ceiling the offer itself imposes, independent of
	// the policy. Amboss keeps total_size unchanged as orders land, so an offer can
	// read ENABLED with its advertised range long since unreachable.
	if offer.TotalSizeSat > 0 && offer.SoldSat > 0 {
		if offer.MinSizeSat > 0 && offer.RemainingSat < offer.MinSizeSat {
			conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
				"only %s sat left to sell, below the %s sat smallest channel this offer advertises",
				formatInt(offer.RemainingSat), formatInt(offer.MinSizeSat))})
		} else if effectiveMax > 0 && offer.RemainingSat < effectiveMax {
			conflicts = append(conflicts, MagmaOfferConflict{Message: fmt.Sprintf(
				"only %s sat left to sell, so the largest order it can still take is %s sat, not %s sat",
				formatInt(offer.RemainingSat), formatInt(offer.RemainingSat), formatInt(effectiveMax))})
		}
	}

	// Duration, price and the routing ceiling: the offer fixes these, so a policy
	// floor above them rejects everything this offer can produce.
	if policy.MaxCommitmentDays > 0 && offer.MinBlockLength > policy.MaxCommitmentDays*144 {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"the offer commits the channel for %d days, above the policy maximum of %d",
			offer.MinBlockLength/144, policy.MaxCommitmentDays)})
	}
	if policy.MinPricePPM > 0 && offer.FeeRatePPM > 0 && offer.FeeRatePPM < policy.MinPricePPM {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"the offer prices at %d ppm, below the policy minimum of %d ppm",
			offer.FeeRatePPM, policy.MinPricePPM)})
	}
	if policy.MinPricePPMPerDay > 0 && offer.MinBlockLength > 0 && offer.FeeRatePPM > 0 {
		perDay := float64(offer.FeeRatePPM) / (float64(offer.MinBlockLength) / 144.0)
		if perDay < float64(policy.MinPricePPMPerDay) {
			conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
				"the offer works out to %.1f ppm/day, below the policy minimum of %d",
				perDay, policy.MinPricePPMPerDay)})
		}
	}
	// A zero cap is a real setting on Amboss, not an unset one: the channel is
	// contractually held at 0 ppm for the whole commitment. It is a legitimate
	// choice - a loss leader, a favour, a test - so it is not blocked. But it is
	// the single most expensive value in this form, so it always says so.
	// Only the rate cap warns. A zero base fee cap is ordinary - it is what most
	// real orders carry - while a zero rate cap is what actually zeroes the
	// revenue for the length of the commitment.
	if offer.FeeRateCapPPM == 0 {
		conflicts = append(conflicts, MagmaOfferConflict{Message: fmt.Sprintf(
			"routing fee capped at 0 ppm: every channel this offer sells routes for "+
				"free until its commitment ends (%d days)", offer.MinBlockLength/144)})
	}
	// The >0 guard that used to sit here read a zero cap as "not informed" and so
	// skipped the very value the floor exists to catch.
	if policy.MinFeeRateCapPPM > 0 && offer.FeeRateCapPPM < policy.MinFeeRateCapPPM {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"the offer caps our routing fee at %d ppm, below the policy minimum of %d ppm",
			offer.FeeRateCapPPM, policy.MinFeeRateCapPPM)})
	}

	// Stock the wallet cannot back is not a policy conflict, but it is the same
	// class of surprise: advertised and undeliverable.
	if policy.MaxDailySizeSat > 0 && offer.MinSizeSat > policy.MaxDailySizeSat {
		conflicts = append(conflicts, MagmaOfferConflict{Blocking: true, Message: fmt.Sprintf(
			"a single channel from this offer (%s sat) exceeds the policy daily cap of %s sat",
			formatInt(offer.MinSizeSat), formatInt(policy.MaxDailySizeSat))})
	}
	return conflicts
}

func (s *MagmaService) ListOffers(ctx context.Context) (MagmaOffersView, error) {
	view := MagmaOffersView{
		ConditionOptions: magmaOfferConditionOptions(),
		OperatorOptions:  magmaOfferOperatorOptions(),
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return view, err
	}
	offers, err := s.amboss.Offers(ctx, token)
	if err != nil {
		return view, err
	}
	view.Offers = offers

	policy, err := s.loadPolicy(ctx)
	if err != nil {
		return view, nil
	}
	// An enabled offer produces orders whether or not anything is watching. In
	// off or monitor mode nobody reacts, and an unanswered order becomes
	// SELLER_FAILED_TO_REACT on the account.
	view.ModeWarning = s.magmaOfferModeWarning(ctx, offers)

	conflicts := make(map[string][]MagmaOfferConflict)
	for _, offer := range offers {
		// Disabled offers cannot produce orders, so a mismatch there is noise.
		if offer.Status != "ENABLED" {
			continue
		}
		if found := magmaOfferConflicts(offer, policy); len(found) > 0 {
			conflicts[offer.ID] = found
		}
	}
	if len(conflicts) > 0 {
		view.Conflicts = conflicts
	}

	if states := s.loadOfferStates(ctx); len(states) > 0 {
		// An offer still down while the balance looks healthy is waiting on the
		// share other offers have already claimed; saying so beats an operator
		// wondering why a funded node keeps an offer suspended.
		if capacity, err := s.Capacity(ctx); err == nil {
			budget := capacity.AvailableSat - policy.MinOnchainReserve
			for id, state := range states {
				if state.AutoDisabledAt == nil {
					continue
				}
				for _, offer := range offers {
					if offer.ID == id && magmaOfferRemaining(offer) <= budget {
						state.HeldForOthers = true
						states[id] = state
					}
				}
			}
		}
		view.OfferStates = states
	}
	return view, nil
}

// SaveOffer creates or updates depending on whether an id is present.
func (s *MagmaService) SaveOffer(ctx context.Context, offer MagmaOffer) (MagmaOffersView, error) {
	// Editing an offer spends nothing, so it is allowed in every mode - it is
	// configuration, not execution. The risk of an enabled offer with nobody
	// reacting is surfaced as a warning instead (see magmaOfferModeWarning).
	if err := s.requireInstalled(ctx); err != nil {
		return MagmaOffersView{}, err
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return MagmaOffersView{}, err
	}
	if offer.ID == "" {
		if err := s.amboss.CreateOffer(ctx, token, offer); err != nil {
			return MagmaOffersView{}, err
		}
	} else if err := s.amboss.UpdateOffer(ctx, token, offer); err != nil {
		return MagmaOffersView{}, err
	}
	s.signal()
	return s.ListOffers(ctx)
}

// errMagmaOfferConflicts is returned when enabling an offer the policy would
// refuse orders from. It carries the reasons so the caller can show them.
type errMagmaOfferConflicts struct {
	Conflicts []MagmaOfferConflict
}

func (e *errMagmaOfferConflicts) Error() string {
	return "offer conflicts with the current policy"
}

// ToggleOffer flips an offer between enabled and disabled.
//
// Enabling is the direction that can hurt: an offer whose terms the policy would
// refuse still gets published on Amboss, and every order it attracts is rejected
// or left to expire, which the account carries as seller failures. So enabling a
// conflicting offer needs confirm=true; disabling never asks, because taking a
// bad offer down is always safe.
func (s *MagmaService) ToggleOffer(ctx context.Context, offerID string, confirm bool) (MagmaOffersView, error) {
	if err := s.requireInstalled(ctx); err != nil {
		return MagmaOffersView{}, err
	}
	token, err := s.usableToken(ctx)
	if err != nil {
		return MagmaOffersView{}, err
	}
	if !confirm {
		if conflicts, err := s.offerEnableConflicts(ctx, token, offerID); err == nil && len(conflicts) > 0 {
			return MagmaOffersView{}, &errMagmaOfferConflicts{Conflicts: conflicts}
		}
	}
	if err := s.amboss.ToggleOffer(ctx, token, offerID); err != nil {
		return MagmaOffersView{}, err
	}
	// The operator decided this offer's state by hand, so the balance automation
	// must not undo it. Clearing the mark covers both directions: a manual enable
	// escapes the automation, and a manual disable is not "ours" to restore.
	s.clearOfferAutoDisable(ctx, offerID)
	s.signal()
	return s.ListOffers(ctx)
}

// offerEnableConflicts returns the policy conflicts of an offer that is about to
// be enabled. An offer already enabled is on its way down, so it reports none.
func (s *MagmaService) offerEnableConflicts(ctx context.Context, token, offerID string) ([]MagmaOfferConflict, error) {
	offers, err := s.amboss.Offers(ctx, token)
	if err != nil {
		return nil, err
	}
	policy, err := s.loadPolicy(ctx)
	if err != nil {
		return nil, err
	}
	for _, offer := range offers {
		if offer.ID != offerID {
			continue
		}
		if offer.Status == "ENABLED" {
			return nil, nil
		}
		return magmaOfferConflicts(offer, policy), nil
	}
	return nil, nil
}
