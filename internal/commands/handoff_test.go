package commands

import (
	"net/url"
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestHandoffCallbackRoundTripAndTampering(t *testing.T) {
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() { config.ValueOf.LinkSigningKey = oldKey })

	data := handoffCallbackData(42, 123456789)
	if len(data) > 64 {
		t.Fatalf("callback data exceeds Telegram's 64-byte limit: %d", len(data))
	}
	messageID, expires, ok := parseHandoffCallback(data)
	if !ok || messageID != 42 || expires != 123456789 {
		t.Fatalf("callback did not round-trip: message=%d expires=%d ok=%v", messageID, expires, ok)
	}
	data[len(data)-1] ^= 1
	if _, _, ok := parseHandoffCallback(data); ok {
		t.Fatal("tampered callback must be rejected")
	}
}

func TestBuildHandoffURL(t *testing.T) {
	oldHost := config.ValueOf.Host
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.Host = "https://example.com/"
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() {
		config.ValueOf.Host = oldHost
		config.ValueOf.LinkSigningKey = oldKey
	})

	result := buildHandoffURL(42, 123456789)
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/handoff/42" || parsed.Query().Get("expires") != "123456789" || parsed.Query().Get("signature") == "" {
		t.Fatalf("unexpected handoff URL: %s", result)
	}
}
