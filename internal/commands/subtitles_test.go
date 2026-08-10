package commands

import (
	"math"
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestSubtitleCallbackFitsTelegramLimitAndRoundTrips(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-signing-key-with-32-bytes"
	data := subtitleCallbackData("x", math.MaxInt32, math.MaxInt64, math.MaxInt32)
	if len(data) > 64 {
		t.Fatalf("callback data exceeds Telegram's 64-byte limit: %d", len(data))
	}
	action, messageID, expires, trackIndex, ok := parseSubtitleCallback(data)
	if !ok || action != "x" || messageID != math.MaxInt32 || expires != math.MaxInt64 || trackIndex != math.MaxInt32 {
		t.Fatal("callback data did not round-trip")
	}
}
