package server

import (
  "bufio"
  "net"
  "net/http"
  "time"
)

func (s *Server) requestLogger() func(http.Handler) http.Handler {
  return func(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
      start := time.Now()
      ww := &responseWriter{ResponseWriter: w, status: 200}

      next.ServeHTTP(ww, r)

      duration := time.Since(start)
      s.logger.Printf("method=%s path=%s status=%d duration_ms=%d", r.Method, r.URL.Path, ww.status, duration.Milliseconds())
    })
  }
}

type responseWriter struct {
  http.ResponseWriter
  status int
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
  return w.ResponseWriter
}

func (w *responseWriter) WriteHeader(status int) {
  w.status = status
  w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Flush() {
  if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
    flusher.Flush()
  }
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
  if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
    return hijacker.Hijack()
  }
  return nil, nil, http.ErrNotSupported
}
