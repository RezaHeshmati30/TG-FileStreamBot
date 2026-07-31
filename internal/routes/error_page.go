package routes

import (
	"html/template"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type streamErrorPageData struct {
	Status  int
	Title   string
	Message string
}

var streamErrorPage = template.Must(template.New("stream-error").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex, nofollow">
  <title>{{.Title}} · File Stream</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px; color: #e8edf7; background: radial-gradient(circle at 50% 0%, #1d3154 0, #0c1424 42%, #070b13 100%); }
    main { width: min(100%, 520px); padding: 42px 36px 36px; text-align: center; border: 1px solid rgba(148, 163, 184, .18); border-radius: 24px; background: rgba(15, 23, 42, .86); box-shadow: 0 24px 70px rgba(0, 0, 0, .38); backdrop-filter: blur(12px); }
    .icon { width: 72px; height: 72px; margin: 0 auto 24px; display: grid; place-items: center; border-radius: 22px; color: #fda4af; background: rgba(244, 63, 94, .12); border: 1px solid rgba(251, 113, 133, .22); }
    svg { width: 34px; height: 34px; fill: none; stroke: currentColor; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
    .status { display: inline-block; margin-bottom: 14px; color: #93c5fd; font-size: 12px; font-weight: 750; letter-spacing: .13em; text-transform: uppercase; }
    h1 { margin: 0; color: #f8fafc; font-size: clamp(26px, 6vw, 34px); line-height: 1.15; letter-spacing: -.025em; }
    p { margin: 16px auto 0; max-width: 400px; color: #aebbd0; font-size: 16px; line-height: 1.65; }
    .hint { margin-top: 28px; padding-top: 22px; border-top: 1px solid rgba(148, 163, 184, .14); color: #7f8da5; font-size: 13px; line-height: 1.5; }
  </style>
</head>
<body>
  <main>
    <div class="icon" aria-hidden="true">
      <svg viewBox="0 0 24 24"><path d="M10.6 13.4a4 4 0 0 0 5.7.1l2.8-2.8a4 4 0 0 0-5.7-5.7l-1.6 1.6"/><path d="M13.4 10.6a4 4 0 0 0-5.7-.1l-2.8 2.8A4 4 0 0 0 10.6 19l1.6-1.6"/><path d="m4 4 16 16"/></svg>
    </div>
    <div class="status">Error {{.Status}}</div>
    <h1>{{.Title}}</h1>
    <p>{{.Message}}</p>
    <div class="hint">Please request a new link from the person or bot that shared this file.</div>
  </main>
</body>
</html>`))

func renderStreamError(ctx *gin.Context, status int, title, message string) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	ctx.Status(status)
	if ctx.Request.Method == http.MethodHead {
		return
	}
	if err := streamErrorPage.Execute(ctx.Writer, streamErrorPageData{Status: status, Title: title, Message: message}); err != nil {
		log.Error("Failed to render stream error page", zap.Error(err))
	}
}
