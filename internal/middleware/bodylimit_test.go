package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// bodyRouter echoes how many bytes the handler could read, and whether reading failed.
func bodyRouter(limit int64, handlerCalls *int) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaxBody(limit))
	r.POST("/x", func(c *gin.Context) {
		*handlerCalls++
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		c.String(http.StatusOK, "ok")
	})
	return r
}

func TestMaxBodyDeclaredTooLarge(t *testing.T) {
	calls := 0
	r := bodyRouter(10, &calls)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 11)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"error":"request body too large"}` {
		t.Fatalf("body = %s", got)
	}
	if calls != 0 {
		t.Fatal("handler must not run for a declared oversized body")
	}
}

func TestMaxBodyChunkedTooLarge(t *testing.T) {
	calls := 0
	r := bodyRouter(10, &calls)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 50)))
	req.ContentLength = -1 // unknown length, as with chunked transfer encoding
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400 from the handler's read/bind failure", w.Code)
	}
	if calls != 1 {
		t.Fatal("handler should run and see the read error")
	}
}

func TestMaxBodyUnderLimitPasses(t *testing.T) {
	calls := 0
	r := bodyRouter(10, &calls)
	req := httptest.NewRequest("POST", "/x", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", w.Code)
	}
}

func TestMaxBodyExactlyAtLimitPasses(t *testing.T) {
	calls := 0
	r := bodyRouter(10, &calls)
	req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 10)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 at exactly the limit", w.Code)
	}
}
