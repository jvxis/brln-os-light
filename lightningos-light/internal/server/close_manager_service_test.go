package server

import (
	"errors"
	"testing"
	"time"
)

func TestCloseManagerNextPollInterval(t *testing.T) {
	t.Run("active sessions keep fast polling", func(t *testing.T) {
		if got := closeManagerNextPollInterval(true, nil, nil, time.Second); got != closeManagerPollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerPollInterval)
		}
	})

	t.Run("idle service backs off", func(t *testing.T) {
		if got := closeManagerNextPollInterval(false, nil, nil, time.Second); got != closeManagerIdlePollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerIdlePollInterval)
		}
	})

	t.Run("state errors keep conservative fast polling", func(t *testing.T) {
		if got := closeManagerNextPollInterval(false, errors.New("db unavailable"), nil, time.Second); got != closeManagerPollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerPollInterval)
		}
	})

	t.Run("refresh errors back off even with active sessions", func(t *testing.T) {
		if got := closeManagerNextPollInterval(true, nil, errors.New("lnd timeout"), time.Second); got != closeManagerIdlePollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerIdlePollInterval)
		}
	})

	t.Run("slow refreshes back off even with active sessions", func(t *testing.T) {
		if got := closeManagerNextPollInterval(true, nil, nil, closeManagerSlowRefresh); got != closeManagerIdlePollInterval {
			t.Fatalf("got %v, want %v", got, closeManagerIdlePollInterval)
		}
	})
}
