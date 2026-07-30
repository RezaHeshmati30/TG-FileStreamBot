package utils

import (
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestSignFile(t *testing.T) {
	previousKey := config.ValueOf.LinkSigningKey
	config.ValueOf.LinkSigningKey = "test-only-signing-key-with-32-bytes"
	t.Cleanup(func() {
		config.ValueOf.LinkSigningKey = previousKey
	})

	signature := SignFile("video.mp4", 1024, "video/mp4", 42, 123456789)
	if !CheckSignature(signature, signature) {
		t.Fatal("expected signature to validate")
	}

	tampered := SignFile("video.mp4", 1024, "video/mp4", 42, 123456790)
	if CheckSignature(signature, tampered) {
		t.Fatal("signature must not validate after changing the expiry")
	}

	if CheckSignature("not-hex", signature) {
		t.Fatal("malformed signature must not validate")
	}
}
