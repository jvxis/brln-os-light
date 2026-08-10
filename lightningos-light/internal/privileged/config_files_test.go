package privileged

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPrepareEnableLoginConfig(t *testing.T) {
	input := []byte("server:\n  port: 8443\nfeatures:\n  enable_login: false\n  enable_app_store_placeholder: true\n")
	output, changed, err := prepareEnableLoginConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected config to change")
	}
	var parsed struct {
		Features struct {
			EnableLogin               bool `yaml:"enable_login"`
			EnableAppStorePlaceholder bool `yaml:"enable_app_store_placeholder"`
		} `yaml:"features"`
	}
	if err := yaml.Unmarshal(output, &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Features.EnableLogin || !parsed.Features.EnableAppStorePlaceholder {
		t.Fatalf("unexpected updated config: %s", output)
	}
}

func TestPrepareEnableLoginConfigAddsFeatures(t *testing.T) {
	output, changed, err := prepareEnableLoginConfig([]byte("server:\n  port: 8443\n"))
	if err != nil || !changed || !bytes.Contains(output, []byte("enable_login: true")) {
		t.Fatalf("output=%q changed=%v err=%v", output, changed, err)
	}
}

func TestPrepareEnableLoginConfigIsIdempotent(t *testing.T) {
	input := []byte("features:\n  enable_login: true\n")
	output, changed, err := prepareEnableLoginConfig(input)
	if err != nil || changed || !bytes.Equal(output, input) {
		t.Fatalf("output=%q changed=%v err=%v", output, changed, err)
	}
}

func TestPrepareEnableLoginConfigRejectsUnsafeShapes(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "scalar root", data: []byte("value\n")},
		{name: "features scalar", data: []byte("features: disabled\n")},
		{name: "duplicate features", data: []byte("features: {}\nfeatures: {}\n")},
		{name: "duplicate enable login", data: []byte("features:\n  enable_login: false\n  enable_login: true\n")},
		{name: "oversized", data: bytes.Repeat([]byte("x"), maxManagerConfigBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := prepareEnableLoginConfig(test.data); err == nil {
				t.Fatal("expected invalid config to fail")
			}
		})
	}
}
