package utils

import (
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestSignSubtitleAction(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-signing-key-with-32-bytes"

	signature := SignSubtitleAction("extract", 42, 123456789, 3)
	if len(signature) != 16 {
		t.Fatalf("expected a compact 16-character signature, got %d", len(signature))
	}
	if signature != SignSubtitleAction("extract", 42, 123456789, 3) {
		t.Fatal("expected identical subtitle actions to have identical signatures")
	}
	if signature == SignSubtitleAction("extract", 42, 123456789, 4) {
		t.Fatal("changing the track index must change the signature")
	}
}
