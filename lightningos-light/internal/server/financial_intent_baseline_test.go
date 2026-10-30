package server

import (
	"testing"
	"time"
)

func TestFinancialIntentBroadGrantCannotApproveArbitraryTransfers(t *testing.T) {
	s, rpc, session := onchainReauthServer(t, "external")
	s.auth.sessions[session.ID].ReauthScopes[authScopeWalletSendExternal] = time.Now().Add(time.Minute)
	for _, body := range []string{
		`{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000,"sat_per_vbyte":1}`,
		`{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":5000,"sat_per_vbyte":1}`,
		`{"address":"1BoatSLRHtKNngkdXEeobR76b53LETtpyT","amount_sat":1000,"sat_per_vbyte":20}`,
	} {
		rec := onchainReauthRequest(s, session, "/api/wallet/send", body, true)
		if rec.Code != 428 {
			t.Errorf("a reusable scope authorized a transfer without its immutable intent: status=%d", rec.Code)
		}
	}
	if rpc.sends.Load() != 0 {
		t.Errorf("unbound authorization reached %d financial RPCs", rpc.sends.Load())
	}
}
