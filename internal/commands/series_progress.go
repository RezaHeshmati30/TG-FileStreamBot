package commands

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const (
	seriesProgressStartPrefix   = "sp:"
	seriesProgressSessionPrefix = "spv:"
	seriesProgressSessionLife   = 30 * time.Minute
)

type seriesProgressCandidate struct {
	Provider    string  `json:"provider"`
	SeriesID    string  `json:"seriesId"`
	SeriesTitle string  `json:"seriesTitle"`
	SeriesYear  string  `json:"seriesYear"`
	PosterURL   *string `json:"posterUrl"`
}

type seriesProgressSession struct {
	mu         sync.Mutex
	ID         string
	UserID     int64
	Query      mediaQuery
	Candidates []seriesProgressCandidate
	Selected   int
	ExpiresAt  time.Time
}

type seriesProgressSummary struct {
	Series []struct {
		Provider        string `json:"provider"`
		SeriesID        string `json:"seriesId"`
		SeriesTitle     string `json:"seriesTitle"`
		SeriesYear      string `json:"seriesYear"`
		WatchedEpisodes []struct {
			SeasonNumber  int `json:"seasonNumber"`
			EpisodeNumber int `json:"episodeNumber"`
		} `json:"watchedEpisodes"`
		LastWatched struct {
			SeasonNumber  int `json:"seasonNumber"`
			EpisodeNumber int `json:"episodeNumber"`
		} `json:"lastWatched"`
	} `json:"series"`
}

var (
	seriesProgressHTTPClient = &http.Client{Timeout: 15 * time.Second}
	seriesProgressSessions   = struct {
		sync.Mutex
		items map[string]*seriesProgressSession
	}{items: make(map[string]*seriesProgressSession)}
)

func (m *command) LoadSeriesProgress(dispatcher dispatcher.Dispatcher) {
	dispatcher.AddHandler(handlers.NewCallbackQuery(nil, handleSeriesProgressCallback))
	m.log.Named("series-progress").Info("Loaded", zap.Bool("enabled", seriesProgressAvailable()))
}

func seriesProgressAvailable() bool {
	return strings.TrimSpace(config.ValueOf.SeriesProgressAPIURL) != "" && len(strings.TrimSpace(config.ValueOf.SeriesProgressAPIKey)) >= 32
}

func seriesProgressStartCallback(messageID int, expires int64) []byte {
	signature := utils.SignSeriesProgressAction(messageID, expires)
	return []byte(fmt.Sprintf("%ss:%d:%d:%s", seriesProgressStartPrefix, messageID, expires, signature))
}

func parseSeriesProgressStart(data []byte) (int, int64, bool) {
	parts := strings.Split(string(data), ":")
	if len(parts) != 5 || parts[0] != "sp" || parts[1] != "s" {
		return 0, 0, false
	}
	messageID, messageErr := strconv.Atoi(parts[2])
	expires, expiresErr := strconv.ParseInt(parts[3], 10, 64)
	if messageErr != nil || expiresErr != nil || messageID <= 0 || expires <= 0 {
		return 0, 0, false
	}
	expected := utils.SignSeriesProgressAction(messageID, expires)
	return messageID, expires, hmac.Equal([]byte(parts[4]), []byte(expected))
}

func seriesProgressSessionCallback(sessionID, action string, index int) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%d", seriesProgressSessionPrefix, sessionID, action, index))
}

func handleSeriesProgressCallback(ctx *ext.Context, update *ext.Update) error {
	if update.CallbackQuery == nil {
		return nil
	}
	data := string(update.CallbackQuery.Data)
	if !strings.HasPrefix(data, seriesProgressStartPrefix) && !strings.HasPrefix(data, seriesProgressSessionPrefix) {
		return nil
	}
	if !isPrivateChat(ctx, update) || update.EffectiveUser() == nil || !isAuthorized(update.EffectiveUser().ID) {
		answerSubtitleCallback(ctx, update, "You are not authorized to use this function.", true)
		return dispatcher.EndGroups
	}
	if !seriesProgressAvailable() {
		answerSubtitleCallback(ctx, update, "Series progress is not configured.", true)
		return dispatcher.EndGroups
	}

	if strings.HasPrefix(data, seriesProgressSessionPrefix) {
		return handleSeriesProgressSessionCallback(ctx, update)
	}
	messageID, expires, ok := parseSeriesProgressStart(update.CallbackQuery.Data)
	if !ok {
		answerSubtitleCallback(ctx, update, "This progress action is invalid.", true)
		return dispatcher.EndGroups
	}
	if time.Now().Unix() > expires {
		answerSubtitleCallback(ctx, update, "This file link has expired.", true)
		return dispatcher.EndGroups
	}
	answerSubtitleCallback(ctx, update, "Identifying series…", false)
	file, err := subtitleSourceFile(ctx, messageID)
	if err != nil {
		return seriesProgressFailure(ctx, update, "The original video is no longer available.", err)
	}
	query := parseMediaFileName(file.FileName)
	if query.Type != "series" || query.Title == "" || query.Season == "" || query.Episode == "" {
		sendSubtitleText(ctx, update, "❌ Series, season, and episode could not be identified from this filename.", nil)
		return dispatcher.EndGroups
	}
	candidates, err := resolveSeriesProgressCandidates(ctx, update.CallbackQuery.UserID, query)
	if err != nil {
		return seriesProgressFailure(ctx, update, "CineRate Pro could not identify this series right now.", err)
	}
	if len(candidates) == 0 {
		sendSubtitleText(ctx, update, "🔍 No matching series was found in CineRate Pro.", nil)
		return dispatcher.EndGroups
	}
	session, err := newSeriesProgressSession(update.CallbackQuery.UserID, query, candidates)
	if err != nil {
		return seriesProgressFailure(ctx, update, "The progress selection could not be started.", err)
	}
	showSeriesProgressConfirmation(ctx, update, session, false)
	return dispatcher.EndGroups
}

func handleSeriesProgressSessionCallback(ctx *ext.Context, update *ext.Update) error {
	parts := strings.Split(string(update.CallbackQuery.Data), ":")
	if len(parts) != 4 || parts[0] != "spv" {
		return dispatcher.EndGroups
	}
	session := getSeriesProgressSession(parts[1], update.CallbackQuery.UserID)
	if session == nil {
		answerSubtitleCallback(ctx, update, "This progress selection has expired. Please start it again.", true)
		return dispatcher.EndGroups
	}
	index, err := strconv.Atoi(parts[3])
	if err != nil {
		return dispatcher.EndGroups
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	answerSubtitleCallback(ctx, update, "Loading…", false)

	switch parts[2] {
	case "c":
		if index < 0 || index >= len(session.Candidates) {
			return dispatcher.EndGroups
		}
		session.Selected = index
		showSeriesProgressConfirmation(ctx, update, session, true)
	case "l":
		showSeriesProgressCandidates(ctx, update, session)
	case "s":
		return saveSelectedSeriesProgress(ctx, update, session)
	case "p":
		return showCurrentSeriesProgress(ctx, update, session)
	}
	return dispatcher.EndGroups
}

func newSeriesProgressSession(userID int64, query mediaQuery, candidates []seriesProgressCandidate) (*seriesProgressSession, error) {
	random := make([]byte, 9)
	if _, err := cryptorand.Read(random); err != nil {
		return nil, err
	}
	session := &seriesProgressSession{
		ID: base64.RawURLEncoding.EncodeToString(random), UserID: userID, Query: query,
		Candidates: candidates, ExpiresAt: time.Now().Add(seriesProgressSessionLife),
	}
	seriesProgressSessions.Lock()
	for key, item := range seriesProgressSessions.items {
		if time.Now().After(item.ExpiresAt) {
			delete(seriesProgressSessions.items, key)
		}
	}
	seriesProgressSessions.items[session.ID] = session
	seriesProgressSessions.Unlock()
	return session, nil
}

func getSeriesProgressSession(id string, userID int64) *seriesProgressSession {
	seriesProgressSessions.Lock()
	defer seriesProgressSessions.Unlock()
	session := seriesProgressSessions.items[id]
	if session == nil || session.UserID != userID || time.Now().After(session.ExpiresAt) {
		delete(seriesProgressSessions.items, id)
		return nil
	}
	session.ExpiresAt = time.Now().Add(seriesProgressSessionLife)
	return session
}

func showSeriesProgressConfirmation(ctx *ext.Context, update *ext.Update, session *seriesProgressSession, edit bool) {
	candidate := session.Candidates[session.Selected]
	text := fmt.Sprintf("📺 Is this the correct series?\n\n🎬 %s\n📅 %s\n📍 Season %s · Episode %s", candidate.SeriesTitle, valueOrUnknown(candidate.SeriesYear), session.Query.Season, session.Query.Episode)
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "✅ Mark as watched", Data: seriesProgressSessionCallback(session.ID, "s", 0)}}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "📍 Current progress", Data: seriesProgressSessionCallback(session.ID, "p", 0)}}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "🔍 Choose another series", Data: seriesProgressSessionCallback(session.ID, "l", 0)}}},
	}}
	if edit {
		editOnlineView(ctx, update, text, markup)
	} else {
		sendSubtitleText(ctx, update, text, markup)
	}
}

func showSeriesProgressCandidates(ctx *ext.Context, update *ext.Update, session *seriesProgressSession) {
	rows := make([]tg.KeyboardButtonRow, 0, len(session.Candidates)+1)
	for index, candidate := range session.Candidates {
		label := fmt.Sprintf("📺 %s (%s)", candidate.SeriesTitle, valueOrUnknown(candidate.SeriesYear))
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCallback{Text: truncateRunes(label, 60), Data: seriesProgressSessionCallback(session.ID, "c", index)},
		}})
	}
	rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonCallback{Text: "⬅️ Back", Data: seriesProgressSessionCallback(session.ID, "c", session.Selected)},
	}})
	editOnlineView(ctx, update, "🔍 Choose the correct series.", &tg.ReplyInlineMarkup{Rows: rows})
}

func saveSelectedSeriesProgress(ctx *ext.Context, update *ext.Update, session *seriesProgressSession) error {
	season, seasonErr := strconv.Atoi(session.Query.Season)
	episode, episodeErr := strconv.Atoi(session.Query.Episode)
	if seasonErr != nil || episodeErr != nil {
		return seriesProgressFailure(ctx, update, "Season or episode is invalid.", errors.New("invalid parsed episode numbers"))
	}
	candidate := session.Candidates[session.Selected]
	summary, err := saveSeriesProgress(ctx, session.UserID, candidate, season, episode)
	if err != nil {
		return seriesProgressFailure(ctx, update, "The episode could not be saved in CineRate Pro.", err)
	}
	count := 0
	if len(summary.Series) > 0 {
		count = len(summary.Series[0].WatchedEpisodes)
	}
	text := fmt.Sprintf("✅ Episode marked as watched\n\n🎬 %s\n📍 Season %d · Episode %d\n📊 %d watched episode(s) saved", candidate.SeriesTitle, season, episode, count)
	editOnlineView(ctx, update, text, nil)
	return dispatcher.EndGroups
}

func showCurrentSeriesProgress(ctx *ext.Context, update *ext.Update, session *seriesProgressSession) error {
	candidate := session.Candidates[session.Selected]
	summary, err := loadSeriesProgress(ctx, session.UserID, candidate)
	if err != nil {
		return seriesProgressFailure(ctx, update, "The current progress could not be loaded.", err)
	}
	text := fmt.Sprintf("📍 Series progress\n\n🎬 %s\n", candidate.SeriesTitle)
	if len(summary.Series) == 0 || len(summary.Series[0].WatchedEpisodes) == 0 {
		text += "📊 No watched episodes saved yet."
	} else {
		series := summary.Series[0]
		text += fmt.Sprintf("📊 %d watched episode(s)\n⏮ Last marked: Season %d · Episode %d", len(series.WatchedEpisodes), series.LastWatched.SeasonNumber, series.LastWatched.EpisodeNumber)
	}
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonCallback{Text: "⬅️ Back", Data: seriesProgressSessionCallback(session.ID, "c", session.Selected)},
	}}}}
	editOnlineView(ctx, update, text, markup)
	return dispatcher.EndGroups
}

func resolveSeriesProgressCandidates(ctx context.Context, userID int64, query mediaQuery) ([]seriesProgressCandidate, error) {
	endpoint, err := seriesProgressEndpoint()
	if err != nil {
		return nil, err
	}
	values := endpoint.Query()
	values.Set("action", "resolve")
	values.Set("title", query.Title)
	if query.Year != "" {
		values.Set("year", query.Year)
	}
	endpoint.RawQuery = values.Encode()
	var response struct {
		Candidates []seriesProgressCandidate `json:"candidates"`
	}
	if err := seriesProgressRequest(ctx, http.MethodGet, endpoint.String(), userID, nil, &response); err != nil {
		return nil, err
	}
	return response.Candidates, nil
}

func saveSeriesProgress(ctx context.Context, userID int64, candidate seriesProgressCandidate, season, episode int) (seriesProgressSummary, error) {
	payload := map[string]any{
		"provider": candidate.Provider, "seriesId": candidate.SeriesID, "seriesTitle": candidate.SeriesTitle,
		"seriesYear": candidate.SeriesYear, "posterUrl": candidate.PosterURL,
		"seasonNumber": season, "episodeNumber": episode,
	}
	var response seriesProgressSummary
	endpoint, err := seriesProgressEndpoint()
	if err != nil {
		return response, err
	}
	err = seriesProgressRequest(ctx, http.MethodPost, endpoint.String(), userID, payload, &response)
	return response, err
}

func loadSeriesProgress(ctx context.Context, userID int64, candidate seriesProgressCandidate) (seriesProgressSummary, error) {
	var response seriesProgressSummary
	endpoint, err := seriesProgressEndpoint()
	if err != nil {
		return response, err
	}
	values := endpoint.Query()
	values.Set("provider", candidate.Provider)
	values.Set("seriesId", candidate.SeriesID)
	endpoint.RawQuery = values.Encode()
	err = seriesProgressRequest(ctx, http.MethodGet, endpoint.String(), userID, nil, &response)
	return response, err
}

func seriesProgressEndpoint() (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(config.ValueOf.SeriesProgressAPIURL))
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") {
		return nil, errors.New("invalid SERIES_PROGRESS_API_URL")
	}
	return endpoint, nil
}

func seriesProgressRequest(ctx context.Context, method, endpoint string, userID int64, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Series-Progress-Key", strings.TrimSpace(config.ValueOf.SeriesProgressAPIKey))
	request.Header.Set("X-Telegram-User-Id", strconv.FormatInt(userID, 10))
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := seriesProgressHTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("CineRate Pro returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(target)
}

func seriesProgressFailure(ctx *ext.Context, update *ext.Update, userMessage string, err error) error {
	utils.Logger.Error("Series progress operation failed", zap.Error(err))
	sendSubtitleText(ctx, update, "❌ "+userMessage, nil)
	return dispatcher.EndGroups
}
