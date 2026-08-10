package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	// robosatsImage is pinned to the version Umbrel/Start9 ship and that renders
	// orders correctly in self-hosted mode. v0.8.5-alpha (:latest) regressed: the
	// order screen never leaves the loading spinner even though the coordinator
	// returns the order plus its bond invoice. Bump this only after verifying a
	// newer tag actually renders the make/take flow self-hosted.
	robosatsImage    = appmanifest.RoboSatsImage
	robosatsTorImage = appmanifest.RoboSatsTorImage
	// robosatsProxyImage terminates TLS/HTTP2 in front of the RoboSats client.
	// The client is served plain HTTP/1.1, which caps the browser at 6
	// connections per host; over Tor each coordinator poll takes many seconds,
	// so the polls pile up and starve the Nostr relay WebSockets the order flow
	// depends on. HTTP/2 multiplexes them over a single connection and removes
	// that limit.
	robosatsProxyImage = appmanifest.RoboSatsProxyImage
	// robosatsPort is both the external HTTPS port (Caddy) and the internal
	// port the client's own nginx listens on inside its container.
	robosatsPort = appmanifest.RoboSatsPort
)

type robosatsPaths struct {
	Root          string
	DataDir       string
	ComposePath   string
	TLSDir        string
	CaddyfilePath string
}

type robosatsApp struct {
	server *Server
}

func newRobosatsApp(s *Server) appHandler {
	return robosatsApp{server: s}
}

func robosatsDefinition() appDefinition {
	return appDefinition{
		ID:          "robosats",
		Name:        "RoboSats Gateway",
		Description: "Self-hosted RoboSats client for P2P Bitcoin trading over Tor.",
		Port:        robosatsPort,
	}
}

func (a robosatsApp) Definition() appDefinition {
	return robosatsDefinition()
}

func (a robosatsApp) Info(ctx context.Context) (appInfo, error) {
	def := a.Definition()
	info := newAppInfo(def)
	// Served through the TLS/HTTP2 proxy, so the UI must open it over https.
	info.Scheme = "https"
	paths := robosatsAppPaths()
	if !fileExists(paths.ComposePath) {
		return info, nil
	}
	info.Installed = true
	status := "unknown"
	handled, brokerStatus, _, err := system.InspectAppWithBroker(ctx, appmanifest.RoboSatsID)
	if handled {
		status = brokerStatus
	} else {
		status, err = getComposeStatus(ctx, paths.Root, paths.ComposePath, "robosats")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a robosatsApp) Install(ctx context.Context) error {
	return a.server.installRobosats(ctx)
}

func (a robosatsApp) Uninstall(ctx context.Context) error {
	return a.server.uninstallRobosats(ctx)
}

func (a robosatsApp) Start(ctx context.Context) error {
	return a.server.startRobosats(ctx)
}

func (a robosatsApp) Stop(ctx context.Context) error {
	return a.server.stopRobosats(ctx)
}

func robosatsAppPaths() robosatsPaths {
	root := filepath.Join(appsRoot, "robosats")
	dataDir := filepath.Join(appsDataRoot, "robosats")
	return robosatsPaths{
		Root:          root,
		DataDir:       dataDir,
		ComposePath:   filepath.Join(root, "docker-compose.yaml"),
		TLSDir:        filepath.Join(root, "tls"),
		CaddyfilePath: filepath.Join(root, "Caddyfile"),
	}
}

func (s *Server) installRobosats(ctx context.Context) error {
	if err := ensureDocker(ctx); err != nil {
		return err
	}
	paths := robosatsAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureRobosatsImages(ctx); err != nil {
		return err
	}
	if err := ensureRobosatsProxyAssets(paths); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureRobosatsUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("robosats: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) uninstallRobosats(ctx context.Context) error {
	paths := robosatsAppPaths()
	if fileExists(paths.ComposePath) {
		_ = runCompose(ctx, paths.Root, paths.ComposePath, "down", "--remove-orphans")
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func (s *Server) startRobosats(ctx context.Context) error {
	paths := robosatsAppPaths()
	if fileExists(paths.ComposePath) {
		if err := prepareRoboSatsCatalogAssets(paths); err != nil {
			return err
		}
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.RoboSatsID, "start"); handled {
		return err
	}
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	if err := os.MkdirAll(paths.DataDir, 0750); err != nil {
		return fmt.Errorf("failed to create app data directory: %w", err)
	}
	if err := ensureRobosatsImages(ctx); err != nil {
		return err
	}
	if err := ensureRobosatsProxyAssets(paths); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths)); err != nil {
		return err
	}
	if err := runCompose(ctx, paths.Root, paths.ComposePath, "up", "-d"); err != nil {
		return err
	}
	if err := ensureRobosatsUfwAccess(ctx); err != nil && s.logger != nil {
		s.logger.Printf("robosats: ufw rule failed: %v", err)
	}
	return nil
}

func (s *Server) stopRobosats(ctx context.Context) error {
	paths := robosatsAppPaths()
	if fileExists(paths.ComposePath) {
		if err := prepareRoboSatsCatalogAssets(paths); err != nil {
			return err
		}
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, appmanifest.RoboSatsID, "stop"); handled {
		return err
	}
	if !fileExists(paths.ComposePath) {
		return errors.New("RoboSats is not installed")
	}
	return runCompose(ctx, paths.Root, paths.ComposePath, "stop")
}

func robosatsComposeContents(paths robosatsPaths) string {
	return appmanifest.RoboSatsCompose(paths.CaddyfilePath, paths.TLSDir)
}

func prepareRoboSatsCatalogAssets(paths robosatsPaths) error {
	if err := ensureRobosatsProxyAssets(paths); err != nil {
		return err
	}
	_, err := ensureFileWithChange(paths.ComposePath, robosatsComposeContents(paths))
	return err
}

// robosatsCaddyfileContents serves the client over TLS/HTTP2 and reverse-proxies
// every path (REST + the Nostr relay WebSockets) to the client container. Caddy
// upgrades WebSockets transparently and enables HTTP/2 whenever TLS is present.
//
// response_header_timeout is the key to a usable federation: the RoboSats client
// fans out to every coordinator at once, and offline ones (or ones whose socat
// bridge failed to bind) leave Tor hanging ~15s per request, which congests the
// single-user Tor and drags the live coordinators up to ~20s. Capping the wait
// makes a dead coordinator return 502 fast; closing the upstream connection
// cascades down and cancels the Tor attempt, freeing it for the live ones. It
// only bounds time-to-first-header, so established WebSockets stream freely.
func robosatsCaddyfileContents() string {
	return appmanifest.RoboSatsCaddyfile()
}

func ensureRobosatsProxyAssets(paths robosatsPaths) error {
	if err := ensureRobosatsProxyCert(paths); err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.CaddyfilePath, robosatsCaddyfileContents()); err != nil {
		return err
	}
	return nil
}

// ensureRobosatsProxyCert generates a long-lived self-signed certificate for the
// Caddy proxy. It is a LAN gateway accessed by IP, so the browser shows the same
// trust warning as the LightningOS UI on :8443 — no CA is involved.
func ensureRobosatsProxyCert(paths robosatsPaths) error {
	crtPath := filepath.Join(paths.TLSDir, "server.crt")
	keyPath := filepath.Join(paths.TLSDir, "server.key")
	if fileExists(crtPath) && fileExists(keyPath) {
		return nil
	}
	if err := os.MkdirAll(paths.TLSDir, 0750); err != nil {
		return fmt.Errorf("failed to create robosats tls directory: %w", err)
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate robosats proxy key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate certificate serial: %w", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lightningos-robosats"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create robosats proxy certificate: %w", err)
	}
	if err := writePEMFile(crtPath, "CERTIFICATE", der, 0644); err != nil {
		return err
	}
	if err := writePEMFile(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(priv), 0600); err != nil {
		return err
	}
	return nil
}

func writePEMFile(path, blockType string, der []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	return nil
}

func ensureRobosatsImages(ctx context.Context) error {
	for _, image := range []string{robosatsImage, robosatsTorImage, robosatsProxyImage} {
		if err := ensureDockerImage(ctx, image); err != nil {
			return err
		}
	}
	return nil
}

// ensureDockerImage pulls the image only when it is not already cached. Every
// RoboSats image is pinned to an exact tag, so a local copy is authoritative and
// bumping a pin simply pulls the new tag on the next install/start.
func ensureDockerImage(ctx context.Context, image string) error {
	if _, err := system.RunCommandWithSudo(ctx, "docker", "image", "inspect", image); err == nil {
		return nil
	}
	return pullDockerImage(ctx, image)
}

func pullDockerImage(ctx context.Context, image string) error {
	out, err := system.RunCommandWithSudo(ctx, "docker", "pull", image)
	if err != nil {
		msg := strings.TrimSpace(out)
		if msg == "" {
			return fmt.Errorf("failed to pull %s: %w", image, err)
		}
		return fmt.Errorf("failed to pull %s: %s", image, msg)
	}
	return nil
}

func ensureRobosatsUfwAccess(ctx context.Context) error {
	statusOut, err := system.RunCommandWithSudo(ctx, "ufw", "status")
	if err != nil || !strings.Contains(strings.ToLower(statusOut), "status: active") {
		return nil
	}
	_, err = system.RunCommandWithSudo(ctx, "ufw", "allow", fmt.Sprintf("%d/tcp", robosatsPort))
	return err
}
