package mediametadata

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
	"unicode"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/types"
)

const (
	tmdbAPIBase   = "https://api.themoviedb.org/3"
	tmdbImageBase = "https://image.tmdb.org/t/p/w500"
)

var (
	yearPattern    = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	episodePattern = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b`)
	releaseCut     = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|bluray|blu-ray|web[ ._-]?dl|webrip|hdtv|dvdrip|remux|x26[45]|h[ ._-]?26[45]|hevc|avc|hdr10?|dolby[ ._-]?vision|dv|proper|repack|multi|dubbed)\b`)
	httpClient     = &http.Client{Timeout: 5 * time.Second}
	cache          = struct {
		sync.RWMutex
		items map[string]cachedMetadata
	}{items: make(map[string]cachedMetadata)}
	inflight = struct {
		sync.Mutex
		items map[string]*lookup
	}{items: make(map[string]*lookup)}
)

// Metadata contains display information shared by Telegram messages and WVC.
type Metadata struct {
	Name     string
	Title    string
	Year     string
	Poster   string
	MIMEType string
	Media    string
	Season   int
	Episode  int
}

type cachedMetadata struct {
	metadata  Metadata
	expiresAt time.Time
}

type parsedFileName struct {
	title   string
	year    string
	media   string
	season  int
	episode int
}

type lookup struct {
	done     chan struct{}
	metadata Metadata
	ok       bool
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

// Available reports whether online enrichment is configured.
func Available() bool {
	return strings.TrimSpace(config.ValueOf.TMDbAPIKey) != ""
}

// Local derives useful metadata without performing network requests.
func Local(file *types.File) Metadata {
	parsed := parseFileName(file.FileName)
	name := parsed.title
	if name == "" {
		name = filepath.Base(file.FileName)
	}
	return Metadata{
		Name:     name,
		Title:    displayTitle(name, parsed.year, parsed.media, parsed.season, parsed.episode),
		Year:     parsed.year,
		MIMEType: mimeType(file.FileName, file.MimeType),
		Media:    parsed.media,
		Season:   parsed.season,
		Episode:  parsed.episode,
	}
}

// Resolve returns local metadata immediately when TMDb is unavailable or no
// unambiguous matching title can be found.
func Resolve(ctx context.Context, file *types.File) Metadata {
	local := Local(file)
	apiKey := strings.TrimSpace(config.ValueOf.TMDbAPIKey)
	parsed := parseFileName(file.FileName)
	if apiKey == "" || parsed.title == "" {
		return local
	}

	cacheKey := strings.ToLower(strings.Join([]string{parsed.media, parsed.title, parsed.year}, "|"))
	if metadata, ok := cached(cacheKey, local); ok {
		return withEpisode(metadata, local)
	}

	resolved, ok := sharedLookup(ctx, cacheKey, func() (Metadata, bool) {
		return searchTMDb(ctx, apiKey, parsed)
	})
	if !ok {
		return local
	}
	return withEpisode(resolved, local)
}

func cached(key string, local Metadata) (Metadata, bool) {
	cache.RLock()
	item, found := cache.items[key]
	cache.RUnlock()
	if !found || time.Now().After(item.expiresAt) {
		return Metadata{}, false
	}
	return item.metadata, true
}

func sharedLookup(ctx context.Context, key string, run func() (Metadata, bool)) (Metadata, bool) {
	inflight.Lock()
	if existing := inflight.items[key]; existing != nil {
		inflight.Unlock()
		select {
		case <-existing.done:
			return existing.metadata, existing.ok
		case <-ctx.Done():
			return Metadata{}, false
		}
	}
	current := &lookup{done: make(chan struct{})}
	inflight.items[key] = current
	inflight.Unlock()

	current.metadata, current.ok = run()
	if current.ok {
		cache.Lock()
		cache.items[key] = cachedMetadata{metadata: current.metadata, expiresAt: time.Now().Add(24 * time.Hour)}
		cache.Unlock()
	}
	inflight.Lock()
	delete(inflight.items, key)
	close(current.done)
	inflight.Unlock()
	return current.metadata, current.ok
}

func withEpisode(metadata, local Metadata) Metadata {
	metadata.MIMEType = local.MIMEType
	metadata.Media = local.Media
	metadata.Season = local.Season
	metadata.Episode = local.Episode
	metadata.Title = displayTitle(metadata.Name, metadata.Year, metadata.Media, metadata.Season, metadata.Episode)
	return metadata
}

func parseFileName(fileName string) parsedFileName {
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	name = strings.NewReplacer(".", " ", "_", " ", "[", " ", "]", " ").Replace(name)
	result := parsedFileName{media: "movie"}
	if match := episodePattern.FindStringSubmatchIndex(name); len(match) == 6 {
		result.media = "tv"
		result.season, _ = strconv.Atoi(name[match[2]:match[3]])
		result.episode, _ = strconv.Atoi(name[match[4]:match[5]])
		name = name[:match[0]]
	}
	if match := yearPattern.FindStringSubmatch(name); len(match) == 2 {
		result.year = match[1]
		name = strings.Split(name, match[1])[0]
	}
	if location := releaseCut.FindStringIndex(name); location != nil {
		name = name[:location[0]]
	}
	result.title = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	return result
}

func displayTitle(name, year, media string, season, episode int) string {
	title := name
	if media == "movie" && year != "" {
		title += " (" + year + ")"
	}
	if media == "tv" && season > 0 && episode > 0 {
		title += " · S" + twoDigits(season) + "E" + twoDigits(episode)
	}
	return title
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

func searchTMDb(ctx context.Context, apiKey string, parsed parsedFileName) (Metadata, bool) {
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
		return Metadata{}, false
	}
	request.Header.Set("Accept", "application/json")
	response, err := httpClient.Do(request)
	if err != nil {
		return Metadata{}, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Metadata{}, false
	}
	var payload tmdbSearchResponse
	if json.NewDecoder(response.Body).Decode(&payload) != nil {
		return Metadata{}, false
	}

	type candidate struct {
		metadata Metadata
		score    int
	}
	candidates := make([]candidate, 0, len(payload.Results))
	for _, result := range payload.Results {
		name, date := strings.TrimSpace(result.Title), result.ReleaseDate
		if parsed.media == "tv" {
			name, date = strings.TrimSpace(result.Name), result.FirstAir
		}
		if name == "" || normalizeTitle(name) != normalizeTitle(parsed.title) {
			continue
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		score := 100
		if parsed.year != "" {
			if year == parsed.year {
				score += 30
			} else {
				score -= 30
			}
		}
		poster := ""
		if strings.HasPrefix(result.PosterPath, "/") {
			poster = tmdbImageBase + result.PosterPath
		}
		candidates = append(candidates, candidate{metadata: Metadata{Name: name, Year: year, Poster: poster, Media: parsed.media}, score: score})
	}
	if len(candidates) == 0 {
		return Metadata{}, false
	}
	best := candidates[0]
	secondScore := -1
	for _, item := range candidates[1:] {
		if item.score > best.score {
			secondScore = best.score
			best = item
		} else if item.score > secondScore {
			secondScore = item.score
		}
	}
	if best.score < 100 || (parsed.year == "" && secondScore == best.score) {
		return Metadata{}, false
	}
	best.metadata.Title = displayTitle(best.metadata.Name, best.metadata.Year, best.metadata.Media, 0, 0)
	return best.metadata, true
}

func normalizeTitle(value string) string {
	var normalized strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			normalized.WriteRune(char)
		}
	}
	return normalized.String()
}

func mimeType(fileName, stored string) string {
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
