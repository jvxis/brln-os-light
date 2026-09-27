package lndclient

import (
	"errors"
	"fmt"

	"lightningos-light/lnrpc"
)

// PolicyUpdateError means LND rejected at least one update. A global request
// may have applied to other channels; callers must not infer rollback or retry
// the entire request blindly. Raw daemon error text is deliberately excluded.
type PolicyUpdateError struct {
	FailedUpdates int
	Reasons       map[string]int
}

func (e *PolicyUpdateError) Error() string {
	return fmt.Sprintf("LND rejected %d channel policy update(s): %v", e.FailedUpdates, e.Reasons)
}

func policyUpdateFailureReasons(resp *lnrpc.PolicyUpdateResponse) map[string]int {
	if len(resp.GetFailedUpdates()) == 0 {
		return nil
	}
	reasons := make(map[string]int)
	for _, failure := range resp.GetFailedUpdates() {
		// A nil entry or a future enum is still a failure, not a success.
		reason := "unknown"
		switch failure.GetReason() {
		case lnrpc.UpdateFailure_UPDATE_FAILURE_PENDING:
			reason = "pending"
		case lnrpc.UpdateFailure_UPDATE_FAILURE_NOT_FOUND:
			reason = "not_found"
		case lnrpc.UpdateFailure_UPDATE_FAILURE_INTERNAL_ERR:
			reason = "internal"
		case lnrpc.UpdateFailure_UPDATE_FAILURE_INVALID_PARAMETER:
			reason = "invalid_parameter"
		}
		reasons[reason]++
	}
	return reasons
}

func policyUpdateResult(resp *lnrpc.PolicyUpdateResponse, err error) error {
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("LND returned no channel policy update response")
	}
	if len(resp.FailedUpdates) > 0 {
		return &PolicyUpdateError{FailedUpdates: len(resp.FailedUpdates), Reasons: policyUpdateFailureReasons(resp)}
	}
	return nil
}
