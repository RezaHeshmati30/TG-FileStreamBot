package routes

import (
	"net/url"
	"testing"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"
	"rsc.io/qr"
)

func TestCanonicalHandoffURLCanBeEncodedAsQR(t *testing.T) {
	oldHost := config.ValueOf.Host
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.Host = "https://example.com/"
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() {
		config.ValueOf.Host = oldHost
		config.ValueOf.LinkSigningKey = oldKey
	})

	expires := int64(123456789)
	signature := utils.SignHandoffPage(42, expires)
	result := canonicalHandoffURL(42, expires, signature)
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/handoff/42" || parsed.Query().Get("signature") != signature {
		t.Fatalf("unexpected canonical handoff URL: %s", result)
	}
	code, err := qr.Encode(result, qr.M)
	if err != nil {
		t.Fatal(err)
	}
	if len(code.PNG()) == 0 {
		t.Fatal("QR PNG must not be empty")
	}
}

func TestPlayerLaunchURLUsesShortHandoffExpiry(t *testing.T) {
	oldHost := config.ValueOf.Host
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.Host = "https://example.com"
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() {
		config.ValueOf.Host = oldHost
		config.ValueOf.LinkSigningKey = oldKey
	})

	parsed, err := url.Parse(playerLaunchURL("wvc", 42, 123456789))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/player/wvc" || parsed.Query().Get("video") != "42" || parsed.Query().Get("expires") != "123456789" {
		t.Fatalf("unexpected player launch URL: %s", parsed.String())
	}
}
