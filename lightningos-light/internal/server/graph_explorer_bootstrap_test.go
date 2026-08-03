package server

import (
	"testing"
	"time"
)

func TestGraphExplorerCoverageRequiresCompletedBootstrap(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		coverage  *time.Time
		bootstrap *time.Time
		want      bool
	}{
		{name: "empty", want: false},
		{name: "stream event only", coverage: &now, want: false},
		{name: "bootstrap marker only", bootstrap: &now, want: false},
		{name: "snapshot committed", coverage: &now, bootstrap: &now, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := graphExplorerCoverageComplete(test.coverage, test.bootstrap); got != test.want {
				t.Fatalf("graphExplorerCoverageComplete() = %t, want %t", got, test.want)
			}
		})
	}
}
