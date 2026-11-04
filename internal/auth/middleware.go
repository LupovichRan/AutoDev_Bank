package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// CookieName is the session cookie.
const CookieName = "bank_session"

type ctxKey struct{}

// FromContext returns the session attached by Authenticate.
func FromContext(ctx context.Context) (Session, bool) {
	sess, ok := ctx.Value(ctxKey{}).(Session)
	return sess, ok
}

// WithSession returns a copy of ctx carrying sess.
func WithSession(ctx context.Context, sess Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, sess)
}

// Authenticate attaches the session named by the bank_session cookie when it
// is valid. It never fails the request: missing, unknown or expired
// sessions simply leave the context without one (RequireSession decides).
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if ck, err := r.Cookie(CookieName); err == nil && ck.Value != "" {
			sess, err := s.LoadSession(r.Context(), ck.Value)
			if err == nil {
				r = r.WithContext(WithSession(r.Context(), sess))
			} else if !errors.Is(err, ErrNoSession) {
				s.log.WarnContext(r.Context(), "auth: load session", "err", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSession rejects requests without a session with 401.
func (s *Service) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			problem(w, http.StatusUnauthorized, "sign in required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects requests whose principal lacks role: 401 without a
// session, 403 with one.
func (s *Service) RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, _ := FromContext(r.Context())
			ok, err := s.HasRole(r.Context(), sess, role)
			if err != nil {
				s.log.ErrorContext(r.Context(), "auth: role check", "err", err)
				problem(w, http.StatusInternalServerError, "internal error")
				return
			}
			if !ok {
				problem(w, http.StatusForbidden, "missing role "+role)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// HasRole reports whether the session's principal holds role.
func (s *Service) HasRole(ctx context.Context, sess Session, role string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `select exists(select 1 from control.roles where principal_id=$1 and role=$2)`,
		sess.PrincipalID, role).Scan(&ok)
	return ok, err
}

// rateLimiter is an in-memory token bucket per client IP: `rate` tokens
// refill evenly over `per`, with a burst of `rate`.
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

func newRateLimiter(rate int, per time.Duration) *rateLimiter {
	return &rateLimiter{rate: float64(rate), per: per, buckets: map[string]*bucket{}, now: time.Now}
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

// rateLimit applies the per-IP ceremony budget (429 when exhausted).
func (s *Service) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(clientIP(r)) {
			w.Header().Set("Retry-After", "60")
			problem(w, http.StatusTooManyRequests, "too many authentication attempts; try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}
