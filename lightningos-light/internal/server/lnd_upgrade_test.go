package server

import "testing"

func TestIsSemverNewer(t *testing.T) {
  tests := []struct {
    name string
    current string
    latest string
    want bool
  }{
    {
      name: "rc to final beta is upgrade",
      current: "0.20.1-beta.rc1",
      latest: "0.20.1-beta",
      want: true,
    },
    {
      name: "beta to rc is not upgrade",
      current: "0.20.1-beta",
      latest: "0.20.1-beta.rc1",
      want: false,
    },
    {
      name: "rc numeric comparison",
      current: "0.20.1-beta.rc2",
      latest: "0.20.1-beta.rc10",
      want: true,
    },
    {
      name: "rc numeric comparison inverse",
      current: "0.20.1-beta.rc10",
      latest: "0.20.1-beta.rc2",
      want: false,
    },
    {
      name: "same version no upgrade",
      current: "0.20.1-beta.rc1",
      latest: "0.20.1-beta.rc1",
      want: false,
    },
    {
      name: "stable to prerelease is not upgrade",
      current: "0.20.1",
      latest: "0.20.1-beta",
      want: false,
    },
    {
      name: "patch bump remains upgrade",
      current: "0.20.0-beta",
      latest: "0.20.1-beta",
      want: true,
    },
  }

  for _, tc := range tests {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      got := isSemverNewer(tc.current, tc.latest)
      if got != tc.want {
        t.Fatalf("isSemverNewer(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
      }
    })
  }
}
