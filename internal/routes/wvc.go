package routes

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/types"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type wvcPageData struct {
	DeepLink string
	Title    string
}

var wvcPage = template.Must(template.New("wvc-launch").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="robots" content="noindex, nofollow">
  <title>Open in Web Video Caster</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px; color: #e8edf7; background: radial-gradient(circle at 50% 0%, #273d72 0, #10182b 43%, #080c15 100%); }
    main { width: min(100%, 520px); padding: 42px 32px 34px; text-align: center; border: 1px solid rgba(148, 163, 184, .18); border-radius: 25px; background: rgba(15, 23, 42, .9); box-shadow: 0 24px 70px rgba(0, 0, 0, .4); }
    .icon { width: 76px; height: 76px; margin: 0 auto 22px; display: grid; place-items: center; border-radius: 23px; background: linear-gradient(145deg, #8b5cf6, #6366f1); font-size: 36px; box-shadow: 0 14px 35px rgba(99, 102, 241, .34); }
    h1 { margin: 0; color: #f8fafc; font-size: clamp(26px, 7vw, 34px); line-height: 1.15; letter-spacing: -.025em; }
    .title { margin: 14px auto 0; max-width: 420px; color: #aebbd0; font-size: 15px; line-height: 1.55; overflow-wrap: anywhere; }
    a { margin-top: 28px; min-height: 54px; display: inline-flex; align-items: center; justify-content: center; width: 100%; padding: 14px 20px; border-radius: 15px; color: white; background: #6366f1; font-size: 16px; font-weight: 750; text-decoration: none; box-shadow: 0 10px 28px rgba(99, 102, 241, .3); }
    .hint { margin: 18px auto 0; color: #7f8da5; font-size: 13px; line-height: 1.55; }
  </style>
</head>
<body>
  <main>
    <div class="icon" aria-hidden="true">📺</div>
    <h1>Opening Web Video Caster…</h1>
    <p class="title">{{.Title}}</p>
    <a href="{{.DeepLink}}">Open in Web Video Caster</a>
    <p class="hint">If the app does not open automatically, tap the button above. Web Video Caster must be installed on this device.</p>
  </main>
  <script>
    const deepLink = {{.DeepLink}};
    window.setTimeout(() => { window.location.href = deepLink; }, 120);
  </script>
</body>
</html>`))

func (e *allRoutes) LoadWVC(r *Route) {
	e.log.Named("WVC").Info("Loaded Web Video Caster route")
	r.Engine.GET("/wvc", getWVCLaunchRoute)
}

func getWVCLaunchRoute(ctx *gin.Context) {
	videoMessageID, videoErr := strconv.Atoi(ctx.Query("video"))
	subtitleMessageID, subtitleErr := strconv.Atoi(ctx.Query("subtitle"))
	expires, expiresErr := strconv.ParseInt(ctx.Query("expires"), 10, 64)
	signature := ctx.Query("signature")
	if videoErr != nil || subtitleErr != nil || expiresErr != nil || videoMessageID <= 0 || subtitleMessageID <= 0 || signature == "" {
		renderStreamError(ctx, http.StatusBadRequest, "Invalid WVC link", "This Web Video Caster link is malformed or incomplete.")
		return
	}
	if time.Now().Unix() > expires {
		renderStreamError(ctx, http.StatusGone, "WVC link expired", "The video and subtitle links have expired and can no longer be opened.")
		return
	}
	expected := utils.SignWVCLaunch(videoMessageID, subtitleMessageID, expires)
	if !utils.CheckSignature(signature, expected) {
		renderStreamError(ctx, http.StatusForbidden, "Invalid WVC link", "This Web Video Caster link has been modified and cannot be used.")
		return
	}

	worker := bot.GetNextWorker()
	video, err := utils.FileFromMessage(ctx, worker.Client, videoMessageID)
	if err != nil {
		log.Warn("WVC video is unavailable", zap.Error(err))
		renderStreamError(ctx, http.StatusNotFound, "Video unavailable", "The original video could not be found.")
		return
	}
	subtitle, err := utils.FileFromMessage(ctx, worker.Client, subtitleMessageID)
	if err != nil {
		log.Warn("WVC subtitle is unavailable", zap.Error(err))
		renderStreamError(ctx, http.StatusNotFound, "Subtitle unavailable", "The selected subtitle could not be found.")
		return
	}

	videoURL := publicStreamURL(video, videoMessageID, expires)
	subtitleURL := publicSubtitleURL(subtitle, subtitleMessageID, expires)
	deepLink := buildWVCDeepLink(videoURL, subtitleURL, video.FileName)

	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	if err := wvcPage.Execute(ctx.Writer, wvcPageData{DeepLink: deepLink, Title: video.FileName}); err != nil {
		log.Error("Failed to render WVC launch page", zap.Error(err))
	}
}

func publicSubtitleURL(file *types.File, messageID int, expires int64) string {
	signature := utils.SignFile(file.FileName, file.FileSize, file.MimeType, file.ID, expires)
	query := url.Values{
		"signature": {signature},
		"expires":   {strconv.FormatInt(expires, 10)},
	}
	fileName := filepath.Base(strings.TrimSpace(file.FileName))
	if fileName == "" || fileName == "." {
		fileName = "subtitle.srt"
	}
	return fmt.Sprintf("%s/subtitle/%d/%s?%s", strings.TrimRight(config.ValueOf.Host, "/"), messageID, url.PathEscape(fileName), query.Encode())
}

func publicStreamURL(file *types.File, messageID int, expires int64) string {
	signature := utils.SignFile(file.FileName, file.FileSize, file.MimeType, file.ID, expires)
	query := url.Values{
		"signature": {signature},
		"expires":   {strconv.FormatInt(expires, 10)},
	}
	return fmt.Sprintf("%s/stream/%d?%s", strings.TrimRight(config.ValueOf.Host, "/"), messageID, query.Encode())
}

func buildWVCDeepLink(videoURL, subtitleURL, title string) string {
	query := url.Values{
		"url":       {videoURL},
		"subtitle":  {subtitleURL},
		"title":     {title},
		"autostart": {"true"},
	}
	return "wvc-x-callback://open?" + query.Encode()
}
