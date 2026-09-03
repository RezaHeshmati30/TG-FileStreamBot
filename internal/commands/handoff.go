package commands

import (
	"crypto/hmac"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

const (
	handoffCallbackPrefix = "qr:"
	handoffLifetime       = 10 * time.Minute
)

func (m *command) LoadHandoff(dispatcher dispatcher.Dispatcher) {
	dispatcher.AddHandler(handlers.NewCallbackQuery(nil, handleHandoffCallback))
	m.log.Named("handoff").Info("Loaded")
}

func handoffCallbackData(messageID int, expires int64) []byte {
	signature := utils.SignHandoffAction(messageID, expires)
	return []byte(fmt.Sprintf("%s%d:%d:%s", handoffCallbackPrefix, messageID, expires, signature))
}

func parseHandoffCallback(data []byte) (int, int64, bool) {
	parts := strings.Split(string(data), ":")
	if len(parts) != 4 || parts[0] != strings.TrimSuffix(handoffCallbackPrefix, ":") {
		return 0, 0, false
	}
	messageID, messageErr := strconv.Atoi(parts[1])
	expires, expiresErr := strconv.ParseInt(parts[2], 10, 64)
	if messageErr != nil || expiresErr != nil || messageID <= 0 || expires <= 0 {
		return 0, 0, false
	}
	expected := utils.SignHandoffAction(messageID, expires)
	return messageID, expires, hmac.Equal([]byte(parts[3]), []byte(expected))
}

func handleHandoffCallback(ctx *ext.Context, update *ext.Update) error {
	if update.CallbackQuery == nil || !strings.HasPrefix(string(update.CallbackQuery.Data), handoffCallbackPrefix) {
		return nil
	}
	if !isPrivateChat(ctx, update) || update.EffectiveUser() == nil || !isAuthorized(update.EffectiveUser().ID) {
		answerSubtitleCallback(ctx, update, "You are not authorized to use this function.", true)
		return dispatcher.EndGroups
	}
	messageID, originalExpires, ok := parseHandoffCallback(update.CallbackQuery.Data)
	if !ok {
		answerSubtitleCallback(ctx, update, "This QR handoff action is invalid.", true)
		return dispatcher.EndGroups
	}
	now := time.Now()
	if now.Unix() > originalExpires {
		answerSubtitleCallback(ctx, update, "This file link has expired.", true)
		return dispatcher.EndGroups
	}

	handoffExpires := now.Add(handoffLifetime).Unix()
	if handoffExpires > originalExpires {
		handoffExpires = originalExpires
	}
	handoffURL := buildHandoffURL(messageID, handoffExpires)
	expiresDisplay := time.Unix(handoffExpires, 0).In(displayLocation()).Format("15:04 MST")
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{
		&tg.KeyboardButtonURL{Text: "📱 Open QR handoff", URL: handoffURL},
	}}}}
	answerSubtitleCallback(ctx, update, "QR handoff created.", false)
	sendSubtitleText(ctx, update, fmt.Sprintf("📱 Cross-device handoff\n\nOpen the page below and scan its QR code with the other device. The handoff expires at %s.", expiresDisplay), markup)
	return dispatcher.EndGroups
}

func buildHandoffURL(messageID int, expires int64) string {
	query := url.Values{
		"expires":   {strconv.FormatInt(expires, 10)},
		"signature": {utils.SignHandoffPage(messageID, expires)},
	}
	return fmt.Sprintf("%s/handoff/%d?%s", strings.TrimRight(config.ValueOf.Host, "/"), messageID, query.Encode())
}
