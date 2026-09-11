package routes

import (
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/linkstate"
	"EverythingSuckz/fsb/internal/stream"
	"EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	range_parser "github.com/quantumsheep/range-parser"
	"go.uber.org/zap"

	"github.com/gin-gonic/gin"
)

var log *zap.Logger

func (e *allRoutes) LoadHome(r *Route) {
	log = e.log.Named("Stream")
	defer log.Info("Loaded stream route")
	r.Engine.GET("/stream/:messageID", getStreamRoute)
	r.Engine.HEAD("/stream/:messageID", getStreamRoute)
	// A filename in the URL lets external players identify subtitle formats
	// even before they have downloaded the response headers or body.
	r.Engine.GET("/subtitle/:messageID/:filename", getStreamRoute)
	r.Engine.HEAD("/subtitle/:messageID/:filename", getStreamRoute)
}

func getStreamRoute(ctx *gin.Context) {
	w := ctx.Writer
	r := ctx.Request

	messageIDParm := ctx.Param("messageID")
	messageID, err := strconv.Atoi(messageIDParm)
	if err != nil {
		renderStreamError(ctx, http.StatusBadRequest, "Invalid link", "This file link is malformed and cannot be opened.")
		return
	}

	signature := ctx.Query("signature")
	if signature == "" {
		renderStreamError(ctx, http.StatusBadRequest, "Incomplete link", "This file link is missing required information.")
		return
	}
	expiresAt, err := strconv.ParseInt(ctx.Query("expires"), 10, 64)
	if err != nil {
		renderStreamError(ctx, http.StatusBadRequest, "Incomplete link", "This file link is missing or contains invalid expiration information.")
		return
	}
	if time.Now().Unix() > expiresAt {
		renderStreamError(ctx, http.StatusGone, "Link expired", "This link has reached the end of its 7-day validity period and is no longer available.")
		return
	}
	if !linkstate.Allows(messageID, expiresAt) {
		renderStreamError(ctx, http.StatusGone, "Link replaced", "This link was expired or replaced by a newer link.")
		return
	}

	worker := bot.GetNextWorker()

	file, err := utils.TimeFuncWithResult(log, "FileFromMessage", func() (*types.File, error) {
		return utils.FileFromMessage(ctx, worker.Client, messageID)
	})
	if err != nil {
		log.Warn("File for stream link is unavailable", zap.Error(err))
		renderStreamError(ctx, http.StatusNotFound, "File unavailable", "The requested file could not be found or is no longer available.")
		return
	}

	expectedSignature := utils.SignFile(
		file.FileName,
		file.FileSize,
		file.MimeType,
		file.ID,
		expiresAt,
	)
	if !utils.CheckSignature(signature, expectedSignature) {
		renderStreamError(ctx, http.StatusForbidden, "Invalid link", "This link is invalid or has been modified and cannot be used.")
		return
	}

	// for photo messages
	if file.FileSize == 0 {
		res, err := worker.Client.API().UploadGetFile(ctx, &tg.UploadGetFileRequest{
			Location: file.Location,
			Offset:   0,
			Limit:    1024 * 1024,
		})
		if err != nil {
			log.Error("Failed to download photo", zap.Error(err))
			renderStreamError(ctx, http.StatusBadGateway, "File temporarily unavailable", "The file could not be loaded right now. Please try again later.")
			return
		}
		result, ok := res.(*tg.UploadFile)
		if !ok {
			log.Error("Unexpected Telegram response while downloading photo", zap.String("type", fmt.Sprintf("%T", res)))
			renderStreamError(ctx, http.StatusInternalServerError, "Something went wrong", "The file could not be prepared for viewing.")
			return
		}
		fileBytes := result.GetBytes()
		ctx.Header("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", file.FileName))
		if r.Method != "HEAD" {
			ctx.Data(http.StatusOK, file.MimeType, fileBytes)
		}
		return
	}

	ctx.Header("Accept-Ranges", "bytes")
	var start, end int64
	rangeHeader := r.Header.Get("Range")
	status := http.StatusOK

	if rangeHeader == "" {
		start = 0
		end = file.FileSize - 1
	} else {
		ranges, err := range_parser.Parse(file.FileSize, r.Header.Get("Range"))
		if err != nil {
			renderStreamError(ctx, http.StatusRequestedRangeNotSatisfiable, "Unsupported file range", "The requested part of this file is not available.")
			return
		}
		start = ranges[0].Start
		end = ranges[0].End
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, file.FileSize))
		log.Info("Content-Range", zap.Int64("start", start), zap.Int64("end", end), zap.Int64("fileSize", file.FileSize))
		status = http.StatusPartialContent
	}

	contentLength := end - start + 1
	mimeType := streamResponseMIMEType(file.FileName, file.MimeType, ctx.FullPath())

	ctx.Header("Content-Type", mimeType)
	ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))

	disposition := "inline"

	if ctx.Query("d") == "true" {
		disposition = "attachment"
	}

	ctx.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": file.FileName}))
	// Headers must be complete before WriteHeader. Otherwise net/http commits a
	// response without the MIME type and filename that players use to detect SRT.
	w.WriteHeader(status)

	if r.Method != "HEAD" {
		pipe, err := stream.NewStreamPipe(ctx, worker.Client, file.Location, start, end, log)
		if err != nil {
			log.Error("Failed to create stream pipe", zap.Error(err))
			return
		}
		defer pipe.Close()
		if _, err := io.CopyN(w, pipe, contentLength); err != nil {
			if !utils.IsClientDisconnectError(err) {
				log.Error("Error while copying stream", zap.Error(err))
			}
		}
	}
}

func streamResponseMIMEType(fileName, storedMIMEType, routePattern string) string {
	if strings.HasPrefix(routePattern, "/subtitle/") {
		switch strings.ToLower(filepath.Ext(fileName)) {
		case ".srt":
			return "application/x-subrip; charset=utf-8"
		case ".vtt":
			return "text/vtt; charset=utf-8"
		case ".ass", ".ssa":
			return "text/x-ssa; charset=utf-8"
		}
	}
	if strings.TrimSpace(storedMIMEType) != "" {
		return storedMIMEType
	}
	return "application/octet-stream"
}
