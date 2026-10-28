package server

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// React/graph components set inline styles. QR workers use same-origin or blob
// URLs; shortcut icons and the DePix provider may supply external image URLs.
// None of these exceptions permits inline scripts, eval or remote scripts.
const managerContentSecurityPolicy = "default-src 'none'; base-uri 'none'; object-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https: http:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; media-src 'self' blob:; frame-src 'self'; frame-ancestors 'self'; form-action 'self'"

func securityHeadersMiddleware(hstsMaxAgeSeconds ...int) func(http.Handler) http.Handler {
	age := 0
	if len(hstsMaxAgeSeconds) > 0 && hstsMaxAgeSeconds[0] > 0 && hstsMaxAgeSeconds[0] <= 31536000 {
		age = hstsMaxAgeSeconds[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			policy := managerContentSecurityPolicy
			if origin := existingAppFrameOrigin(r.Host); origin != "" {
				// Preserve the existing Hardwallet frame on this same node's
				// dedicated HTTPS port. App contents stay outside this policy.
				policy = strings.Replace(policy, "frame-src 'self'", "frame-src 'self' "+origin, 1)
			}
			if strings.HasPrefix(r.URL.Path, "/terminal/") {
				// GoTTY 1.8's terminal renderer compiles WebAssembly. This does
				// not enable JavaScript eval or inline scripts in either page.
				policy = strings.Replace(policy, "script-src 'self'", "script-src 'self' 'wasm-unsafe-eval'", 1)
			}
			w.Header().Set("Content-Security-Policy", policy)
			// Private-CA installs must be able to bootstrap/rotate trust. HSTS is
			// opt-in and host-only, with IP access retained for recovery. Never
			// infer trusted HTTPS from client-controlled forwarding headers.
			if age > 0 && r.TLS != nil && hstsDNSHost(r.Host) {
				w.Header().Set("Strict-Transport-Security", "max-age="+strconv.Itoa(age))
			}
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

func existingAppFrameOrigin(authority string) string {
	u, err := url.Parse("https://" + authority)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) == nil && host != "localhost" && !hstsDNSHost(authority) {
		return ""
	}
	return "https://" + net.JoinHostPort(host, "4449")
}

func hstsDNSHost(authority string) bool {
	u, err := url.Parse("https://" + authority)
	if err != nil || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
