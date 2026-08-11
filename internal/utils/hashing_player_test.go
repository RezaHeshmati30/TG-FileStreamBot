package utils

import "testing"

func TestSignPlayerLaunchBindsPlayerVideoAndExpiry(t *testing.T) {
	signature := SignPlayerLaunch("wvc", 10, 123456789)
	if !CheckSignature(signature, SignPlayerLaunch("wvc", 10, 123456789)) {
		t.Fatal("expected identical player launch data to validate")
	}
	if CheckSignature(signature, SignPlayerLaunch("vlc", 10, 123456789)) {
		t.Fatal("changing the player must invalidate the signature")
	}
	if CheckSignature(signature, SignPlayerLaunch("wvc", 11, 123456789)) {
		t.Fatal("changing the video must invalidate the signature")
	}
	if CheckSignature(signature, SignPlayerLaunch("wvc", 10, 123456790)) {
		t.Fatal("changing the expiry must invalidate the signature")
	}
}
