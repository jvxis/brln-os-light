package server

import (
	"strings"
	"testing"
)

func TestSplitTelegramMessageRespectsLimit(t *testing.T) {
	text := strings.Join([]string{
		"line 1",
		"line 2 is a bit longer",
		"line 3 is also somewhat longer than the previous one",
		"line 4",
	}, "\n")

	chunks := splitTelegramMessage(text, 40)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if got := telegramTextLen(chunk); got > 40 {
			t.Fatalf("chunk %d exceeds limit: got %d", i, got)
		}
	}
}

func TestChunkTelegramAutofeeSectionRepeatsHeader(t *testing.T) {
	lines := []string{
		"✅🔻 alpha | sink | 1000→900 ppm | target 800 | out 20.0% | margin 50 | 🧱floor-lock",
		"✅🔻 beta | sink | 900→800 ppm | target 700 | out 19.0% | margin 40 | ⛔stepcap-lock",
		"✅🔻 gamma | router | 800→700 ppm | target 650 | out 30.0% | margin 30 | 🧯floor-relax",
	}

	chunks := chunkTelegramAutofeeSection("✅ Changed channels", lines, 120)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple section chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if !strings.Contains(chunk, "✅ Changed channels") {
			t.Fatalf("chunk %d missing header: %q", i, chunk)
		}
		if got := telegramTextLen(chunk); got > 120 {
			t.Fatalf("chunk %d exceeds limit: got %d", i, got)
		}
	}
}
