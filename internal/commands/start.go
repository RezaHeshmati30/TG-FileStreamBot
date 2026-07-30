package commands

import (
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
)

func (m *command) LoadStart(dispatcher dispatcher.Dispatcher) {
	log := m.log.Named("start")
	defer log.Sugar().Info("Loaded")
	dispatcher.AddHandler(handlers.NewCommand("start", start))
}

func start(ctx *ext.Context, u *ext.Update) error {
	chatId := u.EffectiveChat().GetID()
	peerChatId := ctx.PeerStorage.GetPeerById(chatId)
	if peerChatId == nil || peerChatId.Type != int(storage.TypeUser) {
		return dispatcher.EndGroups
	}
	if !isAuthorized(chatId) {
		ctx.Reply(u, ext.ReplyTextString("You are not allowed to use this bot."), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(u, ext.ReplyTextString("Hi, send me any file to get a direct streamable link to that file."), nil)
	return dispatcher.EndGroups
}
