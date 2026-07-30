package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

const accessStatePrefix = "FSB_ACCESS_STATE_V1 "

type accessState struct {
	Users     []int64 `json:"users"`
	ActorID   int64   `json:"actor_id"`
	UpdatedAt int64   `json:"updated_at"`
}

type accessStore struct {
	mu        sync.RWMutex
	users     map[int64]struct{}
	messageID int
}

var botAccess = &accessStore{users: make(map[int64]struct{})}

func (m *command) LoadAccess(dispatcher dispatcher.Dispatcher) {
	log := m.log.Named("access")
	defer log.Info("Loaded")
	dispatcher.AddHandler(handlers.NewCommand("id", showID))
	dispatcher.AddHandler(handlers.NewCommand("allow", allowUser))
	dispatcher.AddHandler(handlers.NewCommand("deny", denyUser))
	dispatcher.AddHandler(handlers.NewCommand("users", listUsers))
}

func InitializeAccess(ctx context.Context, client *gotgproto.Client, log *zap.Logger) error {
	channel, err := utils.GetChannelPeer(ctx, client.API(), client.PeerStorage, config.ValueOf.AccessChannelID)
	if err != nil {
		return fmt.Errorf("resolve ACCESS_CHANNEL: %w", err)
	}
	peer := &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}

	users := make(map[int64]struct{}, len(config.ValueOf.AllowedUsers)+1)
	for _, userID := range config.ValueOf.AllowedUsers {
		users[userID] = struct{}{}
	}
	users[config.ValueOf.OwnerID] = struct{}{}

	messageID, state, err := loadPinnedAccessState(ctx, client.API(), channel)
	if err != nil {
		return err
	}
	if messageID == 0 {
		messageID, err = createPinnedAccessState(ctx, client.API(), peer, users)
		if err != nil {
			return fmt.Errorf("create pinned access state: %w", err)
		}
	} else {
		users = usersFromState(state)
		users[config.ValueOf.OwnerID] = struct{}{}
	}

	botAccess.mu.Lock()
	botAccess.users = users
	botAccess.messageID = messageID
	botAccess.mu.Unlock()
	log.Info("Loaded access list", zap.Int("users", len(users)), zap.Int("message_id", messageID))
	return nil
}

func loadPinnedAccessState(ctx context.Context, api *tg.Client, channel *tg.InputChannel) (int, accessState, error) {
	full, err := api.ChannelsGetFullChannel(ctx, channel)
	if err != nil {
		return 0, accessState{}, fmt.Errorf("read ACCESS_CHANNEL details: %w", err)
	}
	channelFull, ok := full.FullChat.(*tg.ChannelFull)
	if !ok {
		return 0, accessState{}, fmt.Errorf("ACCESS_CHANNEL returned unexpected channel details")
	}
	pinnedID, ok := channelFull.GetPinnedMsgID()
	if !ok || pinnedID == 0 {
		return 0, accessState{}, nil
	}

	result, err := api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: channel,
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: pinnedID}},
	})
	if err != nil {
		return 0, accessState{}, fmt.Errorf("read pinned ACCESS_CHANNEL message: %w", err)
	}
	messages, err := messagesFromResult(result)
	if err != nil {
		return 0, accessState{}, err
	}
	if len(messages) != 1 {
		return 0, accessState{}, fmt.Errorf("pinned ACCESS_CHANNEL message was not found")
	}
	message, ok := messages[0].(*tg.Message)
	if !ok {
		return 0, accessState{}, fmt.Errorf("pinned ACCESS_CHANNEL message has an unexpected type")
	}
	state, ok := decodeAccessState(message.Message)
	if !ok {
		return 0, accessState{}, fmt.Errorf("ACCESS_CHANNEL has an unrelated pinned message; unpin it and restart the bot")
	}
	return pinnedID, state, nil
}

func messagesFromResult(result tg.MessagesMessagesClass) ([]tg.MessageClass, error) {
	switch value := result.(type) {
	case *tg.MessagesChannelMessages:
		return value.Messages, nil
	case *tg.MessagesMessages:
		return value.Messages, nil
	case *tg.MessagesMessagesSlice:
		return value.Messages, nil
	default:
		return nil, fmt.Errorf("pinned ACCESS_CHANNEL message returned unexpected result type %T", result)
	}
}

func createPinnedAccessState(ctx context.Context, api *tg.Client, peer *tg.InputPeerChannel, users map[int64]struct{}) (int, error) {
	text, err := encodeAccessState(users, config.ValueOf.OwnerID)
	if err != nil {
		return 0, err
	}
	updates, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: rand.Int63(),
	})
	if err != nil {
		return 0, err
	}
	messageID := sentMessageID(updates)
	if messageID == 0 {
		return 0, fmt.Errorf("Telegram did not return the access-state message ID")
	}
	_, err = api.MessagesUpdatePinnedMessage(ctx, &tg.MessagesUpdatePinnedMessageRequest{
		Silent: true,
		Peer:   peer,
		ID:     messageID,
	})
	if err != nil {
		return 0, err
	}
	return messageID, nil
}

func sentMessageID(updates tg.UpdatesClass) int {
	switch value := updates.(type) {
	case *tg.Updates:
		for _, update := range value.Updates {
			if newMessage, ok := update.(*tg.UpdateNewChannelMessage); ok {
				if message, ok := newMessage.Message.(*tg.Message); ok {
					return message.ID
				}
			}
		}
	case *tg.UpdateShortSentMessage:
		return value.ID
	}
	return 0
}

func decodeAccessState(text string) (accessState, bool) {
	var state accessState
	if !strings.HasPrefix(text, accessStatePrefix) {
		return state, false
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(text, accessStatePrefix)), &state); err != nil {
		return accessState{}, false
	}
	for _, userID := range state.Users {
		if userID <= 0 {
			return accessState{}, false
		}
	}
	return state, true
}

func usersFromState(state accessState) map[int64]struct{} {
	users := make(map[int64]struct{}, len(state.Users))
	for _, userID := range state.Users {
		users[userID] = struct{}{}
	}
	return users
}

func encodeAccessState(users map[int64]struct{}, actorID int64) (string, error) {
	userIDs := sortedUserIDs(users)
	data, err := json.Marshal(accessState{Users: userIDs, ActorID: actorID, UpdatedAt: time.Now().Unix()})
	if err != nil {
		return "", err
	}
	return accessStatePrefix + string(data), nil
}

func sortedUserIDs(users map[int64]struct{}) []int64 {
	userIDs := make([]int64, 0, len(users))
	for userID := range users {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	return userIDs
}

func isAuthorized(userID int64) bool {
	if userID == config.ValueOf.OwnerID {
		return true
	}
	botAccess.mu.RLock()
	defer botAccess.mu.RUnlock()
	_, ok := botAccess.users[userID]
	return ok
}

func isPrivateChat(ctx *ext.Context, u *ext.Update) bool {
	peer := ctx.PeerStorage.GetPeerById(u.EffectiveChat().GetID())
	return peer != nil && peer.Type == int(storage.TypeUser)
}

func showID(ctx *ext.Context, u *ext.Update) error {
	if !isPrivateChat(ctx, u) {
		return dispatcher.EndGroups
	}
	user := u.EffectiveUser()
	if user != nil {
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("Your Telegram user ID: %d", user.ID)), nil)
	}
	return dispatcher.EndGroups
}

func allowUser(ctx *ext.Context, u *ext.Update) error {
	return changeAccess(ctx, u, true)
}

func denyUser(ctx *ext.Context, u *ext.Update) error {
	return changeAccess(ctx, u, false)
}

func changeAccess(ctx *ext.Context, u *ext.Update, allow bool) error {
	if !isPrivateChat(ctx, u) {
		return dispatcher.EndGroups
	}
	actor := u.EffectiveUser()
	if actor == nil || actor.ID != config.ValueOf.OwnerID {
		ctx.Reply(u, ext.ReplyTextString("You are not allowed to manage access."), nil)
		return dispatcher.EndGroups
	}

	command := "deny"
	if allow {
		command = "allow"
	}
	args := u.Args()
	if len(args) != 2 {
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("Usage: /%s <user ID>", command)), nil)
		return dispatcher.EndGroups
	}
	userID, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || userID <= 0 {
		ctx.Reply(u, ext.ReplyTextString("Please provide a valid Telegram user ID."), nil)
		return dispatcher.EndGroups
	}
	if userID == config.ValueOf.OwnerID {
		message := "The owner always has access."
		if !allow {
			message = "The owner cannot be denied access."
		}
		ctx.Reply(u, ext.ReplyTextString(message), nil)
		return dispatcher.EndGroups
	}

	botAccess.mu.Lock()
	defer botAccess.mu.Unlock()
	_, authorized := botAccess.users[userID]
	if allow == authorized {
		state := "already denied"
		if allow {
			state = "already allowed"
		}
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("User %d is %s.", userID, state)), nil)
		return dispatcher.EndGroups
	}

	updated := make(map[int64]struct{}, len(botAccess.users)+1)
	for id := range botAccess.users {
		updated[id] = struct{}{}
	}
	if allow {
		updated[userID] = struct{}{}
	} else {
		delete(updated, userID)
	}
	if err := persistAccessState(ctx, botAccess.messageID, updated, actor.ID); err != nil {
		utils.Logger.Error("Could not persist access change", zap.Error(err))
		ctx.Reply(u, ext.ReplyTextString("The access change could not be saved. Nothing was changed."), nil)
		return dispatcher.EndGroups
	}
	botAccess.users = updated

	state := "denied"
	if allow {
		state = "allowed"
	}
	ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("User %d is now %s.", userID, state)), nil)
	return dispatcher.EndGroups
}

func persistAccessState(ctx *ext.Context, messageID int, users map[int64]struct{}, actorID int64) error {
	text, err := encodeAccessState(users, actorID)
	if err != nil {
		return err
	}
	channel, err := utils.GetChannelPeer(ctx, ctx.Raw, ctx.PeerStorage, config.ValueOf.AccessChannelID)
	if err != nil {
		return err
	}
	_, err = ctx.Raw.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:    &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
		ID:      messageID,
		Message: text,
	})
	return err
}

func listUsers(ctx *ext.Context, u *ext.Update) error {
	if !isPrivateChat(ctx, u) {
		return dispatcher.EndGroups
	}
	actor := u.EffectiveUser()
	if actor == nil || actor.ID != config.ValueOf.OwnerID {
		ctx.Reply(u, ext.ReplyTextString("You are not allowed to manage access."), nil)
		return dispatcher.EndGroups
	}

	botAccess.mu.RLock()
	userIDs := sortedUserIDs(botAccess.users)
	botAccess.mu.RUnlock()
	lines := []string{fmt.Sprintf("Authorized users (%d):", len(userIDs))}
	for _, userID := range userIDs {
		suffix := ""
		if userID == config.ValueOf.OwnerID {
			suffix = " (owner)"
		}
		lines = append(lines, fmt.Sprintf("• %d%s", userID, suffix))
	}
	ctx.Reply(u, ext.ReplyTextString(strings.Join(lines, "\n")), nil)
	return dispatcher.EndGroups
}
