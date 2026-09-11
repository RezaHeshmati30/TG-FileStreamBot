package routes

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/bot"
	"EverythingSuckz/fsb/internal/linkstate"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"rsc.io/qr"
)

type handoffPageData struct {
	Title       string
	QRCode      template.URL
	StreamURL   string
	DownloadURL string
	WVCURL      string
	VLCURL      string
	Expires     string
}

var handoffPage = template.Must(template.New("handoff").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <meta name="robots" content="noindex, nofollow">
  <title>Cross-device handoff</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; padding: 24px; color: #e8edf7; background: radial-gradient(circle at 50% 0%, #155e75 0, #10182b 43%, #080c15 100%); }
    main { width: min(100%, 560px); margin: 0 auto; padding: 34px 28px 28px; text-align: center; border: 1px solid rgba(148,163,184,.18); border-radius: 25px; background: rgba(15,23,42,.92); box-shadow: 0 24px 70px rgba(0,0,0,.4); }
    .eyebrow { color: #67e8f9; font-size: 12px; font-weight: 800; letter-spacing: .14em; text-transform: uppercase; }
    h1 { margin: 10px 0 0; color: #f8fafc; font-size: clamp(25px,7vw,34px); line-height: 1.15; overflow-wrap: anywhere; }
    .lead { margin: 13px auto 22px; color: #aebbd0; line-height: 1.55; }
    .qr { display: block; width: min(100%, 320px); height: auto; margin: 0 auto; padding: 12px; border-radius: 20px; background: white; }
    .hint { margin: 16px auto 0; color: #7f8da5; font-size: 13px; line-height: 1.5; }
    .actions { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-top: 24px; }
    a { min-height: 50px; display: flex; align-items: center; justify-content: center; padding: 12px; border-radius: 14px; color: white; background: #0891b2; font-weight: 750; text-decoration: none; }
    a.secondary { background: rgba(148,163,184,.14); border: 1px solid rgba(148,163,184,.18); }
    @media (max-width: 420px) { .actions { grid-template-columns: 1fr; } main { padding: 28px 20px 22px; } }
  </style>
</head>
<body>
  <main>
    <div class="eyebrow">Cross-device handoff</div>
    <h1>{{.Title}}</h1>
    <p class="lead">Scan this QR code with another device, or use an action below on this device.</p>
    <img class="qr" src="{{.QRCode}}" alt="QR code for this handoff page" width="320" height="320">
    <p class="hint">This handoff expires at {{.Expires}}. Anyone with the QR code can access the file until then.</p>
    <div class="actions">
      <a href="{{.StreamURL}}">▶ Open stream</a>
      <a class="secondary" href="{{.DownloadURL}}">⬇ Download</a>
      <a href="{{.WVCURL}}">📺 Open in WVC</a>
      <a class="secondary" href="{{.VLCURL}}">▶ Open in VLC</a>
    </div>
  </main>
</body>
</html>`))

func (e *allRoutes) LoadHandoff(r *Route) {
	e.log.Named("Handoff").Info("Loaded cross-device handoff route")
	r.Engine.GET("/handoff/:messageID", getHandoffRoute)
}

func getHandoffRoute(ctx *gin.Context) {
	messageID, messageErr := strconv.Atoi(ctx.Param("messageID"))
	expires, expiresErr := strconv.ParseInt(ctx.Query("expires"), 10, 64)
	sourceExpires, sourceExpiresErr := strconv.ParseInt(ctx.Query("source_expires"), 10, 64)
	signature := ctx.Query("signature")
	if messageErr != nil || expiresErr != nil || sourceExpiresErr != nil || messageID <= 0 || expires <= 0 || sourceExpires <= 0 || signature == "" {
		renderStreamError(ctx, http.StatusBadRequest, "Invalid handoff", "This cross-device handoff is malformed or incomplete.")
		return
	}
	if time.Now().Unix() > expires {
		renderStreamError(ctx, http.StatusGone, "Handoff expired", "This cross-device handoff has expired. Please create a new QR code in Telegram.")
		return
	}
	if !linkstate.Allows(messageID, sourceExpires) {
		renderStreamError(ctx, http.StatusGone, "Handoff replaced", "This handoff was expired or replaced by a newer link.")
		return
	}
	if !utils.CheckSignature(signature, utils.SignHandoffPage(messageID, sourceExpires, expires)) {
		renderStreamError(ctx, http.StatusForbidden, "Invalid handoff", "This cross-device handoff has been modified and cannot be used.")
		return
	}

	worker := bot.GetNextWorker()
	file, err := utils.FileFromMessage(ctx, worker.Client, messageID)
	if err != nil {
		log.Warn("Handoff file is unavailable", zap.Error(err))
		renderStreamError(ctx, http.StatusNotFound, "File unavailable", "The original file could not be found.")
		return
	}
	streamURL := publicStreamURL(file, messageID, sourceExpires)
	pageURL := canonicalHandoffURL(messageID, sourceExpires, expires, signature)
	code, err := qr.Encode(pageURL, qr.M)
	if err != nil {
		log.Error("Could not generate handoff QR code", zap.Error(err))
		renderStreamError(ctx, http.StatusInternalServerError, "QR code unavailable", "The QR code could not be generated.")
		return
	}
	qrDataURL := template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())) // #nosec G203 -- locally generated PNG data URL

	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'; frame-ancestors 'none'")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	if err := handoffPage.Execute(ctx.Writer, handoffPageData{
		Title: file.FileName, QRCode: qrDataURL, StreamURL: streamURL,
		DownloadURL: streamURL + "&d=true",
		WVCURL:      playerLaunchURL("wvc", messageID, sourceExpires),
		VLCURL:      playerLaunchURL("vlc", messageID, sourceExpires),
		Expires:     time.Unix(expires, 0).In(configuredLocation()).Format("02 Jan 2006, 15:04 MST"),
	}); err != nil {
		log.Error("Failed to render handoff page", zap.Error(err))
	}
}

func canonicalHandoffURL(messageID int, sourceExpires int64, expires int64, signature string) string {
	query := url.Values{"source_expires": {strconv.FormatInt(sourceExpires, 10)}, "expires": {strconv.FormatInt(expires, 10)}, "signature": {signature}}
	return fmt.Sprintf("%s/handoff/%d?%s", strings.TrimRight(config.ValueOf.Host, "/"), messageID, query.Encode())
}

func playerLaunchURL(player string, messageID int, expires int64) string {
	query := url.Values{
		"video": {strconv.Itoa(messageID)}, "expires": {strconv.FormatInt(expires, 10)},
		"signature": {utils.SignPlayerLaunch(player, messageID, expires)},
	}
	return fmt.Sprintf("%s/player/%s?%s", strings.TrimRight(config.ValueOf.Host, "/"), player, query.Encode())
}

func configuredLocation() *time.Location {
	location, err := time.LoadLocation(config.ValueOf.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}
