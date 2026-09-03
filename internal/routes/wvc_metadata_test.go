package routes

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/types"
)

type wvcRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn wvcRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestParseWVCMovieFileName(t *testing.T) {
	parsed := parseWVCFileName("Dune.Part.Two.2024.2160p.WEB-DL.x265.mkv")
	if parsed.title != "Dune Part Two" || parsed.year != "2024" || parsed.media != "movie" {
		t.Fatalf("unexpected parsed movie: %+v", parsed)
	}
	if got := displayWVCFileName(parsed, "fallback.mkv"); got != "Dune Part Two (2024)" {
		t.Fatalf("unexpected display title: %q", got)
	}
}

func TestParseWVCEpisodeFileName(t *testing.T) {
	parsed := parseWVCFileName("The.Last.of.Us.S02E03.1080p.WEBRip.mkv")
	if parsed.title != "The Last of Us" || parsed.media != "tv" || parsed.season != 2 || parsed.episode != 3 {
		t.Fatalf("unexpected parsed episode: %+v", parsed)
	}
	if got := displayWVCFileName(parsed, "fallback.mkv"); got != "The Last of Us · S02E03" {
		t.Fatalf("unexpected display title: %q", got)
	}
}

func TestWVCMIMETypePrefersStoredVideoTypeAndFallsBackToExtension(t *testing.T) {
	if got := wvcMIMEType("video.mkv", "video/custom"); got != "video/custom" {
		t.Fatalf("stored MIME type was not preserved: %q", got)
	}
	if got := wvcMIMEType("video.mkv", "application/octet-stream"); got != "video/x-matroska" {
		t.Fatalf("extension MIME type was not inferred: %q", got)
	}
}

func TestResolveWVCMetadataUsesTMDbResult(t *testing.T) {
	oldKey := config.ValueOf.TMDbAPIKey
	oldClient := wvcMetadataClient
	config.ValueOf.TMDbAPIKey = "test-key"
	wvcMetadataCache.Lock()
	wvcMetadataCache.items = make(map[string]cachedWVCMetadata)
	wvcMetadataCache.Unlock()
	wvcMetadataClient = &http.Client{Transport: wvcRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/3/search/tv" || request.URL.Query().Get("query") != "Severance" {
			t.Fatalf("unexpected TMDb request: %s", request.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"results":[{"name":"Severance","first_air_date":"2022-02-17","poster_path":"/poster.jpg"}]}`)),
			Request:    request,
		}, nil
	})}
	t.Cleanup(func() {
		config.ValueOf.TMDbAPIKey = oldKey
		wvcMetadataClient = oldClient
		wvcMetadataCache.Lock()
		wvcMetadataCache.items = make(map[string]cachedWVCMetadata)
		wvcMetadataCache.Unlock()
	})

	metadata := resolveWVCMetadata(context.Background(), &types.File{
		FileName: "Severance.S02E04.1080p.mkv",
		MimeType: "application/octet-stream",
	})
	if metadata.Title != "Severance · S02E04" {
		t.Fatalf("unexpected resolved title: %q", metadata.Title)
	}
	if metadata.Poster != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("unexpected poster: %q", metadata.Poster)
	}
	if metadata.MIMEType != "video/x-matroska" {
		t.Fatalf("unexpected MIME type: %q", metadata.MIMEType)
	}
}
