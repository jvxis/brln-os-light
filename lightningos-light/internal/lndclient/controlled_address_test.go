package lndclient

import (
	"lightningos-light/lnrpc/walletrpc"
	"testing"
)

func TestControlledDefaultWalletAddressExcludesImportedAndWatchOnly(t *testing.T) {
	fixture := func() ([]*walletrpc.Account, []*walletrpc.AccountWithAddresses) {
		return []*walletrpc.Account{{Name: "default", DerivationPath: "m/84'/0'/0'"}}, []*walletrpc.AccountWithAddresses{{Name: "default", DerivationPath: "m/84'/0'/0'", Addresses: []*walletrpc.AddressProperty{{Address: "fixture-address", DerivationPath: "m/84'/0'/0'/0/1", PublicKey: make([]byte, 33)}}}}
	}
	accounts, groups := fixture()
	if !controlledDefaultWalletAddress("fixture-address", accounts, groups) {
		t.Fatal("controlled default address rejected")
	}
	for _, mutate := range []func([]*walletrpc.Account, []*walletrpc.AccountWithAddresses){
		func(a []*walletrpc.Account, _ []*walletrpc.AccountWithAddresses) { a[0].WatchOnly = true },
		func(a []*walletrpc.Account, g []*walletrpc.AccountWithAddresses) {
			a[0].Name = "imported"
			g[0].Name = "imported"
		},
		func(_ []*walletrpc.Account, g []*walletrpc.AccountWithAddresses) {
			g[0].Addresses[0].DerivationPath = ""
		},
		func(_ []*walletrpc.Account, g []*walletrpc.AccountWithAddresses) { g[0].Addresses[0].PublicKey = nil },
		func(_ []*walletrpc.Account, g []*walletrpc.AccountWithAddresses) {
			g[0].DerivationPath = "other-account"
		},
	} {
		a, g := fixture()
		mutate(a, g)
		if controlledDefaultWalletAddress("fixture-address", a, g) {
			t.Fatal("unverified/imported/watch-only ownership exempted")
		}
	}
	if controlledDefaultWalletAddress("external-address", accounts, groups) {
		t.Fatal("external address exempted")
	}
}
