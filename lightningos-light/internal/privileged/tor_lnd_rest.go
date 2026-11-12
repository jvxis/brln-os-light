package privileged

import (
	"context"
	"errors"
	"path"
	"regexp"
	"strings"
)

// LND REST over a Tor onion service. Lightning Address services such as
// pay.br-ln.com reach a home node's REST API through Tor, so the member needs
// an onion address for port 8080. The broker owns every value here: the
// torrc path, the onion directory, the virtual port and the local target are
// constants, and neither operation accepts parameters.
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
	runner CommandRunner
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
func findLNDRESTService(torrc string) (string, bool) {
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
			if len(fields) == 2 || fields[2] == torLNDRESTTarget || fields[2] == "localhost:"+torLNDRESTPort {
				return current, true
			}
		}
	}
	return "", false
}

// Do not guess about includes, continuations or quoted paths. Supporting a
// subset explicitly is safer than accidentally creating a second service.
func validateLNDRESTTorrc(torrc string) error {
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
		dir, found := findLNDRESTService(torrc)
		if !found || dir != torLNDRESTDir {
			return errors.New("managed Tor onion directory has conflicting configuration")
		}
	}
	return nil
}

// appendLNDRESTService adds the fixed LightningOS block at the end of torrc.
func appendLNDRESTService(torrc string) string {
	if torrc != "" && !strings.HasSuffix(torrc, "\n") {
		torrc += "\n"
	}
	return torrc + "\n" + torLNDRESTMarker + "\nHiddenServiceDir " + torLNDRESTDir + "/\nHiddenServicePort " +
		torLNDRESTPort + " " + torLNDRESTTarget + "\n"
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
