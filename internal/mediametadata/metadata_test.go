package mediametadata

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/types"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestLocalMovieMetadata(t *testing.T) {
	metadata := Local(&types.File{FileName: "Dune.Part.Two.2024.2160p.WEB-DL.x265.mkv", MimeType: "application/octet-stream"})
	if metadata.Name != "Dune Part Two" || metadata.Title != "Dune Part Two (2024)" || metadata.Year != "2024" || metadata.Media != "movie" {
		t.Fatalf("unexpected movie metadata: %+v", metadata)
	}
	if metadata.MIMEType != "video/x-matroska" {
		t.Fatalf("unexpected MIME type: %q", metadata.MIMEType)
	}
}

func TestLocalEpisodeMetadata(t *testing.T) {
	metadata := Local(&types.File{FileName: "The.Last.of.Us.S02E03.1080p.WEBRip.mkv"})
	if metadata.Name != "The Last of Us" || metadata.Title != "The Last of Us · S02E03" || metadata.Media != "tv" || metadata.Season != 2 || metadata.Episode != 3 {
		t.Fatalf("unexpected episode metadata: %+v", metadata)
	}
}

func TestResolveUsesUnambiguousTMDbResult(t *testing.T) {
	configureTMDbTest(t, `{"results":[{"name":"Severance","first_air_date":"2022-02-17","poster_path":"/poster.jpg"}]}`)
	metadata := Resolve(context.Background(), &types.File{FileName: "Severance.S02E04.1080p.mkv", MimeType: "application/octet-stream"})
	if metadata.Name != "Severance" || metadata.Title != "Severance · S02E04" || metadata.Year != "2022" {
		t.Fatalf("unexpected resolved metadata: %+v", metadata)
	}
	if metadata.Poster != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("unexpected poster: %q", metadata.Poster)
	}
}

func TestResolveRejectsAmbiguousTitleWithoutYear(t *testing.T) {
	configureTMDbTest(t, `{"results":[{"title":"The Thing","release_date":"1982-06-25","poster_path":"/1982.jpg"},{"title":"The Thing","release_date":"2011-10-14","poster_path":"/2011.jpg"}]}`)
	metadata := Resolve(context.Background(), &types.File{FileName: "The.Thing.1080p.mkv"})
	if metadata.Poster != "" || metadata.Name != "The Thing" {
		t.Fatalf("ambiguous result should use local fallback: %+v", metadata)
	}
}

func TestResolveSelectsMatchingYear(t *testing.T) {
	configureTMDbTest(t, `{"results":[{"title":"The Thing","release_date":"2011-10-14","poster_path":"/2011.jpg"},{"title":"The Thing","release_date":"1982-06-25","poster_path":"/1982.jpg"}]}`)
	metadata := Resolve(context.Background(), &types.File{FileName: "The.Thing.1982.1080p.mkv"})
	if metadata.Year != "1982" || !strings.HasSuffix(metadata.Poster, "/1982.jpg") {
		t.Fatalf("year did not disambiguate result: %+v", metadata)
	}
}

func TestResolveFallsBackWhenTMDbTimesOut(t *testing.T) {
	configureTMDbTransport(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	metadata := Resolve(ctx, &types.File{FileName: "Dune.Part.Two.2024.1080p.mkv"})
	if metadata.Poster != "" || metadata.Title != "Dune Part Two (2024)" {
		t.Fatalf("timeout should return local metadata: %+v", metadata)
	}
}

func configureTMDbTest(t *testing.T, body string) {
	t.Helper()
	configureTMDbTransport(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}))
}

func configureTMDbTransport(t *testing.T, transport http.RoundTripper) {
	t.Helper()
	oldKey := config.ValueOf.TMDbAPIKey
	oldClient := httpClient
	config.ValueOf.TMDbAPIKey = "test-key"
	httpClient = &http.Client{Transport: transport}
	cache.Lock()
	cache.items = make(map[string]cachedMetadata)
	cache.Unlock()
	inflight.Lock()
	inflight.items = make(map[string]*lookup)
	inflight.Unlock()
	t.Cleanup(func() {
		config.ValueOf.TMDbAPIKey = oldKey
		httpClient = oldClient
		cache.Lock()
		cache.items = make(map[string]cachedMetadata)
		cache.Unlock()
		inflight.Lock()
		inflight.items = make(map[string]*lookup)
		inflight.Unlock()
	})
}
