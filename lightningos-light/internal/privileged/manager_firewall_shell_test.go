package privileged

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Source the real helper, replacing only OS commands. No UFW or host network
// mutation is performed by these portable tests.
func runManagerFirewallShell(t *testing.T, script string) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if runtime.GOOS == "windows" {
		bash = `C:\Program Files\Git\bin\bash.exe`
		_, err = os.Stat(bash)
	}
	if err != nil {
		t.Skip("bash is required for helper regression tests")
	}
	raw, err := os.ReadFile(filepath.Join("..", "server", "assets", "lightningos-manager-firewall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(t.TempDir(), "firewall.sh")
	if err := os.WriteFile(helper, []byte(strings.ReplaceAll(string(raw), "\r\n", "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", "helper=\"$1\"; shift; source \"$helper\"\n"+script, "test", filepath.ToSlash(helper))
	cmd.Env = append(os.Environ(), "LIGHTNINGOS_MANAGER_PORT=8443", "LIGHTNINGOS_LAN_CIDR=", "LIGHTNINGOS_FIREWALL_CONFIG="+filepath.ToSlash(helper)+".conf")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper regression failed: %v\n%s", err, out)
	}
	return string(out)
}

func TestManagerFirewallShellPrivateNetworkBoundaries(t *testing.T) {
	runManagerFirewallShell(t, `
for cidr in 10.0.0.0/8 10.2.3.4/32 172.16.0.0/12 172.31.255.0/24 192.168.0.0/16 192.168.68.0/24; do
  private_lan_cidr "$cidr" || { echo "private CIDR rejected: $cidr"; exit 1; }
done
for cidr in 10.0.0.0/7 172.16.0.0/11 192.168.0.0/15 203.0.112.0/20 100.64.0.0/10 127.0.0.1/32 169.254.0.0/16 0.0.0.0/0 ::/0 fd00::/8 invalid; do
  if private_lan_cidr "$cidr"; then echo "non-LAN accepted: $cidr"; exit 1; fi
done
`)
}

func TestManagerFirewallShellDetectsPrivateLANOnly(t *testing.T) {
	runManagerFirewallShell(t, `
ip() {
  case "$*" in
    '-4 route show default') echo 'default via 203.0.113.1 dev eth0' ;;
    '-o -4 route show dev eth0 scope link') printf '%s\n' "$fixture_route" ;;
    *) echo 'unexpected network lookup' >&2; return 1 ;;
  esac
}

fixture_route='203.0.112.0/20 proto kernel scope link src 203.0.113.10'
[[ -z "$(detect_lan_cidr)" ]] || { echo 'public provider subnet became LAN'; exit 1; }
fixture_route='192.168.68.0/24 proto kernel scope link src 192.168.68.10'
[[ "$(detect_lan_cidr)" == 192.168.68.0/24 ]]
fixture_route='10.0.0.0/7 proto kernel scope link src 10.0.0.2'
[[ -z "$(detect_lan_cidr)" ]] || { echo 'mixed private/public subnet became LAN'; exit 1; }
`)
}

func TestManagerFirewallShellPreservesIsolatedPrivateLAN(t *testing.T) {
	runManagerFirewallShell(t, `
ip() {
  case "$*" in
    '-4 route show default') return 0 ;;
    '-o -4 route show scope link')
      printf '172.18.0.0/16 dev docker0 scope link\n10.1.0.0/16 dev br-0123456789ab scope link\n192.168.68.0/24 dev enp0s3 scope link\n' ;;
    *) return 1 ;;
  esac
}
[[ "$(detect_lan_cidr)" == 192.168.68.0/24 ]]
`)
}

const managerFirewallShellFixture = `
command_log="${helper}.commands"
: >"$command_log"
read_config_value() {
  case "$1" in
    ACCESS_MODE) echo "${SAVED_MODE:-lan}" ;;
    LAN_CIDR) echo "${SAVED_CIDR:-203.0.112.0/20}" ;;
  esac
}
save_config() { printf 'SAVED %s %s %s\n' "$1" "$2" "$3"; }
ip() {
  case "$*" in
    '-4 route show default') echo 'default via 203.0.113.1 dev eth0' ;;
    '-o -4 route show dev eth0 scope link') echo "${ROUTE_CIDR:-203.0.112.0/20} proto kernel scope link src 203.0.113.10" ;;
    'link show tailscale0') [[ "${HAS_VPN:-1}" == 1 ]] ;;
    *) return 1 ;;
  esac
}
ufw() {
  if [[ "$*" == status ]]; then
    if [[ "${INACTIVE:-0}" == 1 ]]; then echo 'Status: inactive'; return; fi
    printf 'Status: active\n'
    if [[ "${EXPECT_LAN:-0}" == 1 ]]; then
      printf '8443/tcp ALLOW IN 192.168.68.0/24\n'
    else
      printf '8443/tcp on tailscale0 ALLOW IN Anywhere\n8443/tcp on eth0 DENY IN Anywhere\n'
    fi
    printf '8443/tcp on br-0123456789ab ALLOW IN 172.20.0.0/16\n'
    return
  fi
  printf '%s\n' "$*" >>"$command_log"
  printf 'UFW %s\n' "$*"
  if [[ "${FAIL_INSERT:-0}" == 1 && "$1" == insert ]]; then return 1; fi
  if [[ "${FAIL_DELETE:-0}" == 1 && "$1" == --force ]]; then return 1; fi
  return 0
}
`

func TestManagerFirewallShellMigratesVPSBeforeRemovingOldAccess(t *testing.T) {
	out := runManagerFirewallShell(t, managerFirewallShellFixture+"\nconfigure_firewall\nsed 's/^/TRACE /' \"$command_log\"\n")
	allow := strings.Index(out, "TRACE allow in on tailscale0")
	deny := strings.Index(out, "TRACE insert 1 deny in on eth0")
	remove := strings.Index(out, "TRACE --force delete allow from 203.0.112.0/20")
	if allow < 0 || deny < allow || remove < deny || strings.Contains(out, "UFW allow from 203.") || !strings.Contains(out, "SAVED vpn none 0") {
		t.Fatalf("unsafe VPS transition: %s", out)
	}
}

func TestManagerFirewallShellPreservesPrivateLAN(t *testing.T) {
	out := runManagerFirewallShell(t, managerFirewallShellFixture+`
SAVED_CIDR=192.168.68.0/24 ROUTE_CIDR=192.168.68.0/24 EXPECT_LAN=1
configure_firewall
`)
	if !strings.Contains(out, "UFW allow from 192.168.68.0/24") || !strings.Contains(out, "SAVED lan 192.168.68.0/24 0") || strings.Contains(out, "UFW insert 1 deny") {
		t.Fatalf("private LAN changed: %s", out)
	}
}

func TestManagerFirewallShellNoVPNStillBlocksUplink(t *testing.T) {
	out := runManagerFirewallShell(t, managerFirewallShellFixture+`
HAS_VPN=0
if configure_firewall; then echo 'unexpected success'; exit 1; else [[ $? == 3 ]]; fi
`)
	if !strings.Contains(out, "UFW insert 1 deny in on eth0") || !strings.Contains(out, "SAVED vpn none 0") || strings.Contains(out, "UFW allow in on tailscale0") || strings.Contains(out, "[OK]") {
		t.Fatalf("no-VPN public policy unsafe: %s", out)
	}
}

func TestManagerFirewallShellFailureDoesNotSaveSuccess(t *testing.T) {
	for _, failure := range []string{"FAIL_INSERT", "FAIL_DELETE"} {
		out := runManagerFirewallShell(t, managerFirewallShellFixture+"\n"+failure+"=1\n"+`
if configure_firewall; then exit 1; else [[ $? == 5 ]]; fi
`)
		if strings.Contains(out, "SAVED") || strings.Contains(out, "[OK]") {
			t.Fatalf("failed rule reported success: %s", out)
		}
	}
}

func TestManagerFirewallShellInactiveAndExplicitPublicDoNotMutate(t *testing.T) {
	for _, script := range []string{
		"INACTIVE=1\nif configure_firewall; then exit 1; else [[ $? == 3 ]]; fi",
		"REQUESTED_MODE=lan REQUESTED_LAN_CIDR=203.0.112.0/20\nif configure_firewall; then exit 1; else [[ $? == 2 ]]; fi",
		"LIGHTNINGOS_LAN_CIDR=203.0.112.0/20\nif configure_firewall; then exit 1; else [[ $? == 2 ]]; fi",
	} {
		out := runManagerFirewallShell(t, managerFirewallShellFixture+"\n"+script)
		if strings.Contains("\n"+out, "\nUFW ") || strings.Contains(out, "SAVED") {
			t.Fatalf("unexpected mutation: %s", out)
		}
	}
}

func TestManagerFirewallShellExplicitUnprotectedAcknowledgement(t *testing.T) {
	out := runManagerFirewallShell(t, managerFirewallShellFixture+`
REQUESTED_MODE=unprotected
if configure_firewall; then exit 1; else [[ $? == 4 ]]; fi
ACKNOWLEDGE_UNPROTECTED=1
configure_firewall
`)
	if !strings.Contains(out, "SAVED unprotected 203.0.112.0/20 1") || strings.Contains("\n"+out, "\nUFW ") {
		t.Fatalf("explicit unprotected acknowledgement changed: %s", out)
	}
}
