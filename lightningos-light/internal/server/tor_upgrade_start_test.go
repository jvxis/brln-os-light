package server

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestTorStartConfirmationNeverRetriesMutation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		handled   bool
		startErr  error
		running   bool
		wantErr   bool
		wantProbe bool
	}{
		{"acknowledged", true, nil, false, false, false},
		{"reply lost but running", true, context.DeadlineExceeded, true, false, true},
		{"unix socket timeout but running", true, &net.OpError{Op: "read", Net: "unix", Err: os.ErrDeadlineExceeded}, true, false, true},
		{"reply lost and not running", true, context.DeadlineExceeded, false, true, true},
		{"broker rejected even with another unit running", true, errors.New("broker rejected"), true, true, false},
		{"broker unavailable", false, nil, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, probes := 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Lost/expired request must not poison the state confirmation.
			handled, unit, err := startTorUpgradeWithConfirmation(ctx, func(context.Context) (bool, string, error) {
				calls++
				return tc.handled, torUpgradeUnitName, tc.startErr
			}, func(check context.Context) bool {
				probes++
				deadline, ok := check.Deadline()
				if check.Err() != nil || !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("confirmation needs its own bounded live context")
				}
				return tc.running
			})
			if calls != 1 || (probes == 1) != tc.wantProbe || probes > 1 {
				t.Fatalf("calls/probes = %d/%d", calls, probes)
			}
			if handled != tc.handled || unit != torUpgradeUnitName || (err != nil) != tc.wantErr {
				t.Fatalf("unexpected result: %v %q %v", handled, unit, err)
			}
			if tc.wantErr && !errors.Is(err, tc.startErr) {
				t.Fatal("original error was lost")
			}
		})
	}
}
