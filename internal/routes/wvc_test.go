package routes

import (
	"net/url"
	"testing"
)

func TestBuildWVCDeepLinkEncodesVideoAndSubtitleSeparately(t *testing.T) {
	video := "https://example.com/stream/10?signature=video&expires=123"
	subtitle := "https://example.com/stream/20?signature=subtitle&expires=123"
	deepLink := buildWVCDeepLink(video, subtitle, "Example S03E07.mkv")

	parsed, err := url.Parse(deepLink)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "wvc-x-callback" || parsed.Host != "open" {
		t.Fatalf("unexpected WVC target: %s", deepLink)
	}
	if got := parsed.Query().Get("url"); got != video {
		t.Fatalf("video URL changed after encoding: %q", got)
	}
	if got := parsed.Query().Get("subtitle"); got != subtitle {
		t.Fatalf("subtitle URL changed after encoding: %q", got)
	}
	if got := parsed.Query().Get("title"); got != "Example S03E07.mkv" {
		t.Fatalf("title changed after encoding: %q", got)
	}
	if got := parsed.Query().Get("autostart"); got != "true" {
		t.Fatalf("autostart: got %q", got)
	}
}
