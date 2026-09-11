package commands

import (
	"testing"

	"EverythingSuckz/fsb/config"
)

func TestValidityCallbackRoundTripAndTamperProtection(t *testing.T) {
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() { config.ValueOf.LinkSigningKey = oldKey })
	data := validityCallback("24", 42, 123456789)
	action, messageID, expires, ok := parseValidityCallback(data)
	if !ok || action != "24" || messageID != 42 || expires != 123456789 {
		t.Fatalf("callback did not round-trip: %q", data)
	}
	data[len(data)-1] ^= 1
	if _, _, _, ok := parseValidityCallback(data); ok {
		t.Fatal("modified validity callback should be rejected")
	}
}

func TestValidityMenuLayout(t *testing.T) {
	oldKey := config.ValueOf.LinkSigningKey
	config.ValueOf.LinkSigningKey = "test-signing-key-with-at-least-32-characters"
	t.Cleanup(func() { config.ValueOf.LinkSigningKey = oldKey })
	menu := validityMenu(42, 123456789)
	if len(menu.Rows) != 3 || len(menu.Rows[0].Buttons) != 1 || len(menu.Rows[1].Buttons) != 3 || len(menu.Rows[2].Buttons) != 1 {
		t.Fatalf("unexpected validity menu layout: %+v", menu.Rows)
	}
}
