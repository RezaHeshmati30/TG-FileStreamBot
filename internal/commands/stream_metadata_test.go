package commands

import (
	"strings"
	"testing"

	"EverythingSuckz/fsb/internal/mediametadata"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
)

func TestMediaHeadingFormatsEpisodeAndYear(t *testing.T) {
	text := mediaHeading(mediametadata.Metadata{Name: "Severance", Year: "2022", Media: "tv", Season: 2, Episode: 4})
	if !styledTextContains(t, text, "Severance · S02E04") || !styledTextContains(t, text, "2022") {
		t.Fatal("heading is missing series metadata")
	}
}

func TestCaptionFitsUsesTelegramUTF16Limit(t *testing.T) {
	if !captionFits([]styling.StyledTextOption{styling.Plain(strings.Repeat("a", telegramCaptionLimit))}) {
		t.Fatal("caption at the limit should fit")
	}
	if captionFits([]styling.StyledTextOption{styling.Plain(strings.Repeat("a", telegramCaptionLimit+1))}) {
		t.Fatal("caption beyond the limit should not fit")
	}
	if captionFits([]styling.StyledTextOption{styling.Plain(strings.Repeat("🎬", telegramCaptionLimit/2+1))}) {
		t.Fatal("UTF-16 surrogate pairs must count as two code units")
	}
}

func styledTextContains(t *testing.T, text []styling.StyledTextOption, expected string) bool {
	t.Helper()
	builder := entity.Builder{}
	if err := styling.Perform(&builder, text...); err != nil {
		t.Fatal(err)
	}
	plain, _ := builder.Complete()
	return strings.Contains(plain, expected)
}
