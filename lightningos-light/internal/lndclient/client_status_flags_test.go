package lndclient

import "testing"

func TestIsLocalChanDisabledFlags(t *testing.T) {
  tests := []struct {
    name  string
    flags string
    want  bool
  }{
    {name: "empty", flags: "", want: false},
    {name: "local flag", flags: "ChanStatusLocalChanDisabled", want: true},
    {name: "snake local flag", flags: "local_chan_disabled", want: true},
    {name: "generic disabled", flags: "ChanStatusDisabled", want: true},
    {name: "remote disabled", flags: "ChanStatusRemoteChanDisabled", want: false},
    {
      name:  "remote disabled with another local token",
      flags: "ChanStatusLocalCloseInitiator|ChanStatusRemoteChanDisabled",
      want:  false,
    },
    {
      name:  "tokenized local disabled",
      flags: "ChanStatusLocalCloseInitiator|ChanStatusLocalChanDisabled",
      want:  true,
    },
  }

  for _, tc := range tests {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      if got := isLocalChanDisabledFlags(tc.flags); got != tc.want {
        t.Fatalf("isLocalChanDisabledFlags(%q) = %v, want %v", tc.flags, got, tc.want)
      }
    })
  }
}
