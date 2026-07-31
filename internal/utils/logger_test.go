package utils

import (
	"strings"
	"testing"
)

func TestRedactSensitiveText(t *testing.T) {
	input := "GET https://example.com/stream/42?signature=abcdef123456&expires=123 token=private-value Authorization=BearerValue"
	result := RedactSensitiveText(input)

	for _, secret := range []string{"abcdef123456", "private-value", "BearerValue"} {
		if strings.Contains(result, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, result)
		}
	}
	if !strings.Contains(result, "signature=[REDACTED]") {
		t.Fatalf("signature was not redacted: %s", result)
	}
}

func TestRedactBotToken(t *testing.T) {
	token := "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghi"
	result := RedactSensitiveText("Telegram token: " + token)
	if strings.Contains(result, token) {
		t.Fatalf("bot token was not redacted: %s", result)
	}
}
