package routes

import (
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type playerDefinition struct {
	Name       string
	Icon       string
	ButtonText string
	DeepLink   func(string, string) string
}

type playerPageData struct {
	AppName    string
	Icon       string
	ButtonText string
	DeepLink   string
	Title      string
}

var externalPlayers = map[string]playerDefinition{
	"wvc": {
		Name:       "Web Video Caster",
		Icon:       "📺",
		ButtonText: "Open in Web Video Caster",
		DeepLink:   buildWVCVideoDeepLink,
	},
	"vlc": {
		Name:       "VLC",
		Icon:       "▶️",
		ButtonText: "Open in VLC",
		DeepLink:   buildVLCDeepLink,
	},
}

var playerPage = template.Must(template.New("player-launch").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="robots" content="noindex, nofollow">
  <title>Open in {{.AppName}}</title>
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
    <div class="icon" aria-hidden="true">{{.Icon}}</div>
    <h1>Opening {{.AppName}}…</h1>
    <p class="title">{{.Title}}</p>
    <a href="{{.DeepLink}}">{{.ButtonText}}</a>
    <p class="hint">If the app does not open automatically, tap the button above. {{.AppName}} must be installed on this device.</p>
  </main>
  <script>
    const deepLink = {{.DeepLink}};
    window.setTimeout(() => { window.location.href = deepLink; }, 120);
  </script>
</body>
</html>`))

func (e *allRoutes) LoadPlayer(r *Route) {
	e.log.Named("Player").Info("Loaded external player routes")
	r.Engine.GET("/player/:player", getPlayerLaunchRoute)
}

func getPlayerLaunchRoute(ctx *gin.Context) {
	playerID := ctx.Param("player")
	player, supported := externalPlayers[playerID]
	videoMessageID, videoErr := strconv.Atoi(ctx.Query("video"))
	expires, expiresErr := strconv.ParseInt(ctx.Query("expires"), 10, 64)
	signature := ctx.Query("signature")
	if !supported || videoErr != nil || expiresErr != nil || videoMessageID <= 0 || signature == "" {
		renderStreamError(ctx, http.StatusBadRequest, "Invalid player link", "This external player link is malformed or incomplete.")
		return
	}
	if time.Now().Unix() > expires {
		renderStreamError(ctx, http.StatusGone, "Player link expired", "The video link has expired and can no longer be opened.")
		return
	}
	expected := utils.SignPlayerLaunch(playerID, videoMessageID, expires)
	if !utils.CheckSignature(signature, expected) {
		renderStreamError(ctx, http.StatusForbidden, "Invalid player link", "This external player link has been modified and cannot be used.")
		return
	}

	worker := bot.GetNextWorker()
	video, err := utils.FileFromMessage(ctx, worker.Client, videoMessageID)
	if err != nil {
		log.Warn("External player video is unavailable", zap.String("player", playerID), zap.Error(err))
		renderStreamError(ctx, http.StatusNotFound, "Video unavailable", "The original video could not be found.")
		return
	}
	videoURL := publicStreamURL(video, videoMessageID, expires)
	deepLink := player.DeepLink(videoURL, video.FileName)

	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	if err := playerPage.Execute(ctx.Writer, playerPageData{
		AppName: player.Name, Icon: player.Icon, ButtonText: player.ButtonText,
		DeepLink: deepLink, Title: video.FileName,
	}); err != nil {
		log.Error("Failed to render external player page", zap.String("player", playerID), zap.Error(err))
	}
}

func buildWVCVideoDeepLink(videoURL, title string) string {
	query := url.Values{
		"url":       {videoURL},
		"title":     {title},
		"autostart": {"true"},
	}
	return "wvc-x-callback://open?" + query.Encode()
}

func buildVLCDeepLink(videoURL, _ string) string {
	query := url.Values{"url": {videoURL}}
	return "vlc-x-callback://x-callback-url/stream?" + query.Encode()
}
