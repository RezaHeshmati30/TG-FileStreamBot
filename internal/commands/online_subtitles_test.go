package commands

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMediaFileName(t *testing.T) {
	tests := []struct {
		fileName string
		want     mediaQuery
	}{
		{"Dune.Part.Two.2024.2160p.WEB-DL.mkv", mediaQuery{Title: "Dune Part Two", Year: "2024", Type: "movie"}},
		{"The.Last.of.Us.S02E04.1080p.WEB-DL.mkv", mediaQuery{Title: "The Last of Us", Type: "series", Season: "2", Episode: "4"}},
	}
	for _, test := range tests {
		if got := parseMediaFileName(test.fileName); got != test.want {
			t.Fatalf("parse %q: got %+v, want %+v", test.fileName, got, test.want)
		}
	}
}

func TestRankSubsourceMoviesPrefersExactTitleAndYear(t *testing.T) {
	movies := []subsourceMovie{
		{ID: "1", Title: "Dune", Year: "1984"},
		{ID: "2", Title: "Dune: Part Two", Year: "2024"},
	}
	ranked := rankSubsourceMovies(movies, mediaQuery{Title: "Dune Part Two", Year: "2024"})
	if ranked[0].ID != "2" {
		t.Fatalf("expected exact title/year match first, got %+v", ranked[0])
	}
}

func TestSubsourceMovieSearchQueriesUseCurrentAPIParameters(t *testing.T) {
	queries := subsourceMovieSearchQueries(mediaQuery{Title: "Dune Part Two", Year: "2024", Type: "movie"})
	if len(queries) != 2 {
		t.Fatalf("expected query with and without year, got %d", len(queries))
	}
	if got := queries[0].Get("searchType"); got != "text" {
		t.Fatalf("searchType: got %q, want text", got)
	}
	if got := queries[0].Get("q"); got != "Dune Part Two" {
		t.Fatalf("q: got %q", got)
	}
	if got := queries[0].Get("type"); got != "all" {
		t.Fatalf("type: got %q, want all", got)
	}
	if got := queries[0].Get("year"); got != "2024" {
		t.Fatalf("year: got %q, want 2024", got)
	}
	if got := queries[1].Get("year"); got != "" {
		t.Fatalf("fallback must omit year, got %q", got)
	}
}

func TestSubsourceMovieSearchQueriesAddShortTitleFallback(t *testing.T) {
	queries := subsourceMovieSearchQueries(mediaQuery{Title: "The Lord of the Rings Fellowship", Type: "movie"})
	if len(queries) != 2 {
		t.Fatalf("expected full and shortened query, got %d", len(queries))
	}
	if got := queries[1].Get("q"); got != "The Lord of" {
		t.Fatalf("short query: got %q", got)
	}
}

func TestSubsourceErrorMessageReadsJSONWithoutExposingRequest(t *testing.T) {
	got := subsourceErrorMessage([]byte(`{"message":"Invalid search parameters"}`))
	if got != "Invalid search parameters" {
		t.Fatalf("unexpected error message: %q", got)
	}
}

func TestOnlineCallbackFitsTelegramLimit(t *testing.T) {
	data := onlineCallback("abcdefghijkl", "dl", "2147483647")
	if len(data) > 64 {
		t.Fatalf("online callback exceeds Telegram limit: %d", len(data))
	}
}

func TestExtractBestSubtitleFileSelectsEpisode(t *testing.T) {
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, name := range []string{"Show.S01E01.srt", "Show.S01E02.srt"} {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte("1\n00:00:00,000 --> 00:00:01,000\nTest\n"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path, err := extractBestSubtitleFile(archive.Bytes(), directory, mediaQuery{Type: "series", Season: "1", Episode: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "Show.S01E02.srt" {
		t.Fatalf("expected episode 2 subtitle, got %s", filepath.Base(path))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestPickSubsourceArrayHandlesNestedData(t *testing.T) {
	payload := map[string]any{"data": map[string]any{"movies": []any{map[string]any{"id": "1"}}}}
	if got := pickSubsourceArray(payload); len(got) != 1 {
		t.Fatalf("expected one nested result, got %d", len(got))
	}
}
