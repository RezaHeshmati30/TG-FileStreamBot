package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/types"
)

const (
	tmdbAPIBase   = "https://api.themoviedb.org/3"
	tmdbImageBase = "https://image.tmdb.org/t/p/w500"
)

var (
	wvcYearPattern    = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	wvcEpisodePattern = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b`)
	wvcReleaseCut     = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|bluray|blu-ray|web[ ._-]?dl|webrip|hdtv|dvdrip|remux|x26[45]|h[ ._-]?26[45]|hevc|avc|hdr10?|dolby[ ._-]?vision|dv|proper|repack|multi|dubbed)\b`)
	wvcMetadataClient = &http.Client{Timeout: 5 * time.Second}
	wvcMetadataCache  = struct {
		sync.RWMutex
		items map[string]cachedWVCMetadata
	}{items: make(map[string]cachedWVCMetadata)}
)

type wvcMediaMetadata struct {
	Title    string
	Poster   string
	MIMEType string
}

type cachedWVCMetadata struct {
	metadata  wvcMediaMetadata
	expiresAt time.Time
}

type parsedWVCFileName struct {
	title   string
	year    string
	media   string
	season  int
	episode int
}

type tmdbSearchResponse struct {
	Results []struct {
		Title       string `json:"title"`
		Name        string `json:"name"`
		ReleaseDate string `json:"release_date"`
		FirstAir    string `json:"first_air_date"`
		PosterPath  string `json:"poster_path"`
	} `json:"results"`
}

func resolveWVCMetadata(ctx context.Context, file *types.File) wvcMediaMetadata {
	parsed := parseWVCFileName(file.FileName)
	metadata := wvcMediaMetadata{
		Title:    displayWVCFileName(parsed, file.FileName),
		MIMEType: wvcMIMEType(file.FileName, file.MimeType),
	}
	apiKey := strings.TrimSpace(config.ValueOf.TMDbAPIKey)
	if apiKey == "" || parsed.title == "" {
		return metadata
	}

	cacheKey := strings.ToLower(strings.Join([]string{parsed.media, parsed.title, parsed.year}, "|"))
	wvcMetadataCache.RLock()
	cached, found := wvcMetadataCache.items[cacheKey]
	wvcMetadataCache.RUnlock()
	if found && time.Now().Before(cached.expiresAt) {
		cached.metadata.MIMEType = metadata.MIMEType
		if parsed.media == "tv" && parsed.season > 0 && parsed.episode > 0 {
			cached.metadata.Title = appendEpisodeLabel(cached.metadata.Title, parsed.season, parsed.episode)
		}
		return cached.metadata
	}

	resolved, ok := searchTMDbMetadata(ctx, apiKey, parsed)
	if !ok {
		return metadata
	}
	resolved.MIMEType = metadata.MIMEType
	baseResolved := resolved
	if parsed.media == "tv" && parsed.season > 0 && parsed.episode > 0 {
		resolved.Title = appendEpisodeLabel(resolved.Title, parsed.season, parsed.episode)
	}
	wvcMetadataCache.Lock()
	wvcMetadataCache.items[cacheKey] = cachedWVCMetadata{metadata: baseResolved, expiresAt: time.Now().Add(24 * time.Hour)}
	wvcMetadataCache.Unlock()
	return resolved
}

func parseWVCFileName(fileName string) parsedWVCFileName {
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	name = strings.NewReplacer(".", " ", "_", " ", "[", " ", "]", " ").Replace(name)
	result := parsedWVCFileName{media: "movie"}
	if match := wvcEpisodePattern.FindStringSubmatchIndex(name); len(match) == 6 {
		result.media = "tv"
		result.season, _ = strconv.Atoi(name[match[2]:match[3]])
		result.episode, _ = strconv.Atoi(name[match[4]:match[5]])
		name = name[:match[0]]
	}
	if match := wvcYearPattern.FindStringSubmatch(name); len(match) == 2 {
		result.year = match[1]
		name = strings.Split(name, match[1])[0]
	}
	if location := wvcReleaseCut.FindStringIndex(name); location != nil {
		name = name[:location[0]]
	}
	result.title = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	return result
}

func displayWVCFileName(parsed parsedWVCFileName, fallback string) string {
	if parsed.title == "" {
		return filepath.Base(fallback)
	}
	title := parsed.title
	if parsed.media == "movie" && parsed.year != "" {
		title += " (" + parsed.year + ")"
	}
	if parsed.media == "tv" && parsed.season > 0 && parsed.episode > 0 {
		title = appendEpisodeLabel(title, parsed.season, parsed.episode)
	}
	return title
}

func appendEpisodeLabel(title string, season, episode int) string {
	return title + " · S" + twoDigitNumber(season) + "E" + twoDigitNumber(episode)
}

func twoDigitNumber(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func searchTMDbMetadata(ctx context.Context, apiKey string, parsed parsedWVCFileName) (wvcMediaMetadata, bool) {
	endpoint, _ := url.Parse(tmdbAPIBase + "/search/" + parsed.media)
	query := endpoint.Query()
	query.Set("api_key", apiKey)
	query.Set("query", parsed.title)
	query.Set("include_adult", "false")
	if parsed.year != "" {
		if parsed.media == "tv" {
			query.Set("first_air_date_year", parsed.year)
		} else {
			query.Set("year", parsed.year)
		}
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return wvcMediaMetadata{}, false
	}
	request.Header.Set("Accept", "application/json")
	response, err := wvcMetadataClient.Do(request)
	if err != nil {
		return wvcMediaMetadata{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return wvcMediaMetadata{}, false
	}
	var payload tmdbSearchResponse
	if json.NewDecoder(response.Body).Decode(&payload) != nil || len(payload.Results) == 0 {
		return wvcMediaMetadata{}, false
	}
	result := payload.Results[0]
	title := strings.TrimSpace(result.Title)
	date := result.ReleaseDate
	if parsed.media == "tv" {
		title = strings.TrimSpace(result.Name)
		date = result.FirstAir
	}
	if title == "" {
		title = parsed.title
	}
	if len(date) >= 4 && parsed.media == "movie" {
		title += " (" + date[:4] + ")"
	}
	poster := ""
	if strings.HasPrefix(result.PosterPath, "/") {
		poster = tmdbImageBase + result.PosterPath
	}
	return wvcMediaMetadata{Title: title, Poster: poster}, true
}

func wvcMIMEType(fileName, stored string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(stored)), "video/") {
		return strings.TrimSpace(stored)
	}
	switch strings.ToLower(filepath.Ext(fileName)) {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".ts", ".m2ts":
		return "video/mp2t"
	default:
		return "video/*"
	}
}
