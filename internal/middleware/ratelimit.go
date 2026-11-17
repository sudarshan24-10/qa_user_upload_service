package middleware

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Hard-coded on purpose (decision: simple, per client IP, not configurable).
const (
	// Auth routes run bcrypt (~300 ms CPU each), so keep the budget tiny.
	authRatePerMinute = 5
	authBurst         = 5
	// Everything else: far above normal reader use, still stops floods.
	generalRatePerMinute = 100
	generalBurst         = 30
	// An IP unseen for this long is dropped. It is much longer than the time a bucket
	// needs to refill, so a dropped entry behaves exactly like a brand-new one.
	idleTTL = 10 * time.Minute
	// Hard cap on tracked IPs per group so memory stays bounded (~1-2 MB).
	maxEntries = 10000
	// How often the cleanup goroutine sweeps idle entries.
	cleanupInterval = time.Minute
	// Retry-After sent when the map is full and a new IP cannot be tracked.
	fullMapRetryAfter = 60 * time.Second
)

// authPaths are the routes that do bcrypt/token work and get the strict bucket.
var authPaths = map[string]bool{
	"/users/register": true,
	"/users/login":    true,
	"/users/refresh":  true, // future route (item 2B)
}

// bucket is one IP's token bucket: tokens left and when it was last refilled.
type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a token-bucket store keyed by client IP. One mutex guards the map; a
// request holds it only for a few map operations, so contention is negligible.
type limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens added per second
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time // injected so tests control time without sleeping
}

func newLimiter(perMinute, burst int, now func() time.Time) *limiter {
	return &limiter{
		rate:    float64(perMinute) / 60,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     now,
	}
}

// allow spends one token for ip. When none is left it returns false plus how long
// until one is available (used for the Retry-After header).
func (l *limiter) allow(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()

	b, ok := l.buckets[ip]
	if !ok {
		// New IP. If the map is full, free expired entries first; if it is still full,
		// refuse (fail closed). Failing open would let a flood bypass the limiter.
		if len(l.buckets) >= maxEntries {
			l.sweepLocked(t)
			if len(l.buckets) >= maxEntries {
				return false, fullMapRetryAfter
			}
		}
		b = &bucket{tokens: l.burst, last: t}
		l.buckets[ip] = b
	} else {
		// Refill for the time since this IP was last seen, capped at the burst size.
		b.tokens = math.Min(l.burst, b.tokens+t.Sub(b.last).Seconds()*l.rate)
		b.last = t
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweepLocked deletes entries idle longer than idleTTL. The caller holds l.mu.
func (l *limiter) sweepLocked(t time.Time) {
	for ip, b := range l.buckets {
		if t.Sub(b.last) > idleTTL {
			delete(l.buckets, ip)
		}
	}
}

func (l *limiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(l.now())
}

// RateLimiter holds the two bucket groups: auth routes and everything else.
type RateLimiter struct {
	auth    *limiter
	general *limiter
}

func NewRateLimiter() *RateLimiter {
	return newRateLimiter(time.Now)
}

func newRateLimiter(now func() time.Time) *RateLimiter {
	return &RateLimiter{
		auth:    newLimiter(authRatePerMinute, authBurst, now),
		general: newLimiter(generalRatePerMinute, generalBurst, now),
	}
}

// Run evicts idle IPs until ctx is cancelled. It is started from app.Run like the
// background workers, so it stops cleanly on shutdown.
func (r *RateLimiter) Run(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.auth.sweep()
			r.general.sweep()
		}
	}
}

// Middleware must be registered before any handler or auth work, so a rejected
// request never reaches bcrypt.
func (r *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// URL.Path (not FullPath) so unknown routes (404) are limited too.
		l := r.general
		if authPaths[c.Request.URL.Path] {
			l = r.auth
		}

		// ClientIP() is the socket IP because the router trusts no proxy (see app.go).
		ok, wait := l.allow(c.ClientIP())
		if !ok {
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			// Abort so no later middleware or handler runs.
			c.AbortWithStatusJSON(http.StatusTooManyRequests,
				gin.H{"error": "too many requests, please try again later"})
			return
		}
		c.Next()
	}
}
