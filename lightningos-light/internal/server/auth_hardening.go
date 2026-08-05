package server

import (
	"errors"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const authRequestBodyMaxBytes int64 = 16 * 1024

type authRateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

type authRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*authRateBucket
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{buckets: make(map[string]*authRateBucket)}
}

func (l *authRateLimiter) allow(key string, now time.Time, burst float64, refillPerSecond float64) (bool, time.Duration) {
	if l == nil || burst <= 0 || refillPerSecond <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &authRateBucket{tokens: burst, updated: now, lastSeen: now}
		l.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.updated).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(burst, bucket.tokens+elapsed*refillPerSecond)
		bucket.updated = now
	}
	bucket.lastSeen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}

	retry := time.Duration(math.Ceil((1-bucket.tokens)/refillPerSecond)) * time.Second
	if retry < time.Second {
		retry = time.Second
	}
	return false, retry
}

func (a *AuthService) allowAuthRequest(w http.ResponseWriter, r *http.Request, kind string) bool {
	if a == nil {
		return true
	}
	if a.limiter == nil {
		a.limiter = newAuthRateLimiter()
	}
	now := a.currentTime()
	clientAllowed, clientRetry := a.limiter.allow("client:"+kind+":"+authClientIP(r), now, 5, 1.0/10.0)
	globalAllowed, globalRetry := a.limiter.allow("global:"+kind, now, 30, 2)
	if clientAllowed && globalAllowed {
		return true
	}
	retry := clientRetry
	if globalRetry > retry {
		retry = globalRetry
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
	writeErrorCode(w, http.StatusTooManyRequests, "auth_rate_limited", "too many authentication attempts; try again later")
	return false
}

func authClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if value := strings.TrimSpace(r.RemoteAddr); value != "" {
		return value
	}
	return "unknown"
}

func readAuthJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r == nil || r.Body == nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, authRequestBodyMaxBytes)
	if err := readJSON(r, dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeErrorCode(w, http.StatusRequestEntityTooLarge, "auth_request_too_large", "authentication request is too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}
