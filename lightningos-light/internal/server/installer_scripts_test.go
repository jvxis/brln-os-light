package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallersConfigureManagerFirewallWithoutPrompt(t *testing.T) {
	installers := []string{"install.sh", "install_existing.sh", "install_existing_pi.sh"}
	for _, name := range installers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			if strings.Contains(string(raw), `"$MANAGER_FIREWALL_SCRIPT" --interactive`) {
				t.Fatalf("%s must accept the detected LAN CIDR without prompting", name)
			}
		})
	}
}

func TestExistingInstallersAuthorizeDetectedLNDService(t *testing.T) {
	installers := []string{"install_existing.sh", "install_existing_pi.sh"}
	for _, name := range installers {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			content := string(raw)
			for _, expected := range []string{
				`lnd_service="${LND_SERVICE:-lnd}"`,
				`restart ${lnd_service}`,
				`restart --no-block ${lnd_service}`,
			} {
				if !strings.Contains(content, expected) {
					t.Fatalf("%s must authorize the detected LND service; missing %q", name, expected)
				}
			}
		})
	}
}
