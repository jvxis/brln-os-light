package server

import "testing"

// The memo is the only link between a settled invoice on the node and a Magma
// order. Matching it loosely would attribute somebody else's payment to a sale.
func TestMagmaOrderIDFromMemo(t *testing.T) {
	cases := []struct {
		name string
		memo string
		want string
	}{
		{
			name: "memo written by this app and by the original script",
			memo: "Magma-Channel-Sale-Order-ID:99682ca1-84c0-4174-8227-2555ca37177d",
			want: "99682ca1-84c0-4174-8227-2555ca37177d",
		},
		{
			name: "surrounding whitespace is tolerated",
			memo: "  Magma-Channel-Sale-Order-ID: abc-123  ",
			want: "abc-123",
		},
		{name: "unrelated invoice", memo: "coffee"},
		{name: "empty memo", memo: ""},
		// A prefix that merely mentions Magma is not the structured memo.
		{name: "prefix appears mid-string", memo: "re: Magma-Channel-Sale-Order-ID:abc"},
		{name: "similar but different prefix", memo: "Magma-Channel-Sale-Order:abc"},
		{name: "prefix with no id", memo: "Magma-Channel-Sale-Order-ID:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := magmaOrderIDFromMemo(tc.memo); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
