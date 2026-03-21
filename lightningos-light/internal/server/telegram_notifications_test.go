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

func TestTelegramActivityMirrorMessageMarksFailedLightning(t *testing.T) {
	msg := telegramActivityMirrorMessage(Notification{
		Type:      "lightning",
		Action:    "sent",
		Status:    "FAILED",
		AmountSat: 771471,
		PeerAlias: "WalletOfSatoshi.com",
	})

	if !strings.Contains(msg, "Lightning failed") {
		t.Fatalf("expected failed lightning message, got %q", msg)
	}
	if strings.Contains(msg, "Lightning sent") {
		t.Fatalf("did not expect success wording in %q", msg)
	}
}

func TestTelegramActivityMirrorMessageKeepsSucceededLightningSent(t *testing.T) {
	msg := telegramActivityMirrorMessage(Notification{
		Type:      "lightning",
		Action:    "sent",
		Status:    "SUCCEEDED",
		AmountSat: 771471,
		PeerAlias: "WalletOfSatoshi.com",
	})

	if !strings.Contains(msg, "Lightning sent") {
		t.Fatalf("expected success wording, got %q", msg)
	}
}

func TestTelegramActivityMirrorMessageIncludesKeysendMessage(t *testing.T) {
	msg := telegramActivityMirrorMessage(Notification{
		Type:         "keysend",
		Action:       "received",
		Status:       "SETTLED",
		AmountSat:    21,
		PeerAlias:    "peer-alias",
		ChannelAlias: "peer-channel",
		Memo:         "  ola   mundo \n no telegram  ",
	})

	if !strings.Contains(msg, "Keysend received") {
		t.Fatalf("expected keysend wording, got %q", msg)
	}
	if !strings.Contains(msg, "Msg \"ola mundo no telegram\"") {
		t.Fatalf("expected keysend message content, got %q", msg)
	}
}
