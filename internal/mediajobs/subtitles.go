package mediajobs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const maxOnlineSubtitleDownload = 25 * 1024 * 1024

var (
	seasonEpisodePattern = regexp.MustCompile(`(?i)\bS(\d{1,2})[ ._-]*E(\d{1,3})\b`)
	xEpisodePattern      = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{1,3})\b`)
	subtitleIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
)

func detectSeasonEpisode(fileName string) (string, string) {
	for _, pattern := range []*regexp.Regexp{seasonEpisodePattern, xEpisodePattern} {
		if match := pattern.FindStringSubmatch(fileName); len(match) == 3 {
			return strings.TrimLeft(match[1], "0"), strings.TrimLeft(match[2], "0")
		}
	}
	return "", ""
}

func (m *Manager) PrepareSubtitle(id, subtitleID, season, episode string) (*Job, error) {
	if !subtitleIDPattern.MatchString(subtitleID) {
		return nil, errors.New("invalid subtitle selection")
	}
	if season != "" && !regexp.MustCompile(`^\d{1,2}$`).MatchString(season) {
		return nil, errors.New("invalid season")
	}
	if episode != "" && !regexp.MustCompile(`^\d{1,3}$`).MatchString(episode) {
		return nil, errors.New("invalid episode")
	}
	m.mu.Lock()
	job, ok := m.jobs[id]
	if !ok || job.Status != StatusCompleted || job.videoMessageID <= 0 || job.videoExpires <= time.Now().Unix() {
		m.mu.Unlock()
		return nil, errors.New("completed video job was not found")
	}
	if job.SubtitleStatus == "preparing" {
		m.mu.Unlock()
		return nil, errors.New("a subtitle is already being prepared")
	}
	job.SubtitleStatus, job.SubtitleError, job.SubtitleWVCURL = "preparing", "", ""
	job.UpdatedAt = time.Now().UTC()
	result := cloneJob(job)
	m.mu.Unlock()
	go m.prepareSubtitle(job, subtitleID, season, episode)
	return result, nil
}

func (m *Manager) prepareSubtitle(job *Job, subtitleID, season, episode string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	path, err := downloadAndExtractSubtitle(ctx, subtitleID, season, episode)
	if err == nil {
		defer os.RemoveAll(filepath.Dir(path))
	}
	var subtitleMessageID int
	if err == nil {
		subtitleMessageID, err = m.uploadSubtitle(ctx, path)
	}
	if err != nil {
		m.mu.Lock()
		if job.SubtitleStatus == "preparing" {
			job.SubtitleStatus, job.SubtitleError, job.UpdatedAt = "failed", err.Error(), time.Now().UTC()
		}
		m.mu.Unlock()
		if m.log != nil {
			m.log.Warn("Subtitle preparation failed", zap.String("jobId", job.ID), zap.Error(err))
		}
		return
	}
	signature := utils.SignWVCLaunch(job.videoMessageID, subtitleMessageID, job.videoExpires)
	query := url.Values{"video": {strconv.Itoa(job.videoMessageID)}, "subtitle": {strconv.Itoa(subtitleMessageID)}, "expires": {strconv.FormatInt(job.videoExpires, 10)}, "signature": {signature}}
	wvcURL := strings.TrimRight(config.ValueOf.Host, "/") + "/wvc?" + query.Encode()
	m.mu.Lock()
	if job.SubtitleStatus == "preparing" {
		job.SubtitleStatus, job.SubtitleWVCURL, job.UpdatedAt = "ready", wvcURL, time.Now().UTC()
	}
	m.mu.Unlock()
}

func downloadAndExtractSubtitle(ctx context.Context, subtitleID, season, episode string) (string, error) {
	requestURL := "https://api.subsource.net/api/v1/subtitles/" + url.PathEscape(subtitleID) + "/download"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("X-API-Key", config.ValueOf.SubsourceAPIKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Subsource download returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxOnlineSubtitleDownload+1))
	if err != nil || len(data) == 0 || len(data) > maxOnlineSubtitleDownload {
		return "", errors.New("subtitle archive is empty or too large")
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(archive.File) > 100 {
		return "", errors.New("unsupported subtitle archive")
	}
	var candidates []*zip.File
	for _, file := range archive.File {
		ext := strings.ToLower(filepath.Ext(file.Name))
		if !file.FileInfo().IsDir() && file.UncompressedSize64 <= 20*1024*1024 && (ext == ".srt" || ext == ".vtt" || ext == ".ass" || ext == ".ssa") {
			candidates = append(candidates, file)
		}
	}
	if len(candidates) == 0 {
		return "", errors.New("archive contains no supported subtitle file")
	}
	selected := candidates[0]
	if episode != "" {
		wantedSeason, _ := strconv.Atoi(season)
		wantedEpisode, _ := strconv.Atoi(episode)
		matched := false
		for _, candidate := range candidates {
			s, e := detectSeasonEpisode(candidate.Name)
			si, _ := strconv.Atoi(s)
			ei, _ := strconv.Atoi(e)
			if ei == wantedEpisode && (wantedSeason == 0 || si == wantedSeason) {
				selected, matched = candidate, true
				break
			}
		}
		if !matched && len(candidates) > 1 {
			return "", errors.New("archive does not contain the requested episode")
		}
	}
	directory, err := os.MkdirTemp("", "fsb-cinerate-subtitle-")
	if err != nil {
		return "", err
	}
	reader, err := selected.Open()
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	defer reader.Close()
	name := regexp.MustCompile(`[^\pL\pN._ -]+`).ReplaceAllString(filepath.Base(selected.Name), "_")
	if name == "" || name == "." {
		name = "subtitle.srt"
	}
	path := filepath.Join(directory, name)
	output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	written, copyErr := io.Copy(output, io.LimitReader(reader, 20*1024*1024+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || written > 20*1024*1024 {
		_ = os.RemoveAll(directory)
		return "", errors.New("subtitle extraction failed")
	}
	return path, nil
}

func (m *Manager) uploadSubtitle(ctx context.Context, path string) (int, error) {
	inputFile, err := uploader.NewUploader(m.client.API()).FromPath(ctx, path)
	if err != nil {
		return 0, err
	}
	channel, err := utils.GetLogChannelPeer(ctx, m.client.API(), m.client.PeerStorage)
	if err != nil {
		return 0, err
	}
	updates, err := m.client.API().MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Silent:  true,
		Peer:    &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		Media:   &tg.InputMediaUploadedDocument{ForceFile: true, File: inputFile, MimeType: subtitleMIMEType(path), Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: filepath.Base(path)}}},
		Message: "CineRate subtitle", RandomID: rand.Int63(),
	})
	if err != nil {
		return 0, err
	}
	return sentChannelMessageID(updates)
}

func subtitleMIMEType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt":
		return "application/x-subrip"
	case ".vtt":
		return "text/vtt"
	case ".ass", ".ssa":
		return "text/x-ssa"
	default:
		return "text/plain"
	}
}

func sentChannelMessageID(updates tg.UpdatesClass) (int, error) {
	var items []tg.UpdateClass
	switch value := updates.(type) {
	case *tg.Updates:
		items = value.Updates
	case *tg.UpdatesCombined:
		items = value.Updates
	}
	for _, item := range items {
		if update, ok := item.(*tg.UpdateNewChannelMessage); ok {
			if message, ok := update.Message.(*tg.Message); ok {
				return message.ID, nil
			}
		}
	}
	return 0, errors.New("Telegram did not return the subtitle message")
}
