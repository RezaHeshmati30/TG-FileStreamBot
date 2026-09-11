package commands

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/mediametadata"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/storage"
	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/tg"
)

const metadataLookupBudget = 1200 * time.Millisecond

func (m *command) LoadStream(dispatcher dispatcher.Dispatcher) {
	log := m.log.Named("start")
	defer log.Sugar().Info("Loaded")
	dispatcher.AddHandler(
		handlers.NewMessage(nil, sendLink),
	)
}

func supportedMediaFilter(m *types.Message) (bool, error) {
	if not := m.Media == nil; not {
		return false, dispatcher.EndGroups
	}
	switch m.Media.(type) {
	case *tg.MessageMediaDocument:
		return true, nil
	case *tg.MessageMediaPhoto:
		return true, nil
	case tg.MessageMediaClass:
		return false, dispatcher.EndGroups
	default:
		return false, nil
	}
}

func formatFileSize(size int64) string {
	if size <= 0 {
		return "Unknown"
	}

	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}

	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func displayLocation() *time.Location {
	location, err := time.LoadLocation(config.ValueOf.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

func fileLinkPresentation(fileName, mimeType string) (string, string, string) {
	extension := strings.ToLower(filepath.Ext(fileName))
	if strings.Contains(strings.ToLower(mimeType), "video") || utils.Contains([]string{".mkv", ".mp4", ".webm", ".mov", ".avi", ".m4v", ".ts", ".m2ts"}, extension) {
		return "🎬", "Your video link is ready!", "Video Link"
	}
	if utils.Contains([]string{".srt", ".vtt", ".ass", ".ssa", ".sub"}, extension) || strings.Contains(strings.ToLower(mimeType), "subrip") || strings.Contains(strings.ToLower(mimeType), "webvtt") {
		return "💬", "Your subtitle link is ready!", "Subtitle Link"
	}
	return "📎", "Your file link is ready!", "File Link"
}

func sendLink(ctx *ext.Context, u *ext.Update) error {
	chatId := u.EffectiveChat().GetID()
	peerChatId := ctx.PeerStorage.GetPeerById(chatId)
	if peerChatId == nil || peerChatId.Type != int(storage.TypeUser) {
		return dispatcher.EndGroups
	}
	if !isAuthorized(chatId) {
		sendUnauthorizedNotice(ctx, u)
		return dispatcher.EndGroups
	}
	rememberAuthorizedUsername(ctx, u.EffectiveUser())
	supported, err := supportedMediaFilter(u.EffectiveMessage)
	if err != nil {
		return err
	}
	if !supported {
		ctx.Reply(u, ext.ReplyTextString("Sorry, this message type is unsupported."), nil)
		return dispatcher.EndGroups
	}
	var metadata *metadataFuture
	if sourceFile, sourceErr := utils.FileFromMedia(u.EffectiveMessage.Media); sourceErr == nil {
		if icon, _, _ := fileLinkPresentation(sourceFile.FileName, sourceFile.MimeType); icon == "🎬" {
			metadata = startMetadataLookup(ctx, sourceFile)
		}
	}
	update, err := utils.ForwardMessages(ctx, chatId, config.ValueOf.LogChannelID, u.EffectiveMessage.ID)
	if err != nil {
		utils.Logger.Sugar().Error(err)
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("Error - %s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	if len(update.Updates) < 2 {
		ctx.Reply(u, ext.ReplyTextString("Error - unexpected update structure from Telegram"), nil)
		return dispatcher.EndGroups
	}
	msgIDUpdate, ok := update.Updates[0].(*tg.UpdateMessageID)
	if !ok {
		ctx.Reply(u, ext.ReplyTextString("Error - unexpected update type"), nil)
		return dispatcher.EndGroups
	}
	messageID := msgIDUpdate.ID
	newMsg, ok := update.Updates[1].(*tg.UpdateNewChannelMessage)
	if !ok {
		ctx.Reply(u, ext.ReplyTextString("Error - unexpected channel message update"), nil)
		return dispatcher.EndGroups
	}
	msg, ok := newMsg.Message.(*tg.Message)
	if !ok {
		ctx.Reply(u, ext.ReplyTextString("Error - unexpected message type"), nil)
		return dispatcher.EndGroups
	}
	doc := msg.Media
	file, err := utils.FileFromMedia(doc)
	if err != nil {
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("Error - %s", err.Error())), nil)
		return dispatcher.EndGroups
	}
	createdAt := time.Now().UTC()
	expiresAt := createdAt.Add(7 * 24 * time.Hour).Unix()
	signature := utils.SignFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
		expiresAt,
	)
	link := fmt.Sprintf("%s/stream/%d?signature=%s&expires=%d", config.ValueOf.Host, messageID, signature, expiresAt)
	fileIcon, readyText, linkLabel := fileLinkPresentation(file.FileName, file.MimeType)
	baseText := buildStreamMessageText(file, link, fileIcon, readyText, linkLabel, expiresAt)
	markup := buildStreamMainMarkup(file, link, fileIcon, messageID, expiresAt)
	var posterMetadata mediametadata.Metadata
	if metadata != nil {
		posterMetadata = metadata.await()
	}
	messageSent := false
	if fileIcon == "🎬" && posterMetadata.Poster != "" {
		posterText := append(mediaHeading(posterMetadata), baseText...)
		if captionFits(posterText) {
			posterMarkup := tg.ReplyMarkupClass(markup)
			if strings.Contains(link, "http://localhost") {
				posterMarkup = nil
			}
			if posterErr := sendPosterReply(ctx, u, posterMetadata.Poster, posterText, posterMarkup); posterErr == nil {
				messageSent = true
			} else {
				utils.Logger.Sugar().Warnw("Could not send TMDb poster; using text fallback", "error", posterErr)
			}
		}
	}
	if messageSent {
		return dispatcher.EndGroups
	}
	if strings.Contains(link, "http://localhost") {
		_, err = ctx.Reply(u, ext.ReplyTextStyledTextArray(baseText), &ext.ReplyOpts{
			NoWebpage:        true,
			ReplyToMessageId: u.EffectiveMessage.ID,
		})
	} else {
		_, err = ctx.Reply(u, ext.ReplyTextStyledTextArray(baseText), &ext.ReplyOpts{
			Markup:           markup,
			NoWebpage:        true,
			ReplyToMessageId: u.EffectiveMessage.ID,
		})
	}
	if err != nil {
		utils.Logger.Sugar().Error(err)
		ctx.Reply(u, ext.ReplyTextString(fmt.Sprintf("Error - %s", err.Error())), nil)
	}
	return dispatcher.EndGroups
}

func externalPlayerURL(player string, messageID int, expires int64) string {
	query := url.Values{
		"video":     {strconv.Itoa(messageID)},
		"expires":   {strconv.FormatInt(expires, 10)},
		"signature": {utils.SignPlayerLaunch(player, messageID, expires)},
	}
	return fmt.Sprintf("%s/player/%s?%s", strings.TrimRight(config.ValueOf.Host, "/"), player, query.Encode())
}
