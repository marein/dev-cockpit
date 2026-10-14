package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// The answer is the clock at the moment of the request and nothing may keep
// it, a cached one would hand every page the offset of the first.
func TestServerTimeAnswersTheClockUncached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/time", (&Server{}).handleServerTime)

	before := time.Now().UnixMilli()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/time", nil))
	after := time.Now().UnixMilli()

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("expected no-store, got %q", cache)
	}
	var body struct {
		Now int64 `json:"now"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	if body.Now < before || body.Now > after {
		t.Fatalf("now %d is outside the request, %d to %d", body.Now, before, after)
	}
}
