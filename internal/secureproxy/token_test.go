package secureproxy

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"EverythingSuckz/fsb/config"
)

func TestEncryptedTargetRoundTripAndConfidentiality(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-link-signing-key-that-is-long-enough"
	targetURL := "https://media.example.com/video.mkv?secret=value"
	expires := time.Now().Add(time.Hour).Unix()
	token, err := EncryptTarget(targetURL, expires)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(token, "media.example.com") || strings.Contains(token, "secret") {
		t.Fatal("encrypted token exposes the source URL")
	}
	target, err := DecryptTarget(token, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if target.URL != targetURL || target.Expires != expires {
		t.Fatalf("unexpected decrypted target: %+v", target)
	}
}

func TestEncryptedTargetRejectsTamperingAndExpiration(t *testing.T) {
	config.ValueOf.LinkSigningKey = "test-only-link-signing-key-that-is-long-enough"
	token, err := EncryptTarget("https://example.com/video.mp4", time.Now().Add(time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	rawToken, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	rawToken[len(rawToken)-1] ^= 1
	tampered := base64.RawURLEncoding.EncodeToString(rawToken)
	if _, err := DecryptTarget(tampered, time.Now()); err != ErrInvalidToken {
		t.Fatalf("tampered token: got %v, want ErrInvalidToken", err)
	}
	expired, err := EncryptTarget("https://example.com/video.mp4", time.Now().Add(-time.Second).Unix())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptTarget(expired, time.Now()); err != ErrExpiredToken {
		t.Fatalf("expired token: got %v, want ErrExpiredToken", err)
	}
}
