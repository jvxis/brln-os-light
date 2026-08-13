//go:build linux

package privileged

import (
	"errors"
	"testing"
)

func TestAuthorizeCaller(t *testing.T) {
	lookup := func() (string, error) { return "1001", nil }
	tests := []struct {
		name      string
		euid      int
		sudoUID   string
		sudoUser  string
		lookup    func() (string, error)
		want      string
		wantError bool
	}{
		{name: "direct root", euid: 0, lookup: lookup, want: "root-direct"},
		{name: "authorized sudo", euid: 0, sudoUID: "1001", sudoUser: ExpectedCaller, lookup: lookup, want: ExpectedCaller},
		{name: "not root", euid: 1001, sudoUID: "1001", sudoUser: ExpectedCaller, lookup: lookup, wantError: true},
		{name: "wrong user", euid: 0, sudoUID: "1001", sudoUser: "www-data", lookup: lookup, wantError: true},
		{name: "uid mismatch", euid: 0, sudoUID: "1002", sudoUser: ExpectedCaller, lookup: lookup, wantError: true},
		{name: "partial sudo environment", euid: 0, sudoUser: ExpectedCaller, lookup: lookup, wantError: true},
		{name: "invalid uid", euid: 0, sudoUID: "not-a-uid", sudoUser: ExpectedCaller, lookup: lookup, wantError: true},
		{name: "lookup failure", euid: 0, sudoUID: "1001", sudoUser: ExpectedCaller, lookup: func() (string, error) { return "", errors.New("missing") }, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := authorizeCaller(test.euid, test.sudoUID, test.sudoUser, test.lookup)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("authorizeCaller() = %q, %v; want %q, error=%v", got, err, test.want, test.wantError)
			}
		})
	}
}

func TestAuthorizeSocketCaller(t *testing.T) {
	lookup := func() (string, error) { return "1001", nil }
	tests := []struct {
		name      string
		euid      int
		peerUID   uint32
		lookup    func() (string, error)
		want      string
		wantError bool
	}{
		{name: "authorized peer", euid: 0, peerUID: 1001, lookup: lookup, want: ExpectedCaller},
		{name: "broker not root", euid: 1001, peerUID: 1001, lookup: lookup, wantError: true},
		{name: "peer mismatch", euid: 0, peerUID: 1002, lookup: lookup, wantError: true},
		{name: "root peer rejected", euid: 0, peerUID: 0, lookup: lookup, wantError: true},
		{name: "lookup failure", euid: 0, peerUID: 1001, lookup: func() (string, error) { return "", errors.New("missing") }, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := authorizeSocketCaller(test.euid, test.peerUID, test.lookup)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("authorizeSocketCaller() = %q, %v; want %q, error=%v", got, err, test.want, test.wantError)
			}
		})
	}
}
