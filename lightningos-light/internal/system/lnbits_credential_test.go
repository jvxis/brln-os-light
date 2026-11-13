package system

import (
	"context"
	"testing"
)

type lnbitsRevocationClient struct {
	*fakePrivilegedServiceClient
	revokes      int
	revokeDryRun bool
}

func (c *lnbitsRevocationClient) RevokeLNbitsLegacyCredential(_ context.Context, dryRun bool) error {
	c.revokes++
	c.revokeDryRun = dryRun
	return c.err
}

func TestLNbitsCredentialRevocationRequiresEnforceAndPreflightsRestart(t *testing.T) {
	for _, mode := range []string{"off", "shadow", "enforce"} {
		t.Run(mode, func(t *testing.T) {
			c := &lnbitsRevocationClient{fakePrivilegedServiceClient: &fakePrivilegedServiceClient{mode: mode}}
			ConfigurePrivilegedClient(c)
			t.Cleanup(func() { ConfigurePrivilegedClient(nil) })
			err := RevokeLNbitsLegacyCredentialWithBroker(context.Background(), true)
			if mode != "enforce" {
				if err == nil || c.revokes != 0 || c.appCalls != 0 {
					t.Fatal("non-enforce attempted migration")
				}
				return
			}
			if err != nil || c.revokes != 1 || !c.revokeDryRun || c.appCalls != 1 || !c.appDryRun || c.appID != "lnbits" || c.action != "start" {
				t.Fatalf("preflight incorrect: %v", err)
			}
			if err := RevokeLNbitsLegacyCredentialWithBroker(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			if c.revokes != 2 || c.revokeDryRun || c.appCalls != 1 {
				t.Fatal("revocation restarted app or was dry run")
			}
		})
	}
}
