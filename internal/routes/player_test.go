package routes

import (
	"net/url"
	"testing"
)

func TestBuildWVCVideoDeepLinkHasNoSubtitle(t *testing.T) {
	video := "https://example.com/stream/10?signature=video&expires=123"
	deepLink := buildWVCVideoDeepLink(video, wvcMediaMetadata{
		Title:    "Example Video (2025)",
		Poster:   "https://image.tmdb.org/t/p/w500/example.jpg",
		MIMEType: "video/x-matroska",
	})
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
	if _, exists := parsed.Query()["subtitle"]; exists {
		t.Fatal("video-only WVC link must not contain a subtitle parameter")
	}
	if got := parsed.Query().Get("autostart"); got != "true" {
		t.Fatalf("autostart: got %q", got)
	}
	if got := parsed.Query().Get("title"); got != "Example Video (2025)" {
		t.Fatalf("title: got %q", got)
	}
	if got := parsed.Query().Get("poster"); got != "https://image.tmdb.org/t/p/w500/example.jpg" {
		t.Fatalf("poster: got %q", got)
	}
	if got := parsed.Query().Get("mime_type"); got != "video/x-matroska" {
		t.Fatalf("MIME type: got %q", got)
	}
	if got := parsed.Query().Get("secure_uri"); got != "true" {
		t.Fatalf("secure URI: got %q", got)
	}
}

func TestBuildVLCDeepLinkEncodesStreamURL(t *testing.T) {
	video := "https://example.com/stream/10?signature=video&expires=123"
	deepLink := buildVLCDeepLink(video, "ignored")
	parsed, err := url.Parse(deepLink)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "vlc-x-callback" || parsed.Host != "x-callback-url" || parsed.Path != "/stream" {
		t.Fatalf("unexpected VLC target: %s", deepLink)
	}
	if got := parsed.Query().Get("url"); got != video {
		t.Fatalf("video URL changed after encoding: %q", got)
	}
}
