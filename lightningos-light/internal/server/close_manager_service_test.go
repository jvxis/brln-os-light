package server

import (
	"errors"
	"testing"
)

func TestCloseManagerNextPollInterval(t *testing.T) {
	t.Run("active sessions keep fast polling", func(t *testing.T) {
		if got := closeManagerNextPollInterval(true, nil); got != closeManagerPollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerPollInterval)
		}
	})

	t.Run("idle service backs off", func(t *testing.T) {
		if got := closeManagerNextPollInterval(false, nil); got != closeManagerIdlePollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerIdlePollInterval)
		}
	})

	t.Run("state errors keep conservative fast polling", func(t *testing.T) {
		if got := closeManagerNextPollInterval(false, errors.New("db unavailable")); got != closeManagerPollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerPollInterval)
		}
	})
}
