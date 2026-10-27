package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAAssetsAndFallback(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"index.html": "<html>app</html>", "app.js": "console.log('asset')"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler := (&Server{}).handleSPA(dir)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/", "<html>app</html>"},
		{"GET", "/app.js", "console.log('asset')"},
		{"HEAD", "/app.js", ""},
		{"GET", "/wallet", "<html>app</html>"},
		{"GET", "/missing.js", "<html>app</html>"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != http.StatusOK || w.Body.String() != tc.body {
				t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
			}
		})
	}
}
