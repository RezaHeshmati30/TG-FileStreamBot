package commands

import (
	"crypto/hmac"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/linkstate"
	"EverythingSuckz/fsb/internal/mediametadata"
	filetypes "EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

const (
	validityPrefix  = "v:"
	maxLinkValidity = 7 * 24 * time.Hour
)

func (m *command) LoadValidity(dispatcher dispatcher.Dispatcher) {
	dispatcher.AddHandler(handlers.NewCallbackQuery(nil, handleValidityCallback))
	m.log.Named("validity").Info("Loaded")
}

func buildStreamMessageText(file *filetypes.File, link, icon, ready, label string, expires int64) []styling.StyledTextOption {
	return []styling.StyledTextOption{
		styling.Plain(icon + " " + ready + "\n\n🔗 "), styling.Bold(label), styling.Plain(" (Tap to copy)\n"), styling.Code(link),
		styling.Plain("\n\n" + icon + " "), styling.Bold("File Name"), styling.Plain("\n" + file.FileName),
		styling.Plain("\n\n📦 "), styling.Bold("File Size"), styling.Plain("\n" + formatFileSize(file.FileSize)),
		styling.Plain("\n\n⏳ "), styling.Bold("Expires"), styling.Plain("\n" + time.Unix(expires, 0).In(displayLocation()).Format("02 Jan 2006, 15:04 MST")),
	}
}

func buildStreamMainMarkup(file *filetypes.File, link, icon string, messageID int, expires int64) *tg.ReplyInlineMarkup {
	row := tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonURL{Text: "Download", URL: link + "&d=true"}}}
	if icon == "🎬" || strings.Contains(file.MimeType, "audio") || strings.Contains(file.MimeType, "pdf") {
		row.Buttons = append(row.Buttons, &tg.KeyboardButtonURL{Text: "Stream", URL: link})
	}
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{row}}
	if icon != "🎬" {
		return markup
	}
	markup.Rows = append(markup.Rows,
		tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonURL{Text: "📺 WVC", URL: externalPlayerURL("wvc", messageID, expires)},
			&tg.KeyboardButtonURL{Text: "▶️ VLC", URL: externalPlayerURL("vlc", messageID, expires)},
		}},
		tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCallback{Text: "📱 QR code", Data: handoffCallbackData(messageID, expires)},
			&tg.KeyboardButtonCallback{Text: "⏱ Validity", Data: validityCallback("m", messageID, expires)},
		}},
	)
	var subtitles []tg.KeyboardButtonClass
	if subtitlesAvailable() {
		subtitles = append(subtitles, &tg.KeyboardButtonCallback{Text: "💬 Embedded", Data: subtitleCallbackData("p", messageID, expires, -1)})
	}
	if onlineSubtitlesAvailable() {
		subtitles = append(subtitles, &tg.KeyboardButtonCallback{Text: "🔎 Subtitle", Data: subtitleCallbackData("o", messageID, expires, -1)})
	}
	if len(subtitles) > 0 {
		markup.Rows = append(markup.Rows, tg.KeyboardButtonRow{Buttons: subtitles})
	}
	if seriesProgressAvailable() && parseMediaFileName(file.FileName).Type == "series" {
		markup.Rows = append(markup.Rows, tg.KeyboardButtonRow{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "✅ Mark episode watched", Data: seriesProgressStartCallback(messageID, expires)}}})
	}
	return markup
}

func validityCallback(action string, messageID int, expires int64) []byte {
	signature := utils.SignSubtitleAction("validity-"+action, messageID, expires, 0)
	return []byte(fmt.Sprintf("%s%s:%d:%d:%s", validityPrefix, action, messageID, expires, signature))
}

func parseValidityCallback(data []byte) (string, int, int64, bool) {
	parts := strings.Split(string(data), ":")
	if len(parts) != 5 || parts[0] != "v" {
		return "", 0, 0, false
	}
	messageID, err1 := strconv.Atoi(parts[2])
	expires, err2 := strconv.ParseInt(parts[3], 10, 64)
	expected := utils.SignSubtitleAction("validity-"+parts[1], messageID, expires, 0)
	return parts[1], messageID, expires, err1 == nil && err2 == nil && messageID > 0 && expires > 0 && hmac.Equal([]byte(parts[4]), []byte(expected))
}

func validityMenu(messageID int, expires int64) *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "⛔ Expire now", Data: validityCallback("x", messageID, expires)}}},
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCallback{Text: "6 hours", Data: validityCallback("6", messageID, expires)},
			&tg.KeyboardButtonCallback{Text: "24 hours", Data: validityCallback("24", messageID, expires)},
			&tg.KeyboardButtonCallback{Text: "7 days", Data: validityCallback("7d", messageID, expires)},
		}},
		{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{Text: "⬅️ Back", Data: validityCallback("b", messageID, expires)}}},
	}}
}

func handleValidityCallback(ctx *ext.Context, update *ext.Update) error {
	if update.CallbackQuery == nil || !strings.HasPrefix(string(update.CallbackQuery.Data), validityPrefix) {
		return nil
	}
	if !isPrivateChat(ctx, update) || update.EffectiveUser() == nil || !isAuthorized(update.EffectiveUser().ID) {
		answerSubtitleCallback(ctx, update, "You are not authorized to manage this link.", true)
		return dispatcher.EndGroups
	}
	action, messageID, oldExpires, ok := parseValidityCallback(update.CallbackQuery.Data)
	if !ok {
		answerSubtitleCallback(ctx, update, "This validity action is invalid.", true)
		return dispatcher.EndGroups
	}
	if action == "m" {
		answerSubtitleCallback(ctx, update, "Choose the new validity.", false)
		return editReplyMarkup(ctx, update, validityMenu(messageID, oldExpires))
	}
	file, err := utils.FileFromMessageRaw(ctx, ctx.Raw, ctx.PeerStorage, messageID)
	if err != nil {
		answerSubtitleCallback(ctx, update, "The stored file is unavailable.", true)
		return dispatcher.EndGroups
	}
	if action == "b" {
		link := signedStreamLink(file, messageID, oldExpires)
		icon, _, _ := fileLinkPresentation(file.FileName, file.MimeType)
		answerSubtitleCallback(ctx, update, "Back to link actions.", false)
		return editReplyMarkup(ctx, update, buildStreamMainMarkup(file, link, icon, messageID, oldExpires))
	}
	now := time.Now()
	newExpires, state := int64(0), int64(0)
	if action == "x" {
		newExpires = now.Add(-time.Second).Unix()
		state = -oldExpires
	} else {
		var ok bool
		newExpires, ok = replacementLinkExpiry(now, action)
		if !ok {
			answerSubtitleCallback(ctx, update, "This validity option is invalid.", true)
			return dispatcher.EndGroups
		}
		state = newExpires
	}
	if err := persistLinkState(ctx, messageID, state, update.EffectiveUser().ID); err != nil {
		answerSubtitleCallback(ctx, update, "The validity could not be changed.", true)
		return dispatcher.EndGroups
	}
	if err := refreshLinkMessage(ctx, update, file, messageID, newExpires, oldExpires, action == "x"); err != nil {
		_ = persistLinkState(ctx, messageID, oldExpires, update.EffectiveUser().ID)
		answerSubtitleCallback(ctx, update, "The message could not be updated.", true)
		return dispatcher.EndGroups
	}
	message := "Link expired."
	if action != "x" {
		message = "Link validity updated."
	}
	answerSubtitleCallback(ctx, update, message, false)
	return dispatcher.EndGroups
}

func replacementLinkExpiry(now time.Time, action string) (int64, bool) {
	duration := map[string]time.Duration{
		"6":  6 * time.Hour,
		"24": 24 * time.Hour,
		"7d": 7 * 24 * time.Hour,
	}[action]
	if duration <= 0 || duration > maxLinkValidity {
		return 0, false
	}
	return now.Add(duration).Unix(), true
}

func persistLinkState(ctx *ext.Context, messageID int, state int64, actorID int64) error {
	botAccess.mu.Lock()
	defer botAccess.mu.Unlock()
	links := linkstate.Snapshot()
	links[messageID] = state
	if err := persistAccessState(ctx, botAccess.messageID, botAccess.users, botAccess.usernames, botAccess.languages, links, actorID); err != nil {
		return err
	}
	linkstate.Replace(links)
	return nil
}

func signedStreamLink(file *filetypes.File, messageID int, expires int64) string {
	signature := utils.SignFile(file.FileName, file.FileSize, file.MimeType, file.ID, expires)
	return fmt.Sprintf("%s/stream/%d?%s", strings.TrimRight(config.ValueOf.Host, "/"), messageID, url.Values{"signature": {signature}, "expires": {strconv.FormatInt(expires, 10)}}.Encode())
}

func refreshLinkMessage(ctx *ext.Context, update *ext.Update, file *filetypes.File, messageID int, expires int64, callbackExpires int64, expired bool) error {
	link := signedStreamLink(file, messageID, expires)
	icon, ready, label := fileLinkPresentation(file.FileName, file.MimeType)
	text := buildStreamMessageText(file, link, icon, ready, label, expires)
	if callbackMessageHasPoster(ctx, update) {
		text = append(mediaHeading(mediametadata.Resolve(ctx, file)), text...)
	}
	markup := buildStreamMainMarkup(file, link, icon, messageID, expires)
	if expired {
		markup = validityMenu(messageID, callbackExpires)
	}
	builder := entity.Builder{}
	if err := styling.Perform(&builder, text...); err != nil {
		return err
	}
	message, entities := builder.Complete()
	peer := ctx.PeerStorage.GetInputPeerById(update.CallbackQuery.UserID)
	_, err := ctx.Raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: peer, ID: update.CallbackQuery.MsgID, Message: message, Entities: entities, ReplyMarkup: markup})
	return err
}

func callbackMessageHasPoster(ctx *ext.Context, update *ext.Update) bool {
	if update == nil || update.CallbackQuery == nil {
		return false
	}
	result, err := ctx.Raw.MessagesGetMessages(ctx, []tg.InputMessageClass{
		&tg.InputMessageID{ID: update.CallbackQuery.MsgID},
	})
	if err != nil {
		utils.Logger.Sugar().Warnw("Could not inspect link message media", "message_id", update.CallbackQuery.MsgID, "error", err)
		return false
	}
	messages, err := messagesFromResult(result)
	if err != nil || len(messages) != 1 {
		return false
	}
	message, ok := messages[0].(*tg.Message)
	if !ok {
		return false
	}
	_, hasPoster := message.Media.(*tg.MessageMediaPhoto)
	return hasPoster
}

func editReplyMarkup(ctx *ext.Context, update *ext.Update, markup tg.ReplyMarkupClass) error {
	peer := ctx.PeerStorage.GetInputPeerById(update.CallbackQuery.UserID)
	_, err := ctx.Raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: peer, ID: update.CallbackQuery.MsgID, ReplyMarkup: markup})
	return err
}
