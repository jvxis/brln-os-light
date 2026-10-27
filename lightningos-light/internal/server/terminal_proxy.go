package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const terminalProxyPrefix = "/terminal"

func basicAuthHeader(credential string) string {
	if credential == "" {
		return ""
	}
	parts := strings.SplitN(credential, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	token := base64.StdEncoding.EncodeToString([]byte(credential))
	return "Basic " + token
}

func (s *Server) handleTerminalProxy(w http.ResponseWriter, r *http.Request) {
	runtime, err := readTerminalRuntimeSnapshot(terminalRuntimeEnvPath)
	if err != nil || runtime.values["TERMINAL_ENABLED"] != "1" || !terminalCredentialConfigured(runtime.values["TERMINAL_CREDENTIAL"]) {
		http.NotFound(w, r)
		return
	}

	if r.URL.Path == terminalProxyPrefix {
		http.Redirect(w, r, terminalProxyPrefix+"/", http.StatusTemporaryRedirect)
		return
	}

	setNoStore(w)
	if isWebsocketUpgrade(r) {
		ctx, cancel, ok := s.terminalStreamContext(r, runtime)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		defer cancel()
		r = r.WithContext(ctx)
	}
	port := runtime.values["TERMINAL_PORT"]
	if port == "" {
		port = "7681"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		http.NotFound(w, r)
		return
	}
	target, _ := url.Parse("http://127.0.0.1:" + port)
	authHeader := basicAuthHeader(runtime.values["TERMINAL_CREDENTIAL"])
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		req.Header.Del("Cookie")
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = strings.TrimPrefix(req.URL.Path, terminalProxyPrefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		if r.Host != "" {
			req.Host = r.Host
		} else {
			req.Host = target.Host
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		if req.Header.Get("X-Forwarded-Host") == "" && r.Host != "" {
			req.Header.Set("X-Forwarded-Host", r.Host)
		}
		if req.Header.Get("X-Forwarded-Proto") == "" {
			req.Header.Set("X-Forwarded-Proto", "https")
		}
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.logger.Printf("terminal proxy error: %v", err)
		http.Error(w, "Terminal service unavailable", http.StatusBadGateway)
	}
	proxy.ServeHTTP(w, r)
}
