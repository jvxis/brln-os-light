package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadHSTSPolicy(t *testing.T) {
	for _, age := range []int{0, 300, 31536000, -1, 31536001} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := fmt.Sprintf("server:\n  tls_cert: /tmp/fixture.crt\n  tls_key: /tmp/fixture.key\n  hsts_max_age_seconds: %d\n", age)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if age < 0 || age > 31536000 {
			if err == nil {
				t.Fatalf("invalid age accepted: %d", age)
			}
			continue
		}
		if err != nil || cfg.Server.HSTSMaxAgeSeconds != age {
			t.Fatalf("valid age not preserved: %d %v", age, err)
		}
	}
	cfg, err := Load(writeMinimalConfig(t, ""))
	if err != nil || cfg.Server.HSTSMaxAgeSeconds != 0 {
		t.Fatal("legacy config must default to HSTS off")
	}
}
