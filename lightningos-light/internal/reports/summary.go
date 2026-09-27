package reports

// Sum normalized daily rows, preserving the existing per-day sat rounding.
func summarizeRows(items []Row) Summary {
	var totals Metrics
	for _, row := range items {
		m := row.Metrics
		totals.ForwardFeeRevenueSat += m.ForwardFeeRevenueSat
		totals.ForwardFeeRevenueMsat += m.ForwardFeeRevenueMsat
		totals.RebalanceFeeCostSat += m.RebalanceFeeCostSat
		totals.RebalanceFeeCostMsat += m.RebalanceFeeCostMsat
		totals.PaymentFeeCostSat += m.PaymentFeeCostSat
		totals.PaymentFeeCostMsat += m.PaymentFeeCostMsat
		totals.OnchainFeeCostSat += m.OnchainFeeCostSat
		totals.OnchainFeeCostMsat += m.OnchainFeeCostMsat
		totals.OnchainCoopCloseCostSat += m.OnchainCoopCloseCostSat
		totals.OnchainCoopCloseCostMsat += m.OnchainCoopCloseCostMsat
		totals.OnchainLocalForceCostSat += m.OnchainLocalForceCostSat
		totals.OnchainLocalForceCostMsat += m.OnchainLocalForceCostMsat
		totals.OnchainRemoteForceCostSat += m.OnchainRemoteForceCostSat
		totals.OnchainRemoteForceCostMsat += m.OnchainRemoteForceCostMsat
		totals.KeysendReceivedSat += m.KeysendReceivedSat
		totals.KeysendReceivedMsat += m.KeysendReceivedMsat
		totals.KeysendReceivedCount += m.KeysendReceivedCount
		totals.KeysendSentSat += m.KeysendSentSat
		totals.KeysendSentMsat += m.KeysendSentMsat
		totals.KeysendSentCount += m.KeysendSentCount
		totals.MarkedRevenueSat += m.MarkedRevenueSat
		totals.MarkedRevenueMsat += m.MarkedRevenueMsat
		totals.MarkedRevenueCount += m.MarkedRevenueCount
		totals.MarkedCostSat += m.MarkedCostSat
		totals.MarkedCostMsat += m.MarkedCostMsat
		totals.MarkedCostCount += m.MarkedCostCount
		totals.SalesRevenueSat += m.SalesRevenueSat
		totals.SalesRevenueMsat += m.SalesRevenueMsat
		totals.SalesCount += m.SalesCount
		totals.NetRoutingProfitSat += m.NetRoutingProfitSat
		totals.NetRoutingProfitMsat += m.NetRoutingProfitMsat
		totals.NetWithKeysendSat += m.NetWithKeysendSat
		totals.NetWithKeysendMsat += m.NetWithKeysendMsat
		totals.NetTotalSat += m.NetTotalSat
		totals.NetTotalMsat += m.NetTotalMsat
		totals.ForwardCount += m.ForwardCount
		totals.RebalanceCount += m.RebalanceCount
		totals.RebalanceVolumeSat += m.RebalanceVolumeSat
		totals.RebalanceVolumeMsat += m.RebalanceVolumeMsat
		totals.PaymentCount += m.PaymentCount
		totals.RoutedVolumeSat += m.RoutedVolumeSat
		totals.RoutedVolumeMsat += m.RoutedVolumeMsat
	}
	days := int64(len(items))
	return Summary{Days: days, Totals: totals, Averages: averageMetrics(totals, days)}
}
