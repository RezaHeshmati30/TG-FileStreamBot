package commands

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	filetypes "EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const subtitleCallbackPrefix = "sub:"

var (
	subtitleSlots          chan struct{}
	subtitleToolsAvailable bool
	subtitleCache          = struct {
		sync.RWMutex
		items map[string]subtitleResult
	}{items: make(map[string]subtitleResult)}
)

type subtitleResult struct {
	messageID      int
	file           *filetypes.File
	sourceFileName string
}

type subtitleProbe struct {
	Streams []subtitleTrack `json:"streams"`
}

type subtitleTrack struct {
	Index     int               `json:"index"`
	CodecName string            `json:"codec_name"`
	Tags      map[string]string `json:"tags"`
}

func (m *command) LoadSubtitles(dispatcher dispatcher.Dispatcher) {
	subtitleSlots = make(chan struct{}, config.ValueOf.SubtitleConcurrency)
	_, ffprobeErr := exec.LookPath("ffprobe")
	_, ffmpegErr := exec.LookPath("ffmpeg")
	subtitleToolsAvailable = ffprobeErr == nil && ffmpegErr == nil
	dispatcher.AddHandler(handlers.NewCallbackQuery(nil, handleSubtitleCallback))
	dispatcher.AddHandler(handlers.NewCallbackQuery(nil, handleOnlineSubtitleCallback))
	logger := m.log.Named("subtitles")
	if !subtitleToolsAvailable {
		logger.Error("FFmpeg tools are unavailable; embedded subtitle extraction will be disabled", zap.Error(errors.Join(ffprobeErr, ffmpegErr)))
		return
	}
	logger.Info("Loaded", zap.Int("concurrency", config.ValueOf.SubtitleConcurrency))
}

func subtitlesAvailable() bool {
	return subtitleToolsAvailable
}

func subtitleCallbackData(action string, messageID int, expires int64, trackIndex int) []byte {
	signature := utils.SignSubtitleAction(action, messageID, expires, trackIndex)
	return []byte(fmt.Sprintf("%s%s:%d:%d:%d:%s", subtitleCallbackPrefix, action, messageID, expires, trackIndex, signature))
}

func parseSubtitleCallback(data []byte) (string, int, int64, int, bool) {
	parts := strings.Split(string(data), ":")
	if len(parts) != 6 || parts[0] != strings.TrimSuffix(subtitleCallbackPrefix, ":") {
		return "", 0, 0, 0, false
	}
	messageID, err1 := strconv.Atoi(parts[2])
	expires, err2 := strconv.ParseInt(parts[3], 10, 64)
	trackIndex, err3 := strconv.Atoi(parts[4])
	if err1 != nil || err2 != nil || err3 != nil || messageID <= 0 || expires <= 0 {
		return "", 0, 0, 0, false
	}
	expected := utils.SignSubtitleAction(parts[1], messageID, expires, trackIndex)
	if !hmac.Equal([]byte(parts[5]), []byte(expected)) {
		return "", 0, 0, 0, false
	}
	return parts[1], messageID, expires, trackIndex, true
}

func handleSubtitleCallback(ctx *ext.Context, u *ext.Update) error {
	if u.CallbackQuery == nil || !strings.HasPrefix(string(u.CallbackQuery.Data), subtitleCallbackPrefix) {
		return nil
	}
	if !isPrivateChat(ctx, u) || u.EffectiveUser() == nil || !isAuthorized(u.EffectiveUser().ID) {
		answerSubtitleCallback(ctx, u, "You are not authorized to use this function.", true)
		return dispatcher.EndGroups
	}
	action, messageID, expires, trackIndex, ok := parseSubtitleCallback(u.CallbackQuery.Data)
	if !ok {
		answerSubtitleCallback(ctx, u, "This subtitle action is invalid.", true)
		return dispatcher.EndGroups
	}
	if time.Now().Unix() > expires {
		answerSubtitleCallback(ctx, u, "This file link has expired.", true)
		return dispatcher.EndGroups
	}
	if !subtitleToolsAvailable {
		if action != "d" && action != "o" {
			answerSubtitleCallback(ctx, u, "Subtitle tools are not installed on the server.", true)
			return dispatcher.EndGroups
		}
	}

	switch action {
	case "p":
		answerSubtitleCallback(ctx, u, "Checking subtitle tracks…", false)
		return showSubtitleTracks(ctx, u, messageID, expires)
	case "x":
		answerSubtitleCallback(ctx, u, "Preparing the selected subtitle…", false)
		return prepareSubtitle(ctx, u, messageID, expires, trackIndex)
	case "d":
		answerSubtitleCallback(ctx, u, "Sending the SRT file…", false)
		return sendSubtitleDocument(ctx, u, messageID)
	case "o":
		answerSubtitleCallback(ctx, u, "Searching Subsource…", false)
		return startOnlineSubtitleSearch(ctx, u, messageID, expires)
	default:
		answerSubtitleCallback(ctx, u, "This subtitle action is invalid.", true)
		return dispatcher.EndGroups
	}
}

func answerSubtitleCallback(ctx *ext.Context, u *ext.Update, message string, alert bool) {
	_, _ = ctx.Raw.MessagesSetBotCallbackAnswer(ctx, &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID:   u.CallbackQuery.QueryID,
		Alert:     alert,
		Message:   message,
		CacheTime: 0,
	})
}

func showSubtitleTracks(ctx *ext.Context, u *ext.Update, messageID int, expires int64) error {
	file, err := subtitleSourceFile(ctx, messageID)
	if err != nil {
		return subtitleFailure(ctx, u, "The video is no longer available.", err)
	}
	if !strings.Contains(file.MimeType, "video") && !strings.HasSuffix(strings.ToLower(file.FileName), ".mkv") {
		sendSubtitleText(ctx, u, "💬 Subtitle inspection is only available for video files.", nil)
		return dispatcher.EndGroups
	}

	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(config.ValueOf.SubtitleProbeTimeoutSec)*time.Second)
	defer cancel()
	tracks, err := probeSubtitleTracks(probeCtx, internalStreamURL(file, messageID, expires))
	if err != nil {
		return subtitleFailure(ctx, u, "The subtitle tracks could not be inspected.", err)
	}
	if len(tracks) == 0 {
		sendSubtitleText(ctx, u, "💬 No embedded subtitle tracks were found in this video.", nil)
		return dispatcher.EndGroups
	}
	if len(tracks) > 20 {
		tracks = tracks[:20]
	}

	rows := make([]tg.KeyboardButtonRow, 0, len(tracks))
	for _, track := range tracks {
		label := subtitleTrackLabel(track)
		if !supportedSubtitleCodec(track.CodecName) {
			label += " · unsupported"
		}
		if len(label) > 60 {
			label = label[:57] + "…"
		}
		rows = append(rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCallback{
				Text: label,
				Data: subtitleCallbackData("x", messageID, expires, track.Index),
			},
		}})
	}
	sendSubtitleText(ctx, u, "💬 Embedded subtitles\n\nChoose the text subtitle track you want to prepare as an SRT file.", &tg.ReplyInlineMarkup{Rows: rows})
	return dispatcher.EndGroups
}

func prepareSubtitle(ctx *ext.Context, u *ext.Update, messageID int, expires int64, trackIndex int) error {
	cacheKey := fmt.Sprintf("%d:%d", messageID, trackIndex)
	subtitleCache.RLock()
	cached, found := subtitleCache.items[cacheKey]
	subtitleCache.RUnlock()
	if found {
		return sendSubtitleResult(ctx, u, cached, expires)
	}

	select {
	case subtitleSlots <- struct{}{}:
		defer func() { <-subtitleSlots }()
	default:
		sendSubtitleText(ctx, u, "⏳ Another subtitle is currently being prepared. Please try again shortly.", nil)
		return dispatcher.EndGroups
	}

	file, err := subtitleSourceFile(ctx, messageID)
	if err != nil {
		return subtitleFailure(ctx, u, "The video is no longer available.", err)
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, time.Duration(config.ValueOf.SubtitleProbeTimeoutSec)*time.Second)
	tracks, err := probeSubtitleTracks(probeCtx, internalStreamURL(file, messageID, expires))
	probeCancel()
	if err != nil {
		return subtitleFailure(ctx, u, "The selected subtitle could not be verified.", err)
	}
	track, found := findSubtitleTrack(tracks, trackIndex)
	if !found {
		sendSubtitleText(ctx, u, "💬 The selected subtitle track was not found.", nil)
		return dispatcher.EndGroups
	}
	if !supportedSubtitleCodec(track.CodecName) {
		sendSubtitleText(ctx, u, "💬 This is an image-based or unsupported subtitle track and cannot be converted to SRT.", nil)
		return dispatcher.EndGroups
	}

	sendSubtitleText(ctx, u, "⏳ Preparing the selected subtitle. Large videos may take several minutes because the file must be read up to the end.", nil)
	extractCtx, cancel := context.WithTimeout(ctx, time.Duration(config.ValueOf.SubtitleExtractTimeoutSec)*time.Second)
	defer cancel()
	path, err := extractSubtitle(extractCtx, internalStreamURL(file, messageID, expires), trackIndex, subtitleOutputName(file.FileName, track))
	if err != nil {
		if errors.Is(extractCtx.Err(), context.DeadlineExceeded) {
			sendSubtitleText(ctx, u, "⌛ Subtitle extraction exceeded the configured time limit.", nil)
			return dispatcher.EndGroups
		}
		return subtitleFailure(ctx, u, "The subtitle could not be extracted.", err)
	}
	defer os.RemoveAll(filepath.Dir(path))

	result, err := uploadSubtitle(ctx, path)
	if err != nil {
		return subtitleFailure(ctx, u, "The extracted subtitle could not be saved.", err)
	}
	result.sourceFileName = file.FileName
	subtitleCache.Lock()
	subtitleCache.items[cacheKey] = result
	subtitleCache.Unlock()
	return sendSubtitleResult(ctx, u, result, expires)
}

func subtitleSourceFile(ctx *ext.Context, messageID int) (*filetypes.File, error) {
	channel, err := utils.GetLogChannelPeer(ctx, ctx.Raw, ctx.PeerStorage)
	if err != nil {
		return nil, err
	}
	result, err := ctx.Raw.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
	})
	if err != nil {
		return nil, err
	}
	messages, err := messagesFromResult(result)
	if err != nil || len(messages) != 1 {
		return nil, fmt.Errorf("source message %d was not found", messageID)
	}
	message, ok := messages[0].(*tg.Message)
	if !ok {
		return nil, fmt.Errorf("source message has unexpected type %T", messages[0])
	}
	return utils.FileFromMedia(message.Media)
}

func internalStreamURL(file *filetypes.File, messageID int, expires int64) string {
	signature := utils.SignFile(file.FileName, file.FileSize, file.MimeType, file.ID, expires)
	query := url.Values{"signature": []string{signature}, "expires": []string{strconv.FormatInt(expires, 10)}}
	return fmt.Sprintf("http://127.0.0.1:%d/stream/%d?%s", config.ValueOf.Port, messageID, query.Encode())
}

func probeSubtitleTracks(ctx context.Context, sourceURL string) ([]subtitleTrack, error) {
	command := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "s", "-show_entries", "stream=index,codec_name:stream_tags=language,title", "-of", "json", sourceURL)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}
	var result subtitleProbe
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("decode ffprobe response: %w", err)
	}
	return result.Streams, nil
}

func supportedSubtitleCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text":
		return true
	default:
		return false
	}
}

func findSubtitleTrack(tracks []subtitleTrack, index int) (subtitleTrack, bool) {
	for _, track := range tracks {
		if track.Index == index {
			return track, true
		}
	}
	return subtitleTrack{}, false
}

func subtitleTrackLabel(track subtitleTrack) string {
	language := strings.ToUpper(strings.TrimSpace(track.Tags["language"]))
	if language == "" || language == "UND" {
		language = "Unknown language"
	}
	title := strings.TrimSpace(track.Tags["title"])
	label := fmt.Sprintf("💬 %s", language)
	if title != "" && !strings.EqualFold(title, language) {
		label += " · " + title
	}
	return label
}

func subtitleOutputName(videoName string, track subtitleTrack) string {
	base := strings.TrimSuffix(filepath.Base(videoName), filepath.Ext(videoName))
	baseRunes := []rune(base)
	if len(baseRunes) > 120 {
		base = string(baseRunes[:120])
	}
	language := strings.ToLower(strings.TrimSpace(track.Tags["language"]))
	if language == "" {
		language = fmt.Sprintf("track-%d", track.Index)
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "..", "-")
	return replacer.Replace(base + "." + language + ".srt")
}

func extractSubtitle(ctx context.Context, sourceURL string, trackIndex int, outputName string) (string, error) {
	directory, err := os.MkdirTemp("", "fsb-subtitle-")
	if err != nil {
		return "", err
	}
	outputPath := filepath.Join(directory, outputName)
	maxBytes := int64(config.ValueOf.SubtitleMaxOutputMB) * 1024 * 1024
	command := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error", "-i", sourceURL, "-map", fmt.Sprintf("0:%d", trackIndex), "-vn", "-an", "-c:s", "srt", "-f", "srt", "-fs", strconv.FormatInt(maxBytes, 10), "-y", outputPath)
	output, err := command.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", fmt.Errorf("ffmpeg failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	if info.Size() == 0 || info.Size() >= maxBytes {
		_ = os.RemoveAll(directory)
		return "", fmt.Errorf("subtitle output is empty or exceeds %d MB", config.ValueOf.SubtitleMaxOutputMB)
	}
	return outputPath, nil
}

func uploadSubtitle(ctx *ext.Context, path string) (subtitleResult, error) {
	inputFile, err := uploader.NewUploader(ctx.Raw).FromPath(ctx, path)
	if err != nil {
		return subtitleResult{}, err
	}
	channel, err := utils.GetLogChannelPeer(ctx, ctx.Raw, ctx.PeerStorage)
	if err != nil {
		return subtitleResult{}, err
	}
	updates, err := ctx.Raw.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
		Silent: true,
		Peer:   &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		Media: &tg.InputMediaUploadedDocument{
			ForceFile: true,
			File:      inputFile,
			MimeType:  "application/x-subrip",
			Attributes: []tg.DocumentAttributeClass{
				&tg.DocumentAttributeFilename{FileName: filepath.Base(path)},
			},
		},
		Message:  "Extracted subtitle",
		RandomID: rand.Int63(),
	})
	if err != nil {
		return subtitleResult{}, err
	}
	message := sentMessageFromUpdates(updates)
	if message == nil {
		return subtitleResult{}, fmt.Errorf("Telegram did not return the uploaded subtitle message")
	}
	file, err := utils.FileFromMedia(message.Media)
	if err != nil {
		return subtitleResult{}, err
	}
	return subtitleResult{messageID: message.ID, file: file}, nil
}

func sentMessageFromUpdates(updates tg.UpdatesClass) *tg.Message {
	switch value := updates.(type) {
	case *tg.Updates:
		for _, update := range value.Updates {
			if newMessage, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if message, ok := newMessage.Message.(*tg.Message); ok {
					return message
				}
			}
		}
	case *tg.UpdatesCombined:
		for _, update := range value.Updates {
			if newMessage, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if message, ok := newMessage.Message.(*tg.Message); ok {
					return message
				}
			}
		}
	}
	return nil
}

func sendSubtitleResult(ctx *ext.Context, u *ext.Update, result subtitleResult, expires int64) error {
	signature := utils.SignFile(result.file.FileName, result.file.FileSize, result.file.MimeType, result.file.ID, expires)
	link := fmt.Sprintf("%s/stream/%d?signature=%s&expires=%d", config.ValueOf.Host, result.messageID, signature, expires)
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonURL{Text: "💬 Open subtitle", URL: link}}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{
			Text: "⬇️ Download Subtitle",
			Data: subtitleCallbackData("d", result.messageID, expires, -1),
		}}},
	}}
	text := []styling.StyledTextOption{
		styling.Plain("💬 Your subtitle link is ready!\n\n🔗 "),
		styling.Bold("Subtitle Link"),
		styling.Plain(" (Tap to copy)\n"),
		styling.Code(link),
		styling.Plain("\n\n🎬 "),
		styling.Bold("Source Video"),
		styling.Plain("\n"),
		styling.Plain(result.sourceFileName),
		styling.Plain("\n\n⏳ The link expires together with the original video link."),
	}
	sendSubtitleStyledText(ctx, u, text, markup)
	return dispatcher.EndGroups
}

func sendSubtitleDocument(ctx *ext.Context, u *ext.Update, messageID int) error {
	fromChannel, err := utils.GetLogChannelPeer(ctx, ctx.Raw, ctx.PeerStorage)
	if err != nil {
		return subtitleFailure(ctx, u, "The subtitle file could not be found.", err)
	}
	toPeer := subtitlePeer(ctx, u)
	if toPeer.Zero() {
		return subtitleFailure(ctx, u, "The subtitle file could not be sent.", errors.New("callback peer is unavailable"))
	}
	_, err = ctx.Raw.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		DropAuthor:        true,
		DropMediaCaptions: true,
		FromPeer:          &tg.InputPeerChannel{ChannelID: fromChannel.ChannelID, AccessHash: fromChannel.AccessHash},
		ID:                []int{messageID},
		RandomID:          []int64{rand.Int63()},
		ToPeer:            toPeer,
	})
	if err != nil {
		return subtitleFailure(ctx, u, "The subtitle file could not be sent.", err)
	}
	return dispatcher.EndGroups
}

func subtitleFailure(ctx *ext.Context, u *ext.Update, userMessage string, err error) error {
	utils.Logger.Error("Subtitle operation failed", zap.Error(err))
	sendSubtitleText(ctx, u, "❌ "+userMessage, nil)
	return dispatcher.EndGroups
}

func subtitlePeer(ctx *ext.Context, u *ext.Update) tg.InputPeerClass {
	if u.CallbackQuery == nil {
		return &tg.InputPeerEmpty{}
	}
	return ctx.PeerStorage.GetInputPeerById(u.CallbackQuery.UserID)
}

func sendSubtitleText(ctx *ext.Context, u *ext.Update, text string, markup tg.ReplyMarkupClass) {
	peer := subtitlePeer(ctx, u)
	if peer.Zero() {
		utils.Logger.Error("Could not resolve subtitle callback peer")
		return
	}
	builder := ctx.Sender.To(peer)
	var err error
	if markup != nil {
		_, err = builder.Markup(markup).Text(ctx, text)
	} else {
		_, err = builder.Text(ctx, text)
	}
	if err != nil {
		utils.Logger.Error("Could not send subtitle message", zap.Error(err))
	}
}

func sendSubtitleStyledText(ctx *ext.Context, u *ext.Update, text []styling.StyledTextOption, markup tg.ReplyMarkupClass) {
	peer := subtitlePeer(ctx, u)
	if peer.Zero() {
		utils.Logger.Error("Could not resolve subtitle callback peer")
		return
	}
	builder := ctx.Sender.To(peer)
	var err error
	if markup != nil {
		_, err = builder.NoWebpage().Markup(markup).StyledText(ctx, text...)
	} else {
		_, err = builder.NoWebpage().StyledText(ctx, text...)
	}
	if err != nil {
		utils.Logger.Error("Could not send styled subtitle message", zap.Error(err))
	}
}
