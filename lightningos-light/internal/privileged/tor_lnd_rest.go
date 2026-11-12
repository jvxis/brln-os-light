package privileged

import (
	"context"
	"errors"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"
)

// LND REST over a Tor onion service. Lightning Address services such as
// pay.br-ln.com reach a home node's REST API through Tor, so the member needs
// an onion address for port 8080. The broker owns every value here: the
// torrc path, onion directory and virtual port are constants. The destination
// is a verified local listener owned by native LND; neither operation accepts
// destination parameters from the caller.
const (
	torrcPath            = "/etc/tor/torrc"
	torServiceDefaults   = "/usr/share/tor/tor-service-defaults-torrc"
	torLNDRESTDir        = "/var/lib/tor/lightningos-lnd-rest"
	torLNDRESTPort       = "8080"
	torLNDRESTTarget     = "127.0.0.1:8080"
	torLNDRESTMarker     = "# LightningOS: LND REST over Tor, for Lightning Address services (pay.br-ln.com)"
	torrcMaxBytes        = 256 * 1024
	torOnionHostnameMax  = 128
	torOnionDirectoryTop = "/var/lib/tor/"
)

var onionHostnamePattern = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

type NativeTorLNDRESTManager struct {
	runner  CommandRunner
	lndUnit string // Internal test fixture override; never supplied by the API.
}

func NewNativeTorLNDRESTManager(runner CommandRunner) *NativeTorLNDRESTManager {
	return &NativeTorLNDRESTManager{runner: runner}
}

func (manager *NativeTorLNDRESTManager) Status(ctx context.Context, dryRun bool) (TorLNDRESTState, error) {
	if manager == nil {
		return TorLNDRESTState{}, errors.New("Tor LND REST manager is unavailable")
	}
	if dryRun {
		return TorLNDRESTState{Status: "validated"}, nil
	}
	return manager.status(ctx)
}

func (manager *NativeTorLNDRESTManager) Ensure(ctx context.Context, dryRun bool) (TorLNDRESTState, error) {
	if manager == nil || manager.runner == nil {
		return TorLNDRESTState{}, errors.New("Tor LND REST manager is unavailable")
	}
	if dryRun {
		return TorLNDRESTState{Status: "validated"}, nil
	}
	return manager.ensure(ctx)
}

// findLNDRESTService returns the HiddenServiceDir of the first onion service
// in torrc that forwards virtual port 8080 to the local LND REST listener. A
// block the member created by hand counts, so the card never adds a second one.
func findLNDRESTService(torrc string, targets ...string) (string, bool) {
	if len(targets) == 0 {
		targets = []string{torLNDRESTTarget}
	}
	current := ""
	for _, raw := range strings.Split(torrc, "\n") {
		fields := strings.Fields(strings.SplitN(raw, "#", 2)[0])
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "hiddenservicedir":
			current = ""
			if len(fields) >= 2 {
				current = strings.TrimRight(fields[1], "/")
			}
		case "hiddenserviceport":
			if current == "" || len(fields) < 2 || fields[1] != torLNDRESTPort {
				continue
			}
			target := torLNDRESTTarget
			if len(fields) > 2 {
				target = fields[2]
			}
			if target == "localhost:8080" {
				target = torLNDRESTTarget
			}
			for _, allowed := range targets {
				if target == allowed {
					return current, true
				}
			}
		}
	}
	return "", false
}

// Do not guess about includes, continuations or quoted paths. Supporting a
// subset explicitly is safer than accidentally creating a second service.
func validateLNDRESTTorrc(torrc string, targets ...string) error {
	count := 0
	for _, raw := range strings.Split(torrc, "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		key := strings.ToLower(fields[0])
		if key == "%include" || strings.HasSuffix(line, "\\") || strings.ContainsAny(line, "\x00\"") {
			return errors.New("Tor configuration uses includes or syntax requiring manual review")
		}
		if key == "hiddenservicedir" {
			if len(fields) != 2 || !onionDirectoryAllowed(strings.TrimRight(fields[1], "/")) {
				return errors.New("Tor onion directory requires manual review")
			}
			if strings.TrimRight(fields[1], "/") == torLNDRESTDir {
				count++
			}
		}
	}
	if count > 1 {
		return errors.New("duplicate managed Tor onion directory")
	}
	if count == 1 {
		dir, found := findLNDRESTService(torrc, targets...)
		if !found || dir != torLNDRESTDir {
			return errors.New("managed Tor onion directory has conflicting configuration")
		}
	}
	return nil
}

// appendLNDRESTService adds the fixed LightningOS block at the end of torrc.
func appendLNDRESTService(torrc string, targets ...string) string {
	target := torLNDRESTTarget
	if len(targets) > 0 {
		target = targets[0]
	}
	if torrc != "" && !strings.HasSuffix(torrc, "\n") {
		torrc += "\n"
	}
	return torrc + "\n" + torLNDRESTMarker + "\nHiddenServiceDir " + torLNDRESTDir + "/\nHiddenServicePort " +
		torLNDRESTPort + " " + target + "\n"
}

// Kernel-reported listeners must belong to the running native LND. A private
// address alone is insufficient: only addresses assigned to local Docker bridges
// are accepted, and loopback is always preferred. No DNS resolution is involved.
func selectTorRESTTargets(sockets, pid string, bridges map[string]bool) []string {
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(pid) {
		return nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(sockets, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] != "LISTEN" || !strings.Contains(line, "pid="+pid+",") {
			continue
		}
		host, port, err := net.SplitHostPort(fields[3])
		if err != nil || port != torLNDRESTPort {
			continue
		}
		if host == "0.0.0.0" || host == "*" {
			host = "127.0.0.1"
		}
		if host == "::" {
			host = "::1"
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !(ip.IsPrivate() && bridges[host])) {
			continue
		}
		seen[net.JoinHostPort(host, port)] = true
	}
	result := make([]string, 0, len(seen))
	for target := range seen {
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool {
		hi, _, _ := net.SplitHostPort(result[i])
		hj, _, _ := net.SplitHostPort(result[j])
		li, lj := net.ParseIP(hi).IsLoopback(), net.ParseIP(hj).IsLoopback()
		if li != lj {
			return li
		}
		return result[i] < result[j]
	})
	return result
}

// onionDirectoryAllowed keeps hostname reads inside Tor's own state directory,
// even when the onion block was written by hand.
func onionDirectoryAllowed(dir string) bool {
	clean := path.Clean(dir)
	return clean == dir && strings.HasPrefix(clean+"/", torOnionDirectoryTop) && clean+"/" != torOnionDirectoryTop
}

func validOnionHostname(value string) bool {
	return onionHostnamePattern.MatchString(value)
}
