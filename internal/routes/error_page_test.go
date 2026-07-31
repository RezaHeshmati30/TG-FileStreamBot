package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRenderStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/stream/42?signature=must-not-appear", nil)

	renderStreamError(ctx, http.StatusGone, "Link expired", "This link is no longer available.")

	if recorder.Code != http.StatusGone {
		t.Fatalf("expected status %d, got %d", http.StatusGone, recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Link expired") {
		t.Fatalf("expected error title in response body")
	}
	if strings.Contains(recorder.Body.String(), "must-not-appear") {
		t.Fatalf("response contains the request signature")
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected no-store cache policy")
	}
}
