package routes

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/secureproxy"
	"EverythingSuckz/fsb/internal/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var (
	proxySlots  chan struct{}
	proxyClient *http.Client
)

var proxyResponseHeaders = []string{
	"Accept-Ranges",
	"Content-Disposition",
	"Content-Length",
	"Content-Range",
	"Content-Type",
	"ETag",
	"Last-Modified",
}

func (e *allRoutes) LoadProxy(r *Route) {
	proxySlots = make(chan struct{}, config.ValueOf.ProxyConcurrency)
	proxyClient = secureproxy.NewHTTPClient(time.Duration(config.ValueOf.ProxyHeaderTimeoutSec) * time.Second)
	r.Engine.GET("/proxy/:token", getSecureProxyRoute)
	r.Engine.HEAD("/proxy/:token", getSecureProxyRoute)
	r.Engine.GET("/proxy-player/:player/:token", getSecureProxyPlayerRoute)
	e.log.Named("Proxy").Info("Loaded secure proxy routes", zap.Int("concurrency", config.ValueOf.ProxyConcurrency))
}

func getSecureProxyRoute(ctx *gin.Context) {
	target, err := secureproxy.DecryptTarget(ctx.Param("token"), time.Now())
	if err != nil {
		renderProxyTokenError(ctx, err)
		return
	}
	parsed, err := secureproxy.ParseAndValidateURL(target.URL)
	if err != nil {
		renderStreamError(ctx, http.StatusForbidden, "Unsafe destination", "The destination of this secure stream link is not allowed.")
		return
	}
	select {
	case proxySlots <- struct{}{}:
		defer func() { <-proxySlots }()
	default:
		renderStreamError(ctx, http.StatusTooManyRequests, "Proxy is busy", "Too many secure streams are active. Please try again shortly.")
		return
	}

	request, err := http.NewRequestWithContext(ctx.Request.Context(), ctx.Request.Method, parsed.String(), nil)
	if err != nil {
		renderStreamError(ctx, http.StatusBadRequest, "Invalid destination", "The destination URL could not be opened.")
		return
	}
	for _, name := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
		if value := ctx.GetHeader(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", "TG-FileStreamBot-SecureProxy/1.0")
	response, err := proxyClient.Do(request)
	if err != nil {
		log.Warn("Secure proxy upstream request failed", zap.String("host", parsed.Hostname()), zap.Error(err))
		renderStreamError(ctx, http.StatusBadGateway, "Source unavailable", "The external source could not be reached or is not allowed.")
		return
	}
	defer response.Body.Close()
	for _, name := range proxyResponseHeaders {
		if value := response.Header.Get(name); value != "" {
			ctx.Header(name, value)
		}
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	if ctx.Query("d") == "true" && response.Header.Get("Content-Disposition") == "" {
		ctx.Header("Content-Disposition", "attachment")
	}
	ctx.Status(response.StatusCode)
	if ctx.Request.Method == http.MethodHead || response.StatusCode == http.StatusNotModified || response.StatusCode == http.StatusNoContent {
		return
	}
	buffer := make([]byte, 256*1024)
	if _, err := io.CopyBuffer(ctx.Writer, response.Body, buffer); err != nil && !utils.IsClientDisconnectError(err) {
		log.Warn("Secure proxy stream ended with an error", zap.String("host", parsed.Hostname()), zap.Error(err))
	}
}

func getSecureProxyPlayerRoute(ctx *gin.Context) {
	playerID := ctx.Param("player")
	player, supported := externalPlayers[playerID]
	if !supported {
		renderStreamError(ctx, http.StatusBadRequest, "Unsupported player", "The selected external player is not supported.")
		return
	}
	token := ctx.Param("token")
	target, err := secureproxy.DecryptTarget(token, time.Now())
	if err != nil {
		renderProxyTokenError(ctx, err)
		return
	}
	parsed, err := secureproxy.ParseAndValidateURL(target.URL)
	if err != nil {
		renderStreamError(ctx, http.StatusForbidden, "Unsafe destination", "The destination of this secure stream link is not allowed.")
		return
	}
	proxyURL := strings.TrimRight(config.ValueOf.Host, "/") + "/proxy/" + token
	deepLink := player.DeepLink(proxyURL, secureproxy.DisplayName(parsed))
	ctx.Header("Cache-Control", "no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	ctx.Header("Referrer-Policy", "no-referrer")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Type", "text/html; charset=utf-8")
	if err := playerPage.Execute(ctx.Writer, playerPageData{
		AppName: player.Name, Icon: player.Icon, ButtonText: player.ButtonText,
		DeepLink: deepLink, Title: secureproxy.DisplayName(parsed),
	}); err != nil {
		log.Error("Failed to render secure proxy player page", zap.String("player", playerID), zap.Error(err))
	}
}

func renderProxyTokenError(ctx *gin.Context, err error) {
	if errors.Is(err, secureproxy.ErrExpiredToken) {
		renderStreamError(ctx, http.StatusGone, "Secure link expired", "This secure stream link has reached the end of its 24-hour validity period.")
		return
	}
	renderStreamError(ctx, http.StatusForbidden, "Invalid secure link", "This secure stream link is invalid or has been modified.")
}
