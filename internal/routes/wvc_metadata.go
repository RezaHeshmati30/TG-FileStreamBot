package routes

import (
	"context"

	"EverythingSuckz/fsb/internal/mediametadata"
	"EverythingSuckz/fsb/internal/types"
)

type wvcMediaMetadata = mediametadata.Metadata

func resolveWVCMetadata(ctx context.Context, file *types.File) wvcMediaMetadata {
	return mediametadata.Resolve(ctx, file)
}
