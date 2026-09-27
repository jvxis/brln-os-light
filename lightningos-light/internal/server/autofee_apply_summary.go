package server

// recordAppliedDecision counts acknowledged changes (or explicitly simulated
// dry-run decisions). Rejected/transport-failed attempts only count as errors.
func (s *autofeeRunSummary) recordAppliedDecision(d *decision) {
	s.applied++
	if d.NewPpm > d.LocalPpm {
		s.changedUp++
	} else if d.NewPpm < d.LocalPpm {
		s.changedDown++
	} else {
		s.kept++
	}
}
