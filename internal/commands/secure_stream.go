package commands

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/secureproxy"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

var externalURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

func (m *command) LoadSecureStream(dispatcher dispatcher.Dispatcher) {
	dispatcher.AddHandler(handlers.NewMessage(nil, handleSecureStreamURL))
	m.log.Named("secure-stream").Info("Loaded")
}

func handleSecureStreamURL(ctx *ext.Context, update *ext.Update) error {
	message := update.EffectiveMessage
	if message == nil || message.Media != nil || strings.TrimSpace(message.Text) == "" {
		return nil
	}
	chatID := update.EffectiveChat().GetID()
	peer := ctx.PeerStorage.GetPeerById(chatID)
	if peer == nil || peer.Type != int(storage.TypeUser) {
		return nil
	}
	rawURL := firstExternalURL(message.Text)
	if rawURL == "" {
		return nil
	}
	if !isAuthorized(chatID) {
		sendUnauthorizedNotice(ctx, update)
		return dispatcher.EndGroups
	}
	rememberAuthorizedUsername(ctx, update.EffectiveUser())
	parsed, err := secureproxy.ParseAndValidateURL(rawURL)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("❌ This URL cannot be proxied: "+err.Error()), nil)
		return dispatcher.EndGroups
	}
	expires := time.Now().Add(secureproxy.TokenLifetime).Unix()
	token, err := secureproxy.EncryptTarget(parsed.String(), expires)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString("❌ The secure stream link could not be created."), nil)
		return dispatcher.EndGroups
	}
	proxyURL := secureProxyURL(token)
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonURL{Text: "▶️ Open Stream", URL: proxyURL},
			&tg.KeyboardButtonURL{Text: "⬇️ Download", URL: proxyURL + "?d=true"},
		}},
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonURL{Text: "📺 Open in WVC", URL: secureProxyPlayerURL("wvc", token)},
			&tg.KeyboardButtonURL{Text: "▶️ Open in VLC", URL: secureProxyPlayerURL("vlc", token)},
		}},
		{Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCopy{Text: "📋 Copy secure link", CopyText: proxyURL},
		}},
	}}
	expiresDisplay := time.Unix(expires, 0).In(displayLocation()).Format("02 Jan 2006, 15:04 MST")
	text := []styling.StyledTextOption{
		styling.Plain("🌐 Your secure stream link is ready!\n\n🔗 "),
		styling.Bold("Secure Link"),
		styling.Plain(" (Tap to copy)\n"),
		styling.Code(proxyURL),
		styling.Plain("\n\n🌍 "),
		styling.Bold("Source"),
		styling.Plain("\n" + secureproxy.DisplayName(parsed)),
		styling.Plain("\n\n⏳ "),
		styling.Bold("Expires"),
		styling.Plain("\n" + expiresDisplay + " (24 hours)"),
	}
	_, err = ctx.Reply(update, ext.ReplyTextStyledTextArray(text), &ext.ReplyOpts{
		Markup: markup, NoWebpage: true, ReplyToMessageId: message.ID,
	})
	if err != nil {
		return err
	}
	return dispatcher.EndGroups
}

func firstExternalURL(text string) string {
	match := externalURLPattern.FindString(text)
	return strings.TrimRight(match, ".,;!?)]}ـ،؛")
}

func secureProxyURL(token string) string {
	return fmt.Sprintf("%s/proxy/%s", strings.TrimRight(config.ValueOf.Host, "/"), url.PathEscape(token))
}

func secureProxyPlayerURL(player, token string) string {
	return fmt.Sprintf("%s/proxy-player/%s/%s", strings.TrimRight(config.ValueOf.Host, "/"), player, url.PathEscape(token))
}
