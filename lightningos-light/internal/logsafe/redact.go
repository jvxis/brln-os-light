// Package logsafe removes credential material from core diagnostics. It is not
// an authorization layer and is never applied to intentional auth responses.
package logsafe

import (
	"io"
	"regexp"
	"strings"
	"sync"
)

var (
	privateMaterial = regexp.MustCompile(`(?i)(BEGIN[^\r\n]*PRIVATE KEY|\b(?:seed[_ -]?(?:words|phrase)|mnemonic|private[_ -]?key)["']?\s*[:=])`)
	urlUserinfo     = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/]+@`)
	jsonSecret      = regexp.MustCompile(`(?i)("[^"\r\n]*(?:password|passwd|token|secret|macaroon|credential|rpc[_.-]?(?:user|pass|auth)|cookie|adminpw|api[_.-]?key|auth[_.-]?key)[^"\r\n]*"\s*:\s*)"(?:\\.|[^"\\])*"`)
	keyValueSecret  = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:password|passwd|token|secret|macaroon|credential|rpc[_.-]?(?:user|pass|auth)|adminpw|api[_.-]?key|auth[_.-]?key)[a-z0-9_.-]*|pass|user)(\s*[=:]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;]+)`)
	accessToken     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[a-z0-9._~+/=-]+`)
	telegramToken   = regexp.MustCompile(`\b(?:bot)?[0-9]{6,}:[A-Za-z0-9_-]{20,}\b`)
	cookieHeader    = regexp.MustCompile(`(?i)\b((?:set-cookie|cookie)\s*[:=]\s*)[^\r\n]+`)
)

func Redact(message string) string {
	if privateMaterial.MatchString(message) {
		if strings.HasSuffix(message, "\n") {
			return "[redacted private key or seed material]\n"
		}
		return "[redacted private key or seed material]"
	}
	message = urlUserinfo.ReplaceAllString(message, `${1}[redacted]@`)
	message = jsonSecret.ReplaceAllString(message, `$1"[redacted]"`)
	message = keyValueSecret.ReplaceAllString(message, `${1}${2}[redacted]`)
	message = accessToken.ReplaceAllString(message, `${1} [redacted]`)
	message = telegramToken.ReplaceAllString(message, "[redacted]")
	return cookieHeader.ReplaceAllString(message, `${1}[redacted]`)
}

func ContainsPrivateMaterial(message string) bool { return privateMaterial.MatchString(message) }

type Writer struct {
	mu     sync.Mutex
	target io.Writer
}

func NewWriter(target io.Writer) *Writer { return &Writer{target: target} }
func (w *Writer) Write(raw []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	safe := []byte(Redact(string(raw)))
	n, err := w.target.Write(safe)
	if err != nil {
		return 0, err
	}
	if n != len(safe) {
		return 0, io.ErrShortWrite
	}
	return len(raw), nil
}
