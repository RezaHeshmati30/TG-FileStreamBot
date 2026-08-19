package commands

import (
	"math"
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestSeriesProgressStartCallbackFitsTelegramLimitAndRoundTrips(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-link-signing-key-that-is-long-enough"
	data := seriesProgressStartCallback(math.MaxInt32, math.MaxInt64)
	if len(data) > 64 {
		t.Fatalf("callback exceeds Telegram limit: %d bytes", len(data))
	}
	messageID, expires, ok := parseSeriesProgressStart(data)
	if !ok || messageID != math.MaxInt32 || expires != math.MaxInt64 {
		t.Fatalf("unexpected callback parse: message=%d expires=%d ok=%v", messageID, expires, ok)
	}
}

func TestSeriesProgressStartCallbackRejectsTampering(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-link-signing-key-that-is-long-enough"
	data := seriesProgressStartCallback(123, 456)
	data[len(data)-1] ^= 1
	if _, _, ok := parseSeriesProgressStart(data); ok {
		t.Fatal("tampered callback must be rejected")
	}
}

func TestSeriesProgressSessionCallbackFitsTelegramLimit(t *testing.T) {
	data := seriesProgressSessionCallback("abcdefghijkl", "c", math.MaxInt32)
	if len(data) > 64 {
		t.Fatalf("session callback exceeds Telegram limit: %d bytes", len(data))
	}
}
