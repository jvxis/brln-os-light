package server

import (
	"errors"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const authRequestBodyMaxBytes int64 = 16 * 1024

const (
	authRateBucketRetention    = time.Hour
	authRateCleanupInterval    = 5 * time.Minute
	authRateMaxBuckets         = 4096
	authRateLimitAuditInterval = time.Minute
)

type authRateBucket struct {
	tokens    float64
	updated   time.Time
	lastSeen  time.Time
	lastAudit time.Time
}

type authRateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]*authRateBucket
	lastCleanup time.Time
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
	l.cleanupLocked(now)

	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &authRateBucket{tokens: burst, updated: now, lastSeen: now}
		l.buckets[key] = bucket
		l.cleanupLocked(now)
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

func (l *authRateLimiter) cleanupLocked(now time.Time) {
	if len(l.buckets) <= authRateMaxBuckets && !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < authRateCleanupInterval {
		return
	}
	l.lastCleanup = now
	cutoff := now.Add(-authRateBucketRetention)
	for key, bucket := range l.buckets {
		if bucket == nil || bucket.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) <= authRateMaxBuckets {
		return
	}
	type bucketAge struct {
		key      string
		lastSeen time.Time
	}
	ages := make([]bucketAge, 0, len(l.buckets))
	for key, bucket := range l.buckets {
		ages = append(ages, bucketAge{key: key, lastSeen: bucket.lastSeen})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].lastSeen.Before(ages[j].lastSeen) })
	for index := 0; index < len(ages)-authRateMaxBuckets; index++ {
		delete(l.buckets, ages[index].key)
	}
}

func (l *authRateLimiter) shouldAudit(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket == nil {
		return true
	}
	if !bucket.lastAudit.IsZero() && now.Sub(bucket.lastAudit) < authRateLimitAuditInterval {
		return false
	}
	bucket.lastAudit = now
	return true
}

func (a *AuthService) allowAuthRequest(w http.ResponseWriter, r *http.Request, kind string) bool {
	if a == nil {
		return true
	}
	if a.limiter == nil {
		a.limiter = newAuthRateLimiter()
	}
	now := a.currentTime()
	clientKey := "client:" + kind + ":" + authClientIP(r)
	clientAllowed, clientRetry := a.limiter.allow(clientKey, now, 5, 1.0/10.0)
	globalAllowed, globalRetry := a.limiter.allow("global:"+kind, now, 30, 2)
	if clientAllowed && globalAllowed {
		return true
	}
	retry := clientRetry
	if globalRetry > retry {
		retry = globalRetry
	}
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
	if a.limiter.shouldAudit(clientKey, now) {
		a.recordAudit(r, "auth.rate_limited", kind, map[string]any{"kind": kind})
	}
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
