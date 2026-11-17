package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeClock is a manually advanced clock so tests never sleep.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (f *fakeClock) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

// newTestRouter builds a router with no trusted proxies (as in app.go) plus the limiter.
func newTestRouter(rl *RateLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		panic(err)
	}
	r.Use(rl.Middleware())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.POST("/users/login", ok)
	r.POST("/users/refresh", ok)
	r.GET("/documents/x", ok)
	return r
}

// do sends one request from the given socket IP, optionally with an X-Forwarded-For header.
func do(r *gin.Engine, method, path, remoteIP, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remoteIP + ":12345"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAllowUpToBurstThenDenied(t *testing.T) {
	l := newLimiter(authRatePerMinute, authBurst, newFakeClock().now)
	for i := 0; i < authBurst; i++ {
		if ok, _ := l.allow("1.1.1.1"); !ok {
			t.Fatalf("call %d should be allowed", i+1)
		}
	}
	if ok, _ := l.allow("1.1.1.1"); ok {
		t.Fatal("call after burst should be denied")
	}
}

func TestRefillAfterTime(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(authRatePerMinute, authBurst, clk.now)
	for i := 0; i < authBurst; i++ {
		l.allow("1.1.1.1")
	}
	clk.advance(12 * time.Second) // 5/min = 1 token per 12 s
	if ok, _ := l.allow("1.1.1.1"); !ok {
		t.Fatal("one token should have refilled")
	}
	if ok, _ := l.allow("1.1.1.1"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestRefillCapsAtBurst(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(authRatePerMinute, authBurst, clk.now)
	l.allow("1.1.1.1")
	clk.advance(time.Hour)
	allowed := 0
	for i := 0; i < authBurst+3; i++ {
		if ok, _ := l.allow("1.1.1.1"); ok {
			allowed++
		}
	}
	if allowed != authBurst {
		t.Fatalf("allowed %d after long idle, want burst %d", allowed, authBurst)
	}
}

func TestSeparateBucketsPerIP(t *testing.T) {
	l := newLimiter(authRatePerMinute, authBurst, newFakeClock().now)
	for i := 0; i < authBurst+1; i++ {
		l.allow("1.1.1.1")
	}
	if ok, _ := l.allow("2.2.2.2"); !ok {
		t.Fatal("a different IP must have its own bucket")
	}
}

func TestRetryAfterValue(t *testing.T) {
	l := newLimiter(authRatePerMinute, authBurst, newFakeClock().now)
	for i := 0; i < authBurst; i++ {
		l.allow("1.1.1.1")
	}
	ok, wait := l.allow("1.1.1.1")
	if ok {
		t.Fatal("expected denial")
	}
	if wait < 11*time.Second || wait > 12*time.Second {
		t.Fatalf("wait = %v, want about 12s", wait)
	}
}

func TestSweepRemovesIdleEntries(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(generalRatePerMinute, generalBurst, clk.now)
	l.allow("old")
	clk.advance(idleTTL + time.Second)
	l.allow("fresh")
	l.sweep()
	if _, ok := l.buckets["old"]; ok {
		t.Fatal("idle entry should be evicted")
	}
	if _, ok := l.buckets["fresh"]; !ok {
		t.Fatal("recent entry must be kept")
	}
}

// fill adds n distinct IPs to the limiter.
func fill(l *limiter, n int) {
	for i := 0; i < n; i++ {
		l.allow(fmt.Sprintf("ip-%d", i))
	}
}

func TestMapCapRejectsNewIPWhenFull(t *testing.T) {
	l := newLimiter(generalRatePerMinute, generalBurst, newFakeClock().now)
	fill(l, maxEntries)
	ok, wait := l.allow("brand-new")
	if ok {
		t.Fatal("new IP must be refused when the map is full of active entries")
	}
	if wait != fullMapRetryAfter {
		t.Fatalf("wait = %v, want %v", wait, fullMapRetryAfter)
	}
	if len(l.buckets) != maxEntries {
		t.Fatalf("map grew to %d", len(l.buckets))
	}
	if ok, _ := l.allow("ip-0"); !ok {
		t.Fatal("already tracked IPs must keep working")
	}
}

func TestMapCapSweepsBeforeRejecting(t *testing.T) {
	clk := newFakeClock()
	l := newLimiter(generalRatePerMinute, generalBurst, clk.now)
	fill(l, maxEntries)
	clk.advance(idleTTL + time.Second)
	if ok, _ := l.allow("brand-new"); !ok {
		t.Fatal("expired entries should be swept to make room")
	}
}

func TestConcurrentAllow(t *testing.T) {
	// Real clock is fine: 100 requests finish in microseconds, refill is ~0.
	l := newLimiter(authRatePerMinute, authBurst, time.Now)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.allow("1.1.1.1"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != authBurst {
		t.Fatalf("allowed %d, want exactly %d", allowed, authBurst)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	rl := NewRateLimiter()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rl.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestMiddlewareAuthPathLimited(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < authBurst; i++ {
		if w := do(r, "POST", "/users/login", "9.9.9.9", ""); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	if w := do(r, "POST", "/users/login", "9.9.9.9", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request = %d, want 429", w.Code)
	}
}

func TestMiddlewareGeneralBucketIndependent(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < authBurst+2; i++ {
		do(r, "POST", "/users/login", "9.9.9.9", "")
	}
	if w := do(r, "GET", "/documents/x", "9.9.9.9", ""); w.Code != http.StatusOK {
		t.Fatalf("general route = %d, want 200 while auth bucket is empty", w.Code)
	}
}

func TestMiddlewareGeneralLimit(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < generalBurst; i++ {
		do(r, "GET", "/documents/x", "9.9.9.9", "")
	}
	if w := do(r, "GET", "/documents/x", "9.9.9.9", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429 after the general burst", w.Code)
	}
	// Unknown routes (404) count against the general bucket too.
	if w := do(r, "GET", "/nope", "9.9.9.9", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("404 path got %d, want 429", w.Code)
	}
}

func TestMiddlewareRefreshPathUsesAuthBucket(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < authBurst; i++ {
		do(r, "POST", "/users/refresh", "9.9.9.9", "")
	}
	if w := do(r, "POST", "/users/refresh", "9.9.9.9", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", w.Code)
	}
}

func TestMiddleware429BodyAndHeader(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < authBurst; i++ {
		do(r, "POST", "/users/login", "9.9.9.9", "")
	}
	w := do(r, "POST", "/users/login", "9.9.9.9", "")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d", w.Code)
	}
	want := `{"error":"too many requests, please try again later"}`
	if got := strings.TrimSpace(w.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
	secs, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || secs < 1 || secs > 12 {
		t.Fatalf("Retry-After = %q, want integer in [1,12]", w.Header().Get("Retry-After"))
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestMiddlewareRejectedRequestSkipsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := newRateLimiter(newFakeClock().now)
	calls := 0
	r := gin.New()
	r.Use(rl.Middleware())
	r.POST("/users/login", func(c *gin.Context) { calls++ })
	for i := 0; i < authBurst+3; i++ {
		do(r, "POST", "/users/login", "9.9.9.9", "")
	}
	if calls != authBurst {
		t.Fatalf("handler ran %d times, want %d (rejected requests must never reach it)", calls, authBurst)
	}
}

// Verifies Gin v1.11 behaviour: with SetTrustedProxies(nil), X-Forwarded-For is ignored
// and the socket IP is used, so changing the header does not give a fresh bucket.
func TestXForwardedForSpoofIgnored(t *testing.T) {
	r := newTestRouter(newRateLimiter(newFakeClock().now))
	for i := 0; i < authBurst; i++ {
		do(r, "POST", "/users/login", "9.9.9.9", fmt.Sprintf("1.2.3.%d", i))
	}
	if w := do(r, "POST", "/users/login", "9.9.9.9", "1.2.3.200"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF got %d, want 429", w.Code)
	}
}

func TestClientIPIsSocketIPWithoutTrustedProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatal(err)
	}
	var got string
	r.GET("/", func(c *gin.Context) { got = c.ClientIP() })
	do(r, "GET", "/", "9.9.9.9", "6.6.6.6")
	if got != "9.9.9.9" {
		t.Fatalf("ClientIP = %q, want socket IP 9.9.9.9", got)
	}
}
