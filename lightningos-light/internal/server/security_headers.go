package server

import (
	"net/http"
	"strings"
)

func securityHeadersMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "SAMEORIGIN")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=(), payment=(), usb=()")
			if strings.HasPrefix(r.URL.Path, "/api/auth/") {
				setNoStore(w)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
