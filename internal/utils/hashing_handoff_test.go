package utils

import (
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestHandoffSignaturesBindMessageAndExpiry(t *testing.T) {
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() { config.ValueOf.LinkSigningKey = oldKey })

	pageSignature := SignHandoffPage(10, 123456789)
	if !CheckSignature(pageSignature, SignHandoffPage(10, 123456789)) {
		t.Fatal("expected identical handoff page data to validate")
	}
	if CheckSignature(pageSignature, SignHandoffPage(11, 123456789)) || CheckSignature(pageSignature, SignHandoffPage(10, 123456790)) {
		t.Fatal("changing handoff page data must invalidate the signature")
	}
	if SignHandoffAction(10, 123456789) == SignHandoffAction(11, 123456789) {
		t.Fatal("callback signature must bind the Telegram message")
	}
}
