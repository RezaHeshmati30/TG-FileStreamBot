package routes

import (
	"net/url"
	"strings"
	"testing"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/types"
)

func TestBuildWVCDeepLinkEncodesVideoAndSubtitleSeparately(t *testing.T) {
	video := "https://example.com/stream/10?signature=video&expires=123"
	subtitle := "https://example.com/stream/20?signature=subtitle&expires=123"
	deepLink := buildWVCDeepLink(video, subtitle, wvcMediaMetadata{
		Title:    "Example · S03E07",
		Poster:   "https://image.tmdb.org/t/p/w500/example.jpg",
		MIMEType: "video/mp4",
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
	if got := parsed.Query().Get("subtitle"); got != subtitle {
		t.Fatalf("subtitle URL changed after encoding: %q", got)
	}
	if got := parsed.Query().Get("title"); got != "Example · S03E07" {
		t.Fatalf("title changed after encoding: %q", got)
	}
	if got := parsed.Query().Get("autostart"); got != "true" {
		t.Fatalf("autostart: got %q", got)
	}
	if got := parsed.Query().Get("poster"); got != "https://image.tmdb.org/t/p/w500/example.jpg" {
		t.Fatalf("poster: got %q", got)
	}
	if got := parsed.Query().Get("mime_type"); got != "video/mp4" {
		t.Fatalf("MIME type: got %q", got)
	}
	if got := parsed.Query().Get("secure_uri"); got != "true" {
		t.Fatalf("secure URI: got %q", got)
	}
}

func TestPublicSubtitleURLIncludesSubtitleFilename(t *testing.T) {
	oldHost := config.ValueOf.Host
	config.ValueOf.Host = "https://example.com"
	t.Cleanup(func() { config.ValueOf.Host = oldHost })

	file := &types.File{FileName: "Example German subtitle.srt", FileSize: 123, MimeType: "application/x-subrip", ID: 99}
	result := publicSubtitleURL(file, 20, 123456)
	parsed, err := url.Parse(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.Path, "/Example German subtitle.srt") {
		t.Fatalf("subtitle filename missing from URL path: %s", result)
	}
	if parsed.Query().Get("signature") == "" || parsed.Query().Get("expires") != "123456" {
		t.Fatalf("signed subtitle query is incomplete: %s", result)
	}
}
