package server

import (
	"strings"
	"testing"
)

func TestParseElectrsIndexHeight(t *testing.T) {
	sample := `# HELP electrs_index_height Indexed block height
# TYPE electrs_index_height gauge
electrs_index_height{type="tip"} 892044
electrs_mempool_count 12345
`
	h, err := parseElectrsIndexHeight(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != 892044 {
		t.Fatalf("expected 892044, got %d", h)
	}
}

func TestParseElectrsIndexHeightMissingLabel(t *testing.T) {
	sample := `electrs_index_height{type="other"} 42
`
	if _, err := parseElectrsIndexHeight(strings.NewReader(sample)); err == nil {
		t.Fatal("expected error when type=\"tip\" is absent")
	}
}
