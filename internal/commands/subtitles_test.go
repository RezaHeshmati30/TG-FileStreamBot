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

func TestSubtitleDownloadCallbackFitsTelegramLimit(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-signing-key-with-32-bytes"
	data := subtitleCallbackData("d", math.MaxInt32, math.MaxInt64, -1)
	if len(data) > 64 {
		t.Fatalf("download callback data exceeds Telegram's 64-byte limit: %d", len(data))
	}
}

func TestSubtitleMIMETypeUsesActualFormat(t *testing.T) {
	tests := map[string]string{
		"subtitle.srt": "application/x-subrip",
		"subtitle.vtt": "text/vtt",
		"subtitle.ass": "text/x-ssa",
		"subtitle.ssa": "text/x-ssa",
	}
	for path, expected := range tests {
		if got := subtitleMIMEType(path); got != expected {
			t.Errorf("%s: expected %q, got %q", path, expected, got)
		}
	}
}
