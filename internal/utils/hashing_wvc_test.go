package utils

import "testing"

func TestSignWVCLaunchBindsBothFilesAndExpiry(t *testing.T) {
	signature := SignWVCLaunch(10, 20, 123456789)
	if !CheckSignature(signature, SignWVCLaunch(10, 20, 123456789)) {
		t.Fatal("expected identical WVC launch data to validate")
	}
	if CheckSignature(signature, SignWVCLaunch(11, 20, 123456789)) {
		t.Fatal("changing the video message must invalidate the signature")
	}
	if CheckSignature(signature, SignWVCLaunch(10, 21, 123456789)) {
		t.Fatal("changing the subtitle message must invalidate the signature")
	}
	if CheckSignature(signature, SignWVCLaunch(10, 20, 123456790)) {
		t.Fatal("changing the expiry must invalidate the signature")
	}
}
