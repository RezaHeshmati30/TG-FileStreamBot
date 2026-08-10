package commands

import (
	"archive/zip"
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const (
	onlineSubtitlePrefix   = "oss:"
	onlineSubtitlePageSize = 10
	onlineSessionLifetime  = 30 * time.Minute
	subsourceBaseURL       = "https://api.subsource.net/api/v1"
	maxSubsourceDownload   = 25 * 1024 * 1024
)

var (
	onlineSessions = struct {
		sync.RWMutex
		items map[string]*onlineSubtitleSession
	}{items: make(map[string]*onlineSubtitleSession)}
	onlineHTTPClient = &http.Client{Timeout: 30 * time.Second}
	yearPattern      = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	episodePattern   = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b`)
	releaseCut       = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|480p|bluray|blu-ray|web[ ._-]?dl|webrip|hdtv|dvdrip|remux|x26[45]|h[ ._-]?26[45]|hevc|avc)\b`)
)

type onlineSubtitleSession struct {
	mu              sync.Mutex
	ID              string
	UserID          int64
	SourceMessageID int
	LinkExpires     int64
	SourceFileName  string
	Query           mediaQuery
	Language        string
	Movies          []subsourceMovie
	SelectedMovie   int
	Subtitles       []subsourceSubtitle
	Page            int
	ExpiresAt       time.Time
}

type mediaQuery struct {
	Title   string
	Year    string
	Type    string
	Season  string
	Episode string
}

type subsourceMovie struct {
	ID    string
	Title string
	Year  string
	Type  string
}

type subsourceSubtitle struct {
	ID              string
	Name            string
	Language        string
	Season          string
	Episode         string
	ReleaseInfo     string
	Uploader        string
	Rating          string
	Downloads       int64
	HearingImpaired bool
	Files           []string
	Raw             map[string]any
}

type subtitleLanguage struct {
	Code  string
	API   string
	Label string
}

var subtitleLanguages = []subtitleLanguage{
	{Code: "de", API: "german", Label: "🇩🇪 German"},
	{Code: "en", API: "english", Label: "🇬🇧 English"},
	{Code: "fa", API: "farsi_persian", Label: "🇮🇷 Persian"},
	{Code: "es", API: "spanish", Label: "🇪🇸 Spanish"},
	{Code: "fr", API: "french", Label: "🇫🇷 French"},
	{Code: "it", API: "italian", Label: "🇮🇹 Italian"},
	{Code: "tr", API: "turkish", Label: "🇹🇷 Turkish"},
	{Code: "ar", API: "arabic", Label: "🇸🇦 Arabic"},
	{Code: "ru", API: "russian", Label: "🇷🇺 Russian"},
}

func onlineSubtitlesAvailable() bool {
	return strings.TrimSpace(config.ValueOf.SubsourceAPIKey) != ""
}

func startOnlineSubtitleSearch(ctx *ext.Context, u *ext.Update, messageID int, expires int64) error {
	if !onlineSubtitlesAvailable() {
		answerSubtitleCallback(ctx, u, "Online subtitle search is not configured.", true)
		return dispatcher.EndGroups
	}
	file, err := subtitleSourceFile(ctx, messageID)
	if err != nil {
		return subtitleFailure(ctx, u, "The original video is no longer available.", err)
	}
	query := parseMediaFileName(file.FileName)
	if query.Title == "" {
		sendSubtitleText(ctx, u, "🔎 A searchable movie or series title could not be detected in the filename.", nil)
		return dispatcher.EndGroups
	}
	session, err := newOnlineSession(u.CallbackQuery.UserID, messageID, expires, file.FileName, query)
	if err != nil {
		return subtitleFailure(ctx, u, "The online subtitle search could not be started.", err)
	}
	session.Language = preferredSubtitleLanguage(session.UserID)
	if session.Language == "" {
		sendSubtitleText(ctx, u, languageViewText(), languageViewMarkup(session.ID, ""))
		return dispatcher.EndGroups
	}
	return searchAndShowMovies(ctx, u, session, false)
}

func handleOnlineSubtitleCallback(ctx *ext.Context, u *ext.Update) error {
	if u.CallbackQuery == nil || !strings.HasPrefix(string(u.CallbackQuery.Data), onlineSubtitlePrefix) {
		return nil
	}
	if u.EffectiveUser() == nil || !isAuthorized(u.EffectiveUser().ID) {
		answerSubtitleCallback(ctx, u, "You are not authorized to use this function.", true)
		return dispatcher.EndGroups
	}
	parts := strings.Split(string(u.CallbackQuery.Data), ":")
	if len(parts) != 4 {
		answerSubtitleCallback(ctx, u, "This search action is invalid.", true)
		return dispatcher.EndGroups
	}
	session := getOnlineSession(parts[1], u.CallbackQuery.UserID)
	if session == nil {
		answerSubtitleCallback(ctx, u, "This search has expired. Please start it again.", true)
		return dispatcher.EndGroups
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	action, arg := parts[2], parts[3]
	answerSubtitleCallback(ctx, u, "Loading…", false)

	switch action {
	case "l":
		if !validSubtitleLanguage(arg) {
			return dispatcher.EndGroups
		}
		if err := savePreferredSubtitleLanguage(ctx, session.UserID, arg); err != nil {
			return onlineFailure(ctx, u, "Your subtitle language could not be saved.", err)
		}
		session.Language = arg
		return searchAndShowMovies(ctx, u, session, true)
	case "cl":
		return editOnlineView(ctx, u, languageViewText(), languageViewMarkup(session.ID, session.Language))
	case "m":
		index, err := strconv.Atoi(arg)
		if err != nil || index < 0 || index >= len(session.Movies) {
			return dispatcher.EndGroups
		}
		session.SelectedMovie = index
		return showMovieConfirmation(ctx, u, session, true)
	case "ml":
		return showMovieList(ctx, u, session)
	case "mc":
		return showMovieConfirmation(ctx, u, session, true)
	case "s":
		return loadAndShowSubtitles(ctx, u, session)
	case "pg":
		page, err := strconv.Atoi(arg)
		if err == nil {
			session.Page = page
		}
		return showSubtitlePage(ctx, u, session)
	case "dl":
		index, err := strconv.Atoi(arg)
		if err != nil || index < 0 || index >= len(session.Subtitles) {
			return dispatcher.EndGroups
		}
		return downloadOnlineSubtitle(ctx, u, session, index)
	default:
		return dispatcher.EndGroups
	}
}

func newOnlineSession(userID int64, messageID int, expires int64, fileName string, query mediaQuery) (*onlineSubtitleSession, error) {
	random := make([]byte, 9)
	if _, err := cryptorand.Read(random); err != nil {
		return nil, err
	}
	id := base64.RawURLEncoding.EncodeToString(random)
	session := &onlineSubtitleSession{
		ID:              id,
		UserID:          userID,
		SourceMessageID: messageID,
		LinkExpires:     expires,
		SourceFileName:  fileName,
		Query:           query,
		SelectedMovie:   0,
		ExpiresAt:       time.Now().Add(onlineSessionLifetime),
	}
	onlineSessions.Lock()
	for key, item := range onlineSessions.items {
		if time.Now().After(item.ExpiresAt) {
			delete(onlineSessions.items, key)
		}
	}
	onlineSessions.items[id] = session
	onlineSessions.Unlock()
	return session, nil
}

func getOnlineSession(id string, userID int64) *onlineSubtitleSession {
	onlineSessions.Lock()
	session := onlineSessions.items[id]
	if session != nil && session.UserID == userID && time.Now().Before(session.ExpiresAt) {
		session.ExpiresAt = time.Now().Add(onlineSessionLifetime)
	}
	onlineSessions.Unlock()
	if session == nil || session.UserID != userID || time.Now().After(session.ExpiresAt) {
		return nil
	}
	return session
}

func onlineCallback(sessionID, action, arg string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%s", onlineSubtitlePrefix, sessionID, action, arg))
}

func parseMediaFileName(fileName string) mediaQuery {
	name := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	name = strings.NewReplacer(".", " ", "_", " ", "[", " ", "]", " ").Replace(name)
	query := mediaQuery{Type: "movie"}
	if match := episodePattern.FindStringSubmatch(name); len(match) == 3 {
		query.Type, query.Season, query.Episode = "series", trimNumber(match[1]), trimNumber(match[2])
		name = episodePattern.ReplaceAllString(name, " ")
	}
	if match := yearPattern.FindStringSubmatch(name); len(match) == 2 {
		query.Year = match[1]
		name = strings.Split(name, match[1])[0]
	}
	if location := releaseCut.FindStringIndex(name); location != nil {
		name = name[:location[0]]
	}
	name = regexp.MustCompile(`(?i)\b(?:complete|season|multi|dubbed|proper|repack)\b.*$`).ReplaceAllString(name, "")
	query.Title = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	return query
}

func trimNumber(value string) string {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return value
	}
	return strconv.Itoa(parsed)
}

func languageViewText() string {
	return "🌍 Subtitle language\n\nChoose your preferred language. The bot will save it and use it automatically for future searches. You can change it later from the search menu."
}

func languageViewMarkup(sessionID, selected string) *tg.ReplyInlineMarkup {
	rows := make([]tg.KeyboardButtonRow, 0, len(subtitleLanguages))
	for _, language := range subtitleLanguages {
		label := language.Label
		if language.Code == selected {
			label = "✓ " + label
		}
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{
			Text: label, Data: onlineCallback(sessionID, "l", language.Code),
		}}})
	}
	if selected != "" {
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{
			Text: "⬅️ Back", Data: onlineCallback(sessionID, "mc", "0"),
		}}})
	}
	return &tg.ReplyInlineMarkup{Rows: rows}
}

func validSubtitleLanguage(code string) bool {
	for _, language := range subtitleLanguages {
		if language.Code == code {
			return true
		}
	}
	return false
}

func subsourceLanguageName(code string) string {
	for _, language := range subtitleLanguages {
		if language.Code == code {
			return language.API
		}
	}
	return "english"
}

func subtitleLanguageLabel(code string) string {
	for _, language := range subtitleLanguages {
		if language.Code == code {
			return language.Label
		}
	}
	return code
}

func searchAndShowMovies(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession, edit bool) error {
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	movies, err := searchSubsourceMovies(requestCtx, session.Query)
	if err != nil {
		return onlineFailure(ctx, u, "Subsource could not be searched right now.", err)
	}
	if len(movies) == 0 {
		message := fmt.Sprintf("🔎 No matching title was found for:\n\n%s\n\nTry renaming the source file with a clearer title and year.", session.Query.Title)
		if edit {
			return editOnlineView(ctx, u, message, languageChangeMarkup(session.ID))
		}
		sendSubtitleText(ctx, u, message, languageChangeMarkup(session.ID))
		return dispatcher.EndGroups
	}
	session.Movies = movies
	session.SelectedMovie = 0
	if edit {
		return showMovieConfirmation(ctx, u, session, true)
	}
	return showMovieConfirmation(ctx, u, session, false)
}

func showMovieConfirmation(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession, edit bool) error {
	movie := session.Movies[session.SelectedMovie]
	text := fmt.Sprintf("🔎 Is this the correct title?\n\n🎬 %s\n📅 %s\n🎞 %s\n🌍 %s", movie.Title, valueOrUnknown(movie.Year), displayMediaType(movie.Type), subtitleLanguageLabel(session.Language))
	if session.Query.Type == "series" {
		text += fmt.Sprintf("\n📺 Season %s · Episode %s", session.Query.Season, session.Query.Episode)
	}
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "✅ Yes", Data: onlineCallback(session.ID, "s", "0")}}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🔍 Choose another result", Data: onlineCallback(session.ID, "ml", "0")}}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🌍 Change language", Data: onlineCallback(session.ID, "cl", "0")}}},
	}}
	if edit {
		return editOnlineView(ctx, u, text, markup)
	}
	sendSubtitleText(ctx, u, text, markup)
	return dispatcher.EndGroups
}

func showMovieList(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession) error {
	rows := make([]tg.KeyboardButtonRow, 0, len(session.Movies)+2)
	limit := len(session.Movies)
	if limit > 10 {
		limit = 10
	}
	for index, movie := range session.Movies[:limit] {
		label := fmt.Sprintf("🎬 %s (%s)", movie.Title, valueOrUnknown(movie.Year))
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: truncateRunes(label, 60), Data: onlineCallback(session.ID, "m", strconv.Itoa(index))}}})
	}
	rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "⬅️ Back", Data: onlineCallback(session.ID, "mc", "0")}}})
	return editOnlineView(ctx, u, "🔍 Search results\n\nChoose the correct movie or series.", &tg.ReplyInlineMarkup{Rows: rows})
}

func loadAndShowSubtitles(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession) error {
	movie := session.Movies[session.SelectedMovie]
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	results, err := searchSubsourceSubtitles(requestCtx, movie.ID, session.Language, session.Query)
	if err != nil {
		return onlineFailure(ctx, u, "The available subtitles could not be loaded.", err)
	}
	session.Subtitles = results
	session.Page = 0
	return showSubtitlePage(ctx, u, session)
}

func showSubtitlePage(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession) error {
	if len(session.Subtitles) == 0 {
		return editOnlineView(ctx, u, "💬 No subtitles were found in the selected language.", &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🌍 Change language", Data: onlineCallback(session.ID, "cl", "0")}}},
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "⬅️ Back", Data: onlineCallback(session.ID, "mc", "0")}}},
		}})
	}
	pageCount := (len(session.Subtitles) + onlineSubtitlePageSize - 1) / onlineSubtitlePageSize
	if session.Page < 0 {
		session.Page = 0
	}
	if session.Page >= pageCount {
		session.Page = pageCount - 1
	}
	start := session.Page * onlineSubtitlePageSize
	end := start + onlineSubtitlePageSize
	if end > len(session.Subtitles) {
		end = len(session.Subtitles)
	}
	rows := make([]tg.KeyboardButtonRow, 0, onlineSubtitlePageSize+3)
	for index := start; index < end; index++ {
		subtitle := session.Subtitles[index]
		label := fmt.Sprintf("💬 %s", firstNonEmpty(subtitle.ReleaseInfo, subtitle.Name))
		if subtitle.Rating != "" {
			label += " · ⭐ " + subtitle.Rating
		}
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: truncateRunes(label, 60), Data: onlineCallback(session.ID, "dl", strconv.Itoa(index))}}})
	}
	var navigation []tg.KeyboardButtonClass
	if session.Page > 0 {
		navigation = append(navigation, &tg.KeyboardButtonCallback{Text: "⬅️ Previous", Data: onlineCallback(session.ID, "pg", strconv.Itoa(session.Page-1))})
	}
	if session.Page+1 < pageCount {
		navigation = append(navigation, &tg.KeyboardButtonCallback{Text: "More ➡️", Data: onlineCallback(session.ID, "pg", strconv.Itoa(session.Page+1))})
	}
	if len(navigation) > 0 {
		rows = append(rows, tg.KeyboardButtonRow{Buttons: navigation})
	}
	rows = append(rows,
		tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🌍 Change language", Data: onlineCallback(session.ID, "cl", "0")}}},
		tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "⬅️ Back to title", Data: onlineCallback(session.ID, "mc", "0")}}},
	)
	movie := session.Movies[session.SelectedMovie]
	text := fmt.Sprintf("💬 Online subtitles\n\n🎬 %s\n🌍 %s\n📄 Page %d of %d · %d results\n\nChoose a subtitle to download and prepare.", movie.Title, subtitleLanguageLabel(session.Language), session.Page+1, pageCount, len(session.Subtitles))
	return editOnlineView(ctx, u, text, &tg.ReplyInlineMarkup{Rows: rows})
}

func languageChangeMarkup(sessionID string) *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🌍 Change language", Data: onlineCallback(sessionID, "cl", "0")}}}}}
}

func editOnlineView(ctx *ext.Context, u *ext.Update, text string, markup tg.ReplyMarkupClass) error {
	peer := subtitlePeer(ctx, u)
	if peer.Zero() {
		return dispatcher.EndGroups
	}
	_, err := ctx.Raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{NoWebpage: true, Peer: peer, ID: u.CallbackQuery.MsgID, Message: text, ReplyMarkup: markup})
	if err != nil {
		utils.Logger.Error("Could not edit online subtitle view", zap.Error(err))
	}
	return dispatcher.EndGroups
}

func searchSubsourceMovies(ctx context.Context, query mediaQuery) ([]subsourceMovie, error) {
	parameterSets := subsourceMovieSearchQueries(query)
	var lastErr error
	for _, params := range parameterSets {
		var payload any
		if err := subsourceJSON(ctx, "/movies/search", params, &payload); err != nil {
			lastErr = err
			continue
		}
		movies := rankSubsourceMovies(normalizeSubsourceMovies(pickSubsourceArray(payload)), query)
		if len(movies) > 0 {
			return movies, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func subsourceMovieSearchQueries(query mediaQuery) []url.Values {
	withYear := url.Values{
		"searchType": {"text"},
		"q":          {query.Title},
		"type":       {"all"},
	}
	queries := make([]url.Values, 0, 3)
	if query.Year != "" {
		withYear.Set("year", query.Year)
		queries = append(queries, withYear)
		withoutYear := cloneURLValues(withYear)
		withoutYear.Del("year")
		queries = append(queries, withoutYear)
	} else {
		queries = append(queries, withYear)
	}

	words := strings.Fields(query.Title)
	if len(words) > 3 {
		shortQuery := cloneURLValues(withYear)
		shortQuery.Set("q", strings.Join(words[:3], " "))
		shortQuery.Del("year")
		queries = append(queries, shortQuery)
	}
	return queries
}

func cloneURLValues(values url.Values) url.Values {
	clone := make(url.Values, len(values))
	for key, entries := range values {
		clone[key] = append([]string(nil), entries...)
	}
	return clone
}

func searchSubsourceSubtitles(ctx context.Context, movieID, language string, query mediaQuery) ([]subsourceSubtitle, error) {
	params := url.Values{
		"movieId":  {movieID},
		"language": {subsourceLanguageName(language)},
		"limit":    {"50"},
		"page":     {"1"},
		"sort":     {"rating"},
	}
	if query.Type == "series" {
		params.Set("season", query.Season)
		params.Set("episode", query.Episode)
	}
	var payload any
	if err := subsourceJSON(ctx, "/subtitles", params, &payload); err != nil {
		return nil, err
	}
	return normalizeSubsourceSubtitles(pickSubsourceArray(payload), query), nil
}

func subsourceJSON(ctx context.Context, path string, params url.Values, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, subsourceBaseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("X-API-Key", config.ValueOf.SubsourceAPIKey)
	response, err := onlineHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		message := subsourceErrorMessage(body)
		if message != "" {
			return fmt.Errorf("Subsource returned HTTP %d: %s", response.StatusCode, utils.RedactSensitiveText(message))
		}
		return fmt.Errorf("Subsource returned HTTP %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(target)
}

func subsourceErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) == nil {
		for _, key := range []string{"message", "error", "detail"} {
			if message := firstStringValue(payload[key]); message != "" {
				return strings.TrimSpace(message)
			}
		}
	}
	return strings.TrimSpace(string(body))
}

func pickSubsourceArray(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	if record, ok := value.(map[string]any); ok {
		for _, key := range []string{"results", "data", "movies", "items", "subtitles", "subs"} {
			if values := pickSubsourceArray(record[key]); len(values) > 0 {
				return values
			}
		}
	}
	return nil
}

func normalizeSubsourceMovies(values []any) []subsourceMovie {
	result := make([]subsourceMovie, 0, len(values))
	for _, value := range values {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		movie := subsourceMovie{
			ID:    stringField(record, "movieId", "id", "_id", "imdb_id", "imdbId"),
			Title: stringField(record, "title", "name", "movieName", "releaseName"),
			Year:  stringField(record, "year", "releaseYear", "release_date"),
			Type:  stringField(record, "type", "movieType", "mediaType"),
		}
		if movie.ID != "" && movie.Title != "" {
			result = append(result, movie)
		}
	}
	return result
}

func rankSubsourceMovies(movies []subsourceMovie, query mediaQuery) []subsourceMovie {
	target := normalizeOnlineTitle(query.Title)
	sort.SliceStable(movies, func(i, j int) bool {
		return movieMatchScore(movies[i], target, query.Year) > movieMatchScore(movies[j], target, query.Year)
	})
	return movies
}

func movieMatchScore(movie subsourceMovie, target, year string) int {
	candidate := normalizeOnlineTitle(movie.Title)
	score := 0
	if candidate == target {
		score += 100
	} else if strings.Contains(candidate, target) || strings.Contains(target, candidate) {
		score += 40
	}
	if year != "" && strings.Contains(movie.Year, year) {
		score += 30
	}
	return score
}

func normalizeOnlineTitle(value string) string {
	value = strings.ToLower(value)
	value = regexp.MustCompile(`[^\pL\pN]+`).ReplaceAllString(value, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}

func normalizeSubsourceSubtitles(values []any, query mediaQuery) []subsourceSubtitle {
	result := make([]subsourceSubtitle, 0, len(values))
	for _, value := range values {
		record, ok := value.(map[string]any)
		if !ok {
			continue
		}
		subtitle := subsourceSubtitle{
			ID:              stringField(record, "subtitleId", "id", "subId"),
			Name:            stringField(record, "releaseName", "releaseInfo", "name", "title", "filename", "fileName"),
			Language:        stringField(record, "lang", "language", "languageName"),
			Season:          stringField(record, "season", "seasonNumber", "season_number"),
			Episode:         stringField(record, "episode", "episodeNumber", "episode_number"),
			ReleaseInfo:     firstStringValue(record["releaseInfo"]),
			Uploader:        stringField(record, "uploader", "author", "owner"),
			Rating:          stringField(record, "rating"),
			Downloads:       intField(record, "downloads", "downloadCount"),
			HearingImpaired: boolField(record, "hi", "hearingImpaired", "hearing_impaired"),
			Files:           normalizeOnlineFiles(record["files"]),
			Raw:             record,
		}
		if subtitle.ID == "" {
			continue
		}
		if query.Type == "series" && query.Episode != "" && !onlineSubtitleMatchesEpisode(subtitle, query.Season, query.Episode) {
			continue
		}
		result = append(result, subtitle)
	}
	return result
}

func onlineSubtitleMatchesEpisode(subtitle subsourceSubtitle, season, episode string) bool {
	s, _ := strconv.Atoi(season)
	e, _ := strconv.Atoi(episode)
	if s <= 0 || e <= 0 {
		return false
	}
	if subtitle.Season != "" || subtitle.Episode != "" {
		actualSeason, _ := strconv.Atoi(subtitle.Season)
		actualEpisode, _ := strconv.Atoi(subtitle.Episode)
		if actualSeason > 0 && actualEpisode > 0 {
			return actualSeason == s && actualEpisode == e
		}
	}
	text := strings.Join(append([]string{subtitle.Name, subtitle.ReleaseInfo}, subtitle.Files...), " ")
	return containsExactSeasonEpisode(text, s, e)
}

func containsExactSeasonEpisode(text string, season, episode int) bool {
	normalized := strings.ToLower(strings.NewReplacer(".", "", "_", "", "-", "", " ", "").Replace(text))
	patterns := []string{
		fmt.Sprintf("s%02de%02d", season, episode),
		fmt.Sprintf("s%de%02d", season, episode),
		fmt.Sprintf("s%02de%d", season, episode),
		fmt.Sprintf("s%de%d", season, episode),
		fmt.Sprintf("%dx%02d", season, episode),
		fmt.Sprintf("%dx%d", season, episode),
	}
	for _, pattern := range patterns {
		if strings.Contains(normalized, pattern) {
			return true
		}
	}
	return false
}

func downloadOnlineSubtitle(ctx *ext.Context, u *ext.Update, session *onlineSubtitleSession, index int) error {
	subtitle := session.Subtitles[index]
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	path, err := fetchAndExtractSubsourceSubtitle(requestCtx, subtitle, session.Query)
	if err != nil {
		return onlineFailure(ctx, u, "The selected subtitle could not be downloaded.", err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	result, err := uploadSubtitle(ctx, path)
	if err != nil {
		return onlineFailure(ctx, u, "The online subtitle could not be saved.", err)
	}
	result.sourceFileName = session.SourceFileName
	result.subtitleName = filepath.Base(path)
	return sendSubtitleResult(ctx, u, result, session.LinkExpires)
}

func fetchAndExtractSubsourceSubtitle(ctx context.Context, subtitle subsourceSubtitle, query mediaQuery) (string, error) {
	downloadURL := findSubsourceDownloadURL(subtitle.Raw)
	if downloadURL == "" {
		downloadURL = fmt.Sprintf("%s/subtitles/%s/download", subsourceBaseURL, url.PathEscape(subtitle.ID))
	}
	parsedURL, err := url.Parse(downloadURL)
	if err != nil || !strings.EqualFold(parsedURL.Hostname(), "api.subsource.net") {
		return "", errors.New("Subsource returned an invalid download URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("X-API-Key", config.ValueOf.SubsourceAPIKey)
	response, err := onlineHTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Subsource download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSubsourceDownload+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > maxSubsourceDownload {
		return "", errors.New("Subsource subtitle archive is empty or too large")
	}
	directory, err := os.MkdirTemp("", "fsb-online-subtitle-")
	if err != nil {
		return "", err
	}
	path, err := extractBestSubtitleFile(data, directory, query)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	return path, nil
}

func extractBestSubtitleFile(data []byte, directory string, query mediaQuery) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", errors.New("Subsource returned an unsupported archive format")
	}
	if len(archive.File) > 100 {
		return "", errors.New("subtitle archive contains too many files")
	}
	candidates := make([]*zip.File, 0)
	for _, file := range archive.File {
		extension := strings.ToLower(filepath.Ext(file.Name))
		if utils.Contains([]string{".srt", ".vtt", ".ass", ".ssa"}, extension) && !file.FileInfo().IsDir() && file.UncompressedSize64 <= 20*1024*1024 {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("archive contains no supported subtitle file")
	}
	selected := candidates[0]
	if query.Episode != "" {
		matched := false
		for _, candidate := range candidates {
			probe := subsourceSubtitle{Name: candidate.Name}
			if onlineSubtitleMatchesEpisode(probe, query.Season, query.Episode) {
				selected = candidate
				matched = true
				break
			}
		}
		if !matched {
			return "", fmt.Errorf("subtitle archive does not contain season %s episode %s", query.Season, query.Episode)
		}
	}
	reader, err := selected.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	name := safeOnlineSubtitleName(filepath.Base(selected.Name))
	path := filepath.Join(directory, name)
	output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	written, copyErr := io.Copy(output, io.LimitReader(reader, 20*1024*1024+1))
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written > 20*1024*1024 {
		return "", errors.New("extracted subtitle file is too large")
	}
	return path, nil
}

func findSubsourceDownloadURL(value any) string {
	if text, ok := value.(string); ok && strings.HasPrefix(text, "https://api.subsource.net/") {
		return text
	}
	if record, ok := value.(map[string]any); ok {
		for _, key := range []string{"download_url", "downloadUrl", "download", "url"} {
			if candidate := findSubsourceDownloadURL(record[key]); candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func safeOnlineSubtitleName(name string) string {
	extension := strings.ToLower(filepath.Ext(name))
	base := strings.TrimSuffix(name, filepath.Ext(name))
	base = regexp.MustCompile(`[^\pL\pN._ -]+`).ReplaceAllString(base, "_")
	base = truncateRunes(strings.Trim(base, " ._"), 120)
	if base == "" {
		base = "subtitle"
	}
	return base + extension
}

func stringField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := record[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		case json.Number:
			return value.String()
		case map[string]any:
			if nested := stringField(value, "total", "good", "username", "name", "displayName"); nested != "" {
				return nested
			}
		}
	}
	return ""
}

func firstStringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	if values, ok := value.([]any); ok && len(values) > 0 {
		return firstStringValue(values[0])
	}
	return ""
}

func intField(record map[string]any, keys ...string) int64 {
	value := stringField(record, keys...)
	parsed, _ := strconv.ParseInt(strings.Split(value, ".")[0], 10, 64)
	return parsed
}

func boolField(record map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := record[key].(bool); ok && value {
			return true
		}
		if value := strings.ToLower(stringField(record, key)); value == "true" || value == "1" {
			return true
		}
	}
	return false
}

func normalizeOnlineFiles(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	files := make([]string, 0, len(values))
	for _, value := range values {
		switch item := value.(type) {
		case string:
			files = append(files, item)
		case map[string]any:
			if name := stringField(item, "name", "fileName", "filename", "path"); name != "" {
				files = append(files, name)
			}
		}
	}
	return files
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func valueOrUnknown(value string) string {
	if value == "" {
		return "Unknown"
	}
	return value
}

func displayMediaType(value string) string {
	if strings.Contains(strings.ToLower(value), "series") || strings.Contains(strings.ToLower(value), "tv") {
		return "Series"
	}
	return "Movie"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "Subtitle"
}

func onlineFailure(ctx *ext.Context, u *ext.Update, userMessage string, err error) error {
	utils.Logger.Error("Online subtitle operation failed", zap.Error(err))
	sendSubtitleText(ctx, u, "❌ "+userMessage, nil)
	return dispatcher.EndGroups
}
