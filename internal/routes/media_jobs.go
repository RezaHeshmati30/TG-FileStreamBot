package routes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"EverythingSuckz/fsb/config"
	"EverythingSuckz/fsb/internal/mediajobs"

	"github.com/gin-gonic/gin"
)

const mediaJobBodyLimit = 16 * 1024

type createMediaJobRequest struct {
	ID        string `json:"id"`
	SourceBot string `json:"sourceBot"`
	Payload   string `json:"payload"`
}

type chooseMediaJobRequest struct {
	ChoiceID string `json:"choiceId"`
}

func (e *allRoutes) LoadMediaJobs(r *Route) {
	r.Engine.POST("/api/v1/media-link-jobs", verifyCineRateBridge(), createMediaJob)
	r.Engine.GET("/api/v1/media-link-jobs/:id", verifyCineRateBridge(), getMediaJob)
	r.Engine.POST("/api/v1/media-link-jobs/:id/choices", verifyCineRateBridge(), chooseMediaJob)
	r.Engine.DELETE("/api/v1/media-link-jobs/:id", verifyCineRateBridge(), cancelMediaJob)
}

func verifyCineRateBridge() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		secret := config.ValueOf.CineRateBridgeSecret
		if !mediajobs.Enabled() || len(secret) < 32 {
			ctx.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "media-link bridge is disabled"})
			return
		}
		timestamp := ctx.GetHeader("X-CineRate-Timestamp")
		provided := ctx.GetHeader("X-CineRate-Signature")
		unixTimestamp, err := strconv.ParseInt(timestamp, 10, 64)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid bridge timestamp"})
			return
		}
		requestTime := time.Unix(unixTimestamp, 0)
		if delta := time.Since(requestTime); delta > 5*time.Minute || delta < -5*time.Minute {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "expired bridge request"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(ctx.Request.Body, mediaJobBodyLimit+1))
		if err != nil || len(body) > mediaJobBodyLimit {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ctx.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(ctx.Request.Method + "\n" + ctx.Request.URL.Path + "\n" + timestamp + "\n" + string(body)))
		expected := mac.Sum(nil)
		actual, decodeErr := hex.DecodeString(provided)
		if decodeErr != nil || !hmac.Equal(actual, expected) {
			ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid bridge signature"})
			return
		}
		ctx.Next()
	}
}

func createMediaJob(ctx *gin.Context) {
	var request createMediaJobRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid media-link job"})
		return
	}
	job, err := mediajobs.Default.Create(request.ID, request.SourceBot, request.Payload)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusAccepted, job)
}

func getMediaJob(ctx *gin.Context) {
	job, ok := mediajobs.Default.Get(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "media-link job was not found"})
		return
	}
	ctx.JSON(http.StatusOK, job)
}

func chooseMediaJob(ctx *gin.Context) {
	var request chooseMediaJobRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid media-link choice"})
		return
	}
	job, err := mediajobs.Default.Choose(ctx.Param("id"), request.ChoiceID)
	if err != nil {
		ctx.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, job)
}

func cancelMediaJob(ctx *gin.Context) {
	job, ok := mediajobs.Default.Cancel(ctx.Param("id"))
	if !ok {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "media-link job was not found"})
		return
	}
	ctx.JSON(http.StatusOK, job)
}
