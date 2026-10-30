package server

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/sync/semaphore"
)

const requestBodyReadTimeout = 30 * time.Second

func requestBodyLimit(path string) int64 {
	switch {
	case strings.HasPrefix(path, "/api/auth/"):
		return authRequestBodyMaxBytes
	case path == "/api/lnops/channel/scb/restore":
		return 16 << 20
	case path == "/api/financial/intents":
		// Preserve the existing 16 MiB SCB request inside the intent envelope.
		return (16 << 20) + (64 << 10)
	default:
		return 1 << 20
	}
}

// Validate the complete bounded body before any handler can perform a mutation.
// Reading only the first JSON value would allow an arbitrarily large suffix.
// The memory budget also bounds concurrent slow uploads, independently of auth.
func requestBodyMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	budget := semaphore.NewWeighted(32 << 20)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body == nil || r.Body == http.NoBody {
				next.ServeHTTP(w, r)
				return
			}
			limit := requestBodyLimit(r.URL.Path)
			rejectBody := func() {
				if r.ProtoMajor == 1 {
					w.Header().Set("Connection", "close")
				}
			}
			tooLarge := func() {
				rejectBody()
				code := "request_too_large"
				if strings.HasPrefix(r.URL.Path, "/api/auth/") {
					code = "auth_request_too_large"
				}
				writeErrorCode(w, http.StatusRequestEntityTooLarge, code, "request body is too large")
			}
			if r.ContentLength > limit {
				tooLarge()
				return
			}
			if !budget.TryAcquire(limit + 1) {
				rejectBody()
				w.Header().Set("Retry-After", "1")
				writeErrorCode(w, http.StatusServiceUnavailable, "request_capacity_exceeded", "too many uploads; try again later")
				return
			}
			// Hold the reservation while the handler owns the buffered body, but
			// avoid applying the read deadline to its response or an SSE stream.
			defer budget.Release(limit + 1)
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(timeout))
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
			if err != nil {
				rejectBody()
				var large *http.MaxBytesError
				var network net.Error
				switch {
				case errors.As(err, &large):
					tooLarge()
				case errors.As(err, &network) && network.Timeout():
					writeErrorCode(w, http.StatusRequestTimeout, "request_read_timeout", "request body read timed out")
				default:
					writeError(w, http.StatusBadRequest, "could not read request body")
				}
				return
			}
			_ = controller.SetReadDeadline(time.Time{})
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}
