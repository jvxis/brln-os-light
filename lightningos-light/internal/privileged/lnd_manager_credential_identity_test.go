package privileged

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type lndServiceIdentityRunner struct {
	user  string
	group string
	err   error
}

func (runner *lndServiceIdentityRunner) Run(_ context.Context, path string, args ...string) (string, error) {
	if path != systemctlPath || len(args) != 4 || args[0] != "show" || args[2] != "--value" || args[3] != defaultLNDServiceUnit {
		return "", errors.New("unexpected fixed command")
	}
	if runner.err != nil {
		return "", runner.err
	}
	switch args[1] {
	case "--property=User":
		return runner.user + "\n", nil
	case "--property=Group":
		return runner.group + "\n", nil
	default:
		return "", errors.New("unexpected property")
	}
}

func TestLNDManagerCredentialUsesFixedServiceIdentity(t *testing.T) {
	manager := &NativeLNDManagerCredentialManager{
		lndServiceUnit: defaultLNDServiceUnit,
		runner:         &lndServiceIdentityRunner{user: "admin", group: "admin"},
		lookupIdentity: func(name string) (int, int, error) {
			if name != "admin" {
				return 0, 0, fmt.Errorf("unexpected user %q", name)
			}
			return 1000, 1000, nil
		},
		lookupGroupGID: func(name string) (int, error) {
			if name != "admin" {
				return 0, fmt.Errorf("unexpected group %q", name)
			}
			return 1000, nil
		},
	}
	uid, gid, err := manager.resolveLNDServiceIdentity(context.Background())
	if err != nil || uid != 1000 || gid != 1000 {
		t.Fatalf("existing-node service identity was not accepted: uid=%d gid=%d err=%v", uid, gid, err)
	}
}

func TestLNDManagerCredentialUsesServicePrimaryGroupWhenUnset(t *testing.T) {
	manager := &NativeLNDManagerCredentialManager{
		lndServiceUnit: defaultLNDServiceUnit,
		runner:         &lndServiceIdentityRunner{user: "lnd"},
		lookupIdentity: func(string) (int, int, error) { return 1100, 1200, nil },
		lookupGroupGID: func(string) (int, error) {
			t.Fatal("group lookup must not run when systemd Group= is empty")
			return 0, nil
		},
	}
	uid, gid, err := manager.resolveLNDServiceIdentity(context.Background())
	if err != nil || uid != 1100 || gid != 1200 {
		t.Fatalf("primary service group was not used: uid=%d gid=%d err=%v", uid, gid, err)
	}
}

func TestLNDManagerCredentialRejectsUnsafeServiceIdentity(t *testing.T) {
	for _, test := range []struct {
		name  string
		user  string
		group string
	}{
		{name: "missing user", group: "lnd"},
		{name: "root user", user: "root", group: "root"},
		{name: "invalid user", user: "../../admin", group: "admin"},
		{name: "root group", user: "lnd", group: "root"},
		{name: "invalid group", user: "lnd", group: "../admin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := &NativeLNDManagerCredentialManager{
				lndServiceUnit: defaultLNDServiceUnit,
				runner:         &lndServiceIdentityRunner{user: test.user, group: test.group},
				lookupIdentity: func(string) (int, int, error) { return 1000, 1000, nil },
				lookupGroupGID: func(string) (int, error) { return 1000, nil },
			}
			if _, _, err := manager.resolveLNDServiceIdentity(context.Background()); err == nil {
				t.Fatal("unsafe service identity was accepted")
			}
		})
	}
}
