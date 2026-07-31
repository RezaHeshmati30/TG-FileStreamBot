package commands

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"EverythingSuckz/fsb/config"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

const (
	callbackShowID      = "access:show-id"
	menuManageUsers     = "👥 Manage users"
	menuAllowAccess     = "✅ Allow access"
	menuDenyAccess      = "❌ Revoke access"
	menuAuthorizedUsers = "📋 Authorized users"
	menuMyProfile       = "🆔 My profile"
	menuBack            = "⬅️ Back"
)

var adminInput = struct {
	sync.Mutex
	mode string
}{}

func unauthorizedMarkup() *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{
		Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButtonCallback{
			Text: "🆔 Show my Telegram ID",
			Data: []byte(callbackShowID),
		}},
	}}}
}

func sendUnauthorizedNotice(ctx *ext.Context, u *ext.Update) {
	ctx.Reply(
		u,
		ext.ReplyTextString("🔒 You are not authorized to use this bot yet.\n\nUse the button below to display your Telegram ID, then send it to the administrator."),
		&ext.ReplyOpts{Markup: unauthorizedMarkup()},
	)
}

func adminMainKeyboard() *tg.ReplyKeyboardMarkup {
	return &tg.ReplyKeyboardMarkup{
		Resize:     true,
		Persistent: true,
		Placeholder: "Choose an admin action",
		Rows: []tg.KeyboardButtonRow{
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: menuManageUsers}}},
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: menuAuthorizedUsers}, &tg.KeyboardButton{Text: menuMyProfile}}},
		},
	}
}

func adminUsersKeyboard() *tg.ReplyKeyboardMarkup {
	return &tg.ReplyKeyboardMarkup{
		Resize:     true,
		Persistent: true,
		Placeholder: "Manage bot access",
		Rows: []tg.KeyboardButtonRow{
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: menuAllowAccess}, &tg.KeyboardButton{Text: menuDenyAccess}}},
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: menuAuthorizedUsers}}},
			{Buttons: []tg.KeyboardButtonClass{&tg.KeyboardButton{Text: menuBack}}},
		},
	}
}

func sendUserCard(ctx *ext.Context, u *ext.Update) error {
	user := u.EffectiveUser()
	if user == nil {
		return dispatcher.EndGroups
	}

	username := "Not set"
	if user.Username != "" {
		username = "@" + user.Username
	}
	displayName := strings.TrimSpace(strings.Join([]string{user.FirstName, user.LastName}, " "))
	if displayName == "" {
		displayName = "Not set"
	}
	language := strings.ToUpper(user.LangCode)
	if language == "" {
		language = "Unknown"
	}
	accessStatus := "Not authorized"
	if isAuthorized(user.ID) {
		accessStatus = "Authorized"
	}
	id := strconv.FormatInt(user.ID, 10)

	text := []styling.StyledTextOption{
		styling.Plain("👤 "), styling.Bold("Your Telegram Profile"),
		styling.Plain("\n\n🧑 "), styling.Bold("Username"), styling.Plain("\n" + username),
		styling.Plain("\n\n🆔 "), styling.Bold("Telegram ID"), styling.Plain("\n"), styling.Code(id),
		styling.Plain("\n\n👤 "), styling.Bold("Display Name"), styling.Plain("\n" + displayName),
		styling.Plain("\n\n🌍 "), styling.Bold("Language"), styling.Plain("\n" + language),
		styling.Plain("\n\n🔐 "), styling.Bold("Bot Access"), styling.Plain("\n" + accessStatus),
	}
	shareText := fmt.Sprintf("Telegram ID: %s\nUsername: %s\nDisplay name: %s", id, username, displayName)
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{
		Buttons: []tg.KeyboardButtonClass{
			&tg.KeyboardButtonCopy{Text: "📋 Copy ID", CopyText: id},
			&tg.KeyboardButtonURL{Text: "📤 Share ID", URL: "https://t.me/share/url?url=&text=" + url.QueryEscape(shareText)},
		},
	}}}
	peer := ctx.PeerStorage.GetInputPeerById(u.EffectiveChat().GetID())
	if peer.Zero() {
		return dispatcher.EndGroups
	}
	_, err := ctx.Sender.To(peer).Markup(markup).StyledText(ctx, text...)
	if err != nil {
		return err
	}
	return dispatcher.EndGroups
}

func handleAccessCallback(ctx *ext.Context, u *ext.Update) error {
	if u.CallbackQuery == nil || string(u.CallbackQuery.Data) != callbackShowID {
		return nil
	}
	if !isPrivateChat(ctx, u) {
		return dispatcher.EndGroups
	}
	_, _ = ctx.Raw.MessagesSetBotCallbackAnswer(ctx, &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID:   u.CallbackQuery.QueryID,
		Message:   "Your profile is shown below.",
		CacheTime: 0,
	})
	return sendUserCard(ctx, u)
}

func handleAccessMenu(ctx *ext.Context, u *ext.Update) error {
	if u.EffectiveMessage == nil || !isPrivateChat(ctx, u) {
		return nil
	}
	actor := u.EffectiveUser()
	if actor == nil || actor.ID != config.ValueOf.OwnerID {
		return nil
	}
	input := strings.TrimSpace(u.EffectiveMessage.Text)

	switch input {
	case menuManageUsers:
		ctx.Reply(u, ext.ReplyTextString("👥 User management\n\nChoose an action below."), &ext.ReplyOpts{Markup: adminUsersKeyboard()})
		return dispatcher.EndGroups
	case menuAllowAccess:
		setAdminInputMode("allow")
		ctx.Reply(u, ext.ReplyTextString("Send the numeric Telegram ID you want to authorize."), nil)
		return dispatcher.EndGroups
	case menuDenyAccess:
		setAdminInputMode("deny")
		ctx.Reply(u, ext.ReplyTextString("Send the numeric Telegram ID whose access you want to revoke."), nil)
		return dispatcher.EndGroups
	case menuAuthorizedUsers:
		return listUsers(ctx, u)
	case menuMyProfile:
		return sendUserCard(ctx, u)
	case menuBack:
		setAdminInputMode("")
		ctx.Reply(u, ext.ReplyTextString("Admin menu"), &ext.ReplyOpts{Markup: adminMainKeyboard()})
		return dispatcher.EndGroups
	}

	mode := adminInputMode()
	if mode == "" || strings.HasPrefix(input, "/") {
		return nil
	}
	userID, err := strconv.ParseInt(input, 10, 64)
	if err != nil || userID <= 0 {
		ctx.Reply(u, ext.ReplyTextString("Please send a valid numeric Telegram ID, or tap Back to cancel."), &ext.ReplyOpts{Markup: adminUsersKeyboard()})
		return dispatcher.EndGroups
	}
	setAdminInputMode("")
	return applyAccessChange(ctx, u, actor, userID, mode == "allow")
}

func setAdminInputMode(mode string) {
	adminInput.Lock()
	adminInput.mode = mode
	adminInput.Unlock()
}

func adminInputMode() string {
	adminInput.Lock()
	defer adminInput.Unlock()
	return adminInput.mode
}
