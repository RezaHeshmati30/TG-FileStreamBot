package commands

import (
	"testing"
	"time"

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

func TestReplacementLinkExpiryUsesCurrentTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		action   string
		duration time.Duration
	}{
		{action: "6", duration: 6 * time.Hour},
		{action: "24", duration: 24 * time.Hour},
		{action: "7d", duration: 7 * 24 * time.Hour},
	}
	for _, test := range tests {
		expires, ok := replacementLinkExpiry(now, test.action)
		if !ok {
			t.Fatalf("action %q was rejected", test.action)
		}
		want := now.Add(test.duration).Unix()
		if expires != want {
			t.Fatalf("action %q expires at %d, want %d", test.action, expires, want)
		}
	}
}

func TestReplacementLinkExpiryDoesNotStack(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	first, ok := replacementLinkExpiry(now, "7d")
	if !ok {
		t.Fatal("first 7-day selection was rejected")
	}
	second, ok := replacementLinkExpiry(now, "7d")
	if !ok {
		t.Fatal("second 7-day selection was rejected")
	}
	if second != first || second > now.Add(maxLinkValidity).Unix() {
		t.Fatalf("repeated selection stacked validity: first=%d second=%d", first, second)
	}
}

func TestReplacementLinkExpiryRejectsUnknownAction(t *testing.T) {
	if _, ok := replacementLinkExpiry(time.Unix(1_700_000_000, 0), "30d"); ok {
		t.Fatal("unknown validity action should be rejected")
	}
}
