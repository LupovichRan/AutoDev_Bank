package httpapi

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// defaultPublicRatePerMinute is the per-IP budget for the anonymous
// transparency and open-data endpoints.
const defaultPublicRatePerMinute = 60

// maxBuckets caps the limiter's memory under an address sweep.
const maxBuckets = 10_000

// rateLimiter is an in-memory token bucket per client IP: rate tokens refill
// evenly over per, with a burst of rate. At most maxBuckets keys are
// tracked; past that the least recently seen key is evicted. (A copy of the
// limiter in internal/auth, kept separate so the two budgets are independent.)
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64
	per     time.Duration
	buckets map[string]*bucket
	now     func() time.Time
	lastGC  time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate int, per time.Duration, now func() time.Time) *rateLimiter {
	return &rateLimiter{rate: float64(rate), per: per, buckets: map[string]*bucket{}, now: now}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastGC) > l.per {
		// Buckets idle for a full period are full again; drop them.
		for k, b := range l.buckets {
			if now.Sub(b.last) > l.per {
				delete(l.buckets, k)
			}
		}
		l.lastGC = now
	}
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.evictOldest()
		}
		b = &bucket{tokens: l.rate, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate / l.per.Seconds()
	if b.tokens > l.rate {
		b.tokens = l.rate
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *rateLimiter) evictOldest() {
	var (
		oldestKey string
		oldest    time.Time
		found     bool
	)
	for k, b := range l.buckets {
		if !found || b.last.Before(oldest) {
			oldestKey, oldest, found = k, b.last, true
		}
	}
	if found {
		delete(l.buckets, oldestKey)
	}
}

// clientIP is the TCP peer address without the port. X-Forwarded-For is not
// trusted: the server is expected to face clients directly.
func clientIP(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		addr = addr[:i]
	}
	return strings.Trim(addr, "[]")
}

// isPublicDataPath reports whether path is one of the anonymous, throttled
// endpoints.
func isPublicDataPath(path string) bool {
	return strings.HasPrefix(path, "/api/transparency/") || strings.HasPrefix(path, "/api/opendata/")
}

// publicRateLimit throttles /api/transparency/* and /api/opendata/* per IP
// (429 problem when exhausted).
func publicRateLimit(l *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicDataPath(r.URL.Path) && !l.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			Write(w, r, http.StatusTooManyRequests, "", "rate_limited", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
