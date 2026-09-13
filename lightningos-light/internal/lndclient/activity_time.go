package lndclient

import "time"

// Live activity includes transactions already known by LND whose timestamp is
// slightly ahead of the node clock. Historical ranges retain their exact end.
// Preserve the original timestamp for display; do not change transaction data.
func onchainActivityTimeInRange(timestamp, start, end, now time.Time) bool {
	if timestamp.Before(start) {
		return false
	}
	if !timestamp.After(end) {
		return true
	}
	live := !end.Before(now.Add(-time.Minute)) && !end.After(now.Add(time.Minute))
	return live && !timestamp.After(now.Add(2*time.Hour))
}
