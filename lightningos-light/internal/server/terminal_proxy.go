package server

import (
  "encoding/base64"
  "net/http"
  "net/http/httputil"
  "net/url"
  "os"
  "strings"
)

const terminalProxyPrefix = "/terminal"

func terminalProxyTarget() *url.URL {
  port := strings.TrimSpace(os.Getenv("TERMINAL_PORT"))
  if port == "" {
    port = "7681"
  }
  target, _ := url.Parse("http://127.0.0.1:" + port)
  return target
}

func terminalCredential() string {
  return strings.TrimSpace(os.Getenv("TERMINAL_CREDENTIAL"))
}

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
  if strings.TrimSpace(os.Getenv("TERMINAL_ENABLED")) != "1" {
    http.NotFound(w, r)
    return
  }

  if r.URL.Path == terminalProxyPrefix {
    http.Redirect(w, r, terminalProxyPrefix+"/", http.StatusTemporaryRedirect)
    return
  }

  target := terminalProxyTarget()
  authHeader := basicAuthHeader(terminalCredential())
  proxy := httputil.NewSingleHostReverseProxy(target)
  proxy.Director = func(req *http.Request) {
    req.URL.Scheme = target.Scheme
    req.URL.Host = target.Host
    req.URL.Path = strings.TrimPrefix(req.URL.Path, terminalProxyPrefix)
    if req.URL.Path == "" {
      req.URL.Path = "/"
    }
    req.Host = target.Host
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
