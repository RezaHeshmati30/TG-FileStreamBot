package commands

import (
	"context"
	"fmt"
	"unicode/utf16"

	"EverythingSuckz/fsb/internal/mediametadata"
	filetypes "EverythingSuckz/fsb/internal/types"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

const telegramCaptionLimit = 1024

type metadataFuture struct {
	local  mediametadata.Metadata
	result <-chan mediametadata.Metadata
	done   <-chan struct{}
	cancel context.CancelFunc
}

func startMetadataLookup(parent context.Context, file *filetypes.File) *metadataFuture {
	local := mediametadata.Local(file)
	if !mediametadata.Available() {
		return &metadataFuture{local: local}
	}
	lookupContext, cancel := context.WithTimeout(parent, metadataLookupBudget)
	result := make(chan mediametadata.Metadata, 1)
	go func() {
		result <- mediametadata.Resolve(lookupContext, file)
	}()
	return &metadataFuture{local: local, result: result, done: lookupContext.Done(), cancel: cancel}
}

func (future *metadataFuture) await() mediametadata.Metadata {
	if future == nil {
		return mediametadata.Metadata{}
	}
	if future.cancel != nil {
		defer future.cancel()
	}
	if future.result == nil {
		return future.local
	}
	select {
	case metadata := <-future.result:
		return metadata
	case <-future.done:
		return future.local
	}
}

func mediaHeading(metadata mediametadata.Metadata) []styling.StyledTextOption {
	if metadata.Name == "" {
		return nil
	}
	title := metadata.Name
	if metadata.Media == "tv" && metadata.Season > 0 && metadata.Episode > 0 {
		title += fmt.Sprintf(" · S%02dE%02d", metadata.Season, metadata.Episode)
	}
	text := []styling.StyledTextOption{
		styling.Plain("🎬 "),
		styling.Bold(title),
	}
	if metadata.Year != "" {
		text = append(text, styling.Plain("\n📅 "), styling.Bold(metadata.Year))
	}
	return append(text, styling.Plain("\n\n"))
}

func captionFits(text []styling.StyledTextOption) bool {
	builder := entity.Builder{}
	if err := styling.Perform(&builder, text...); err != nil {
		return false
	}
	plain, _ := builder.Complete()
	return len(utf16.Encode([]rune(plain))) <= telegramCaptionLimit
}

func sendPosterReply(ctx *ext.Context, update *ext.Update, poster string, text []styling.StyledTextOption, markup tg.ReplyMarkupClass) error {
	peer := ctx.PeerStorage.GetInputPeerById(update.EffectiveChat().GetID())
	if peer.Zero() {
		return fmt.Errorf("Telegram peer is unavailable")
	}
	builder := ctx.Sender.To(peer).Reply(update.EffectiveMessage.ID)
	if markup != nil {
		builder = builder.Markup(markup)
	}
	_, err := builder.PhotoExternal(ctx, poster, text...)
	return err
}
