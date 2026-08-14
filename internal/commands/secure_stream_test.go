package commands

import "testing"

func TestFirstExternalURL(t *testing.T) {
	got := firstExternalURL("Open this link: https://example.com/video.mkv?x=1&y=2.")
	if got != "https://example.com/video.mkv?x=1&y=2" {
		t.Fatalf("unexpected URL: %q", got)
	}
	if got := firstExternalURL("no link here"); got != "" {
		t.Fatalf("expected no URL, got %q", got)
	}
}
