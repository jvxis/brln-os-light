//go:build linux

package privileged

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const torLNDRESTHostnameWait = 30 * time.Second

func (manager *NativeTorLNDRESTManager) status(ctx context.Context) (TorLNDRESTState, error) {
	targets, err := manager.restTargets(ctx)
	if err != nil {
		return TorLNDRESTState{}, err
	}
	torrc, _, err := readTorrc()
	if err != nil {
		return TorLNDRESTState{}, err
	}
	if err := validateLNDRESTTorrc(torrc, targets...); err != nil {
		return TorLNDRESTState{}, err
	}
	state := TorLNDRESTState{Status: "absent"}
	dir, target, found := existingTorRESTTarget(torrc, targets)
	if !found && len(targets) > 0 {
		target = targets[0]
	}
	state.RESTReachable = target != "" && lndRESTReachable(ctx, target)
	if !found {
		return state, nil
	}
	state.Status = "configured"
	state.Managed = dir == torLNDRESTDir
	if hostname, err := readOnionHostname(dir); err == nil {
		state.Hostname = hostname
	} else {
		state.Status = "pending"
	}
	return state, nil
}

func (manager *NativeTorLNDRESTManager) ensure(ctx context.Context) (TorLNDRESTState, error) {
	targets, err := manager.restTargets(ctx)
	if err != nil {
		return TorLNDRESTState{}, err
	}
	if len(targets) == 0 {
		return TorLNDRESTState{}, errors.New("no verified local LND REST listener on port 8080")
	}
	torrc, info, err := readTorrc()
	if err != nil {
		return TorLNDRESTState{}, err
	}
	if err := validateLNDRESTTorrc(torrc, targets...); err != nil {
		return TorLNDRESTState{}, err
	}
	dir, target, found := existingTorRESTTarget(torrc, targets)
	if !found {
		// An existing port-8080 service with a different/unavailable target needs
		// review, not a second onion or silent retargeting.
		for _, line := range strings.Split(torrc, "\n") {
			fields := strings.Fields(strings.SplitN(line, "#", 2)[0])
			if len(fields) >= 2 && strings.EqualFold(fields[0], "HiddenServicePort") && fields[1] == torLNDRESTPort {
				return TorLNDRESTState{}, errors.New("existing REST onion target requires manual review")
			}
		}
		target = targets[0]
	}
	if !lndRESTReachable(ctx, target) {
		return TorLNDRESTState{}, errors.New("verified local LND REST listener is unavailable")
	}
	if !found {
		if err := validateOnionPath(torLNDRESTDir); err != nil {
			return TorLNDRESTState{}, err
		}
		updated := appendLNDRESTService(torrc, target)
		if err := writeValidatedTorrc(updated, info, func(candidate string) error { return manager.verifyTorConfigAt(ctx, candidate) }); err != nil {
			return TorLNDRESTState{}, err
		}
		dir = torLNDRESTDir
	}
	if !onionDirectoryAllowed(dir) {
		return TorLNDRESTState{}, errors.New("existing LND REST onion directory is outside /var/lib/tor")
	}
	if err := validateOnionPath(dir); err != nil {
		return TorLNDRESTState{}, err
	}
	if found {
		if err := manager.verifyTorConfigAt(ctx, torrcPath); err != nil {
			return TorLNDRESTState{}, err
		}
	}
	// A hostname can survive removal of the configuration or a failed reload.
	// Always reload on an explicit ensure, even if that file already exists.
	if err := manager.reloadTor(ctx); err != nil {
		if !found {
			if restoreErr := writeTorrc(torrc, info); restoreErr != nil {
				return TorLNDRESTState{}, errors.New("Tor reload failed and configuration rollback failed; manual review required")
			}
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if rollbackErr := manager.reloadTor(rollbackCtx); rollbackErr != nil {
				return TorLNDRESTState{}, errors.New("Tor configuration restored but reload recovery failed; manual review required")
			}
		}
		return TorLNDRESTState{}, err
	}
	deadline := time.Now().Add(torLNDRESTHostnameWait)
	for {
		if hostname, err := readOnionHostname(dir); err == nil {
			return TorLNDRESTState{Status: "configured", Hostname: hostname, Managed: dir == torLNDRESTDir, RESTReachable: true}, nil
		}
		if time.Now().After(deadline) {
			return TorLNDRESTState{Status: "pending", Managed: dir == torLNDRESTDir, RESTReachable: true}, nil
		}
		select {
		case <-ctx.Done():
			return TorLNDRESTState{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// verifyTorConfig runs the same check the Debian tor@default unit runs before
// starting Tor, so a broken block never reaches the running daemon.
func (manager *NativeTorLNDRESTManager) verifyTorConfigAt(ctx context.Context, configPath string) error {
	binary := ""
	for _, candidate := range []string{"/usr/bin/tor", "/usr/sbin/tor"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			binary = candidate
			break
		}
	}
	if binary == "" {
		return errors.New("Tor binary was not found")
	}
	args := []string{}
	if _, err := os.Stat(torServiceDefaults); err == nil {
		args = append(args, "--defaults-torrc", torServiceDefaults)
	}
	args = append(args, "-f", configPath, "--RunAsDaemon", "0", "--verify-config")
	if _, err := manager.runner.Run(ctx, binary, args...); err != nil {
		return errors.New("Tor configuration check failed")
	}
	return nil
}

// reloadTor sends SIGHUP through systemd. LND keeps running: its own onion and
// peer circuits are not torn down by a configuration reload.
func (manager *NativeTorLNDRESTManager) reloadTor(ctx context.Context) error {
	for _, unit := range []string{"tor@default.service", "tor.service"} {
		pid, err := manager.runner.Run(ctx, "/usr/bin/systemctl", "show", unit, "--property=MainPID", "--value")
		if err != nil || strings.TrimSpace(pid) == "0" || strings.TrimSpace(pid) == "" {
			continue
		}
		if _, err := manager.runner.Run(ctx, "/usr/bin/systemctl", "reload", unit); err != nil {
			return errors.New("Tor reload failed")
		}
		return nil
	}
	return errors.New("no running native Tor daemon found")
}

func lndRESTReachable(ctx context.Context, targets ...string) bool {
	target := torLNDRESTTarget
	if len(targets) > 0 {
		target = targets[0]
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func existingTorRESTTarget(torrc string, targets []string) (string, string, bool) {
	for _, target := range targets {
		if dir, found := findLNDRESTService(torrc, target); found {
			return dir, target, true
		}
	}
	return "", "", false
}

func (manager *NativeTorLNDRESTManager) restTargets(ctx context.Context) ([]string, error) {
	unit := manager.lndUnit
	if unit == "" {
		unit = "lnd.service"
	}
	pid, err := manager.runner.Run(ctx, "/usr/bin/systemctl", "show", unit, "--property=MainPID", "--value")
	if err != nil {
		return nil, errors.New("cannot identify native LND service")
	}
	pid = strings.TrimSpace(pid)
	if pid == "0" || pid == "" {
		return nil, nil
	}
	sockets, err := manager.runner.Run(ctx, "/usr/bin/ss", "-H", "-ltnp", "sport = :8080")
	if err != nil {
		return nil, errors.New("cannot inspect native LND REST listeners")
	}
	bridges := map[string]bool{}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.New("cannot inspect local interfaces")
	}
	for _, iface := range interfaces {
		if iface.Name != "docker0" && !strings.HasPrefix(iface.Name, "br-") {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if info, err := os.Stat(filepath.Join("/sys/class/net", iface.Name, "bridge")); err != nil || !info.IsDir() {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && ip.IsPrivate() {
				bridges[ip.String()] = true
			}
		}
	}
	return selectTorRESTTargets(sockets, pid, bridges), nil
}

func readTorrc() (string, os.FileInfo, error) {
	if err := validateRuntimeParent(filepath.Dir(torrcPath)); err != nil {
		return "", nil, errors.New("unsafe Tor configuration directory")
	}
	info, err := os.Lstat(torrcPath)
	if err != nil {
		return "", nil, errors.New("Tor configuration file was not found")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return "", nil, errors.New("Tor configuration file is not root-controlled")
	}
	if info.Size() > torrcMaxBytes {
		return "", nil, errors.New("Tor configuration file is too large")
	}
	fd, err := unix.Open(torrcPath, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", nil, errors.New("Tor configuration file could not be read")
	}
	file := os.NewFile(uintptr(fd), torrcPath)
	defer file.Close()
	if err := validateRootFile(fd, 0o022); err != nil {
		return "", nil, errors.New("unsafe Tor configuration file")
	}
	info, err = file.Stat()
	if err != nil {
		return "", nil, errors.New("Tor configuration stat failed")
	}
	data, err := io.ReadAll(io.LimitReader(file, torrcMaxBytes+1))
	if err != nil || len(data) > torrcMaxBytes {
		return "", nil, errors.New("Tor configuration file could not be read")
	}
	return string(data), info, nil
}

// writeTorrc replaces torrc atomically, keeping its mode and group.
func writeTorrc(content string, info os.FileInfo) error {
	return writeValidatedTorrc(content, info, nil)
}

func writeValidatedTorrc(content string, info os.FileInfo, validate func(string) error) error {
	parent := filepath.Dir(torrcPath)
	if err := validateRuntimeParent(parent); err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("Tor configuration ownership is unknown")
	}
	temporary, err := os.CreateTemp(parent, ".lightningos-torrc-")
	if err != nil {
		return errors.New("create Tor configuration failed")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return errors.New("set Tor configuration mode failed")
	}
	if err := temporary.Chown(0, int(stat.Gid)); err != nil {
		return errors.New("set Tor configuration ownership failed")
	}
	if written, err := temporary.WriteString(content); err != nil || written != len(content) {
		return errors.New("write Tor configuration failed")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("sync Tor configuration failed")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("close Tor configuration failed")
	}
	if validate != nil {
		if err := validate(temporaryPath); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, torrcPath); err != nil {
		return errors.New("commit Tor configuration failed")
	}
	committed = true
	directoryFD, err := unix.Open(parent, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("open Tor configuration directory failed")
	}
	defer unix.Close(directoryFD)
	if err := unix.Fsync(directoryFD); err != nil {
		return errors.New("sync Tor configuration directory failed")
	}
	return nil
}

func validateOnionPath(dir string) error {
	current := "/"
	for _, component := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe Tor onion directory")
		}
	}
	return nil
}

func readOnionHostname(dir string) (string, error) {
	if !onionDirectoryAllowed(dir) {
		return "", errors.New("onion directory is outside /var/lib/tor")
	}
	// Traverse every component without following links, including Tor's state
	// directory, then read only the bounded public hostname (never a private key).
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", errors.New("onion directory unavailable")
	}
	for _, component := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if openErr != nil {
			return "", errors.New("unsafe onion directory")
		}
		fd = next
	}
	defer unix.Close(fd)
	hostFD, err := unix.Openat(fd, "hostname", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("onion hostname unavailable")
	}
	file := os.NewFile(uintptr(hostFD), "hostname")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > torOnionHostnameMax {
		return "", errors.New("invalid hostname file")
	}
	data, err := io.ReadAll(io.LimitReader(file, torOnionHostnameMax+1))
	if err != nil {
		return "", errors.New("onion hostname is not available")
	}
	if len(data) > torOnionHostnameMax {
		return "", errors.New("onion hostname too large")
	}
	hostname := strings.TrimSpace(string(data))
	if !validOnionHostname(hostname) {
		return "", errors.New("onion hostname is invalid")
	}
	return hostname, nil
}
