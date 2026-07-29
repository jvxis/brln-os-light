package server

import "testing"

// LND's open call returns a full channel point, not a bare txid. Treating one as
// the other is silent: the value stores fine, and only the later lookup fails,
// because it appends its own ":vout" and can never match. This helper has to
// accept both forms so rows written before that was understood still reconcile.
func TestMagmaTxidFromPoint(t *testing.T) {
	const txid = "81e07dabc522e814ff68d4d8882a03ef6df68bc451f2e6aa4942c88fc413b73b"
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "channel point with vout 1", value: txid + ":1", want: txid},
		{name: "channel point with vout 0", value: txid + ":0", want: txid},
		{name: "already a bare txid", value: txid, want: txid},
		{name: "surrounding whitespace", value: "  " + txid + ":1  ", want: txid},
		{name: "empty", value: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaTxidFromPoint(tc.value); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
