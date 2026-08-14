package privileged

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	maxBarkWalletSecretBytes        = 4096
	defaultManagerCACertificatePath = "/etc/lightningos/tls/local-ca.crt"
	legacyManagerCACertificatePath  = "/etc/lightningos/tls/los-local-ca.crt"
)

type BarkWalletPaths struct {
	SnapshotRoot         string
	WalletDir            string
	AuthDir              string
	AdminPasswordPath    string
	SessionSecretPath    string
	ComposePath          string
	CaddyfilePath        string
	TLSDir               string
	TLSCertificate       string
	TLSPrivateKey        string
	ManagerCACertificate string
	LegacyComposePath    string
	LegacyTLSCert        string
	LegacyTLSKey         string
}

type NativeBarkWalletManager struct {
	Runner       CommandRunner
	Paths        BarkWalletPaths
	requireFixed bool
}

func NewNativeBarkWalletManager(runner CommandRunner) *NativeBarkWalletManager {
	snapshotRoot := filepath.Join(defaultPrivilegedAppsRoot, appmanifest.BarkWalletID)
	dataRoot := filepath.Join(defaultAppsDataRoot, appmanifest.BarkWalletID)
	legacyRoot := filepath.Join(defaultAppsRoot, appmanifest.BarkWalletID)
	tlsDir := filepath.Join(snapshotRoot, appmanifest.BarkWalletTLSDir)
	return &NativeBarkWalletManager{Runner: runner, requireFixed: true, Paths: BarkWalletPaths{
		SnapshotRoot:         snapshotRoot,
		WalletDir:            filepath.Join(dataRoot, "wallet"),
		AuthDir:              filepath.Join(dataRoot, "auth"),
		AdminPasswordPath:    filepath.Join(dataRoot, "auth", "ui_password"),
		SessionSecretPath:    filepath.Join(dataRoot, "auth", "ui_session_secret"),
		ComposePath:          filepath.Join(snapshotRoot, appmanifest.BarkWalletComposeFile),
		CaddyfilePath:        filepath.Join(snapshotRoot, appmanifest.BarkWalletCaddyfile),
		TLSDir:               tlsDir,
		TLSCertificate:       filepath.Join(tlsDir, "server.crt"),
		TLSPrivateKey:        filepath.Join(tlsDir, "server.key"),
		ManagerCACertificate: selectBarkManagerCACertificatePath(defaultManagerCACertificatePath, legacyManagerCACertificatePath),
		LegacyComposePath:    filepath.Join(legacyRoot, appmanifest.BarkWalletComposeFile),
		LegacyTLSCert:        filepath.Join(legacyRoot, appmanifest.BarkWalletTLSDir, "server.crt"),
		LegacyTLSKey:         filepath.Join(legacyRoot, appmanifest.BarkWalletTLSDir, "server.key"),
	}}
}

func selectBarkManagerCACertificatePath(primary, compatibility string) string {
	if safeNonEmptyRegularFile(primary) {
		return primary
	}
	if safeNonEmptyRegularFile(compatibility) {
		return compatibility
	}
	return primary
}

func fixedBarkManagerCACertificatePath(path string) bool {
	return path == defaultManagerCACertificatePath || path == legacyManagerCACertificatePath
}

func (manager *NativeBarkWalletManager) composePaths() appmanifest.BarkWalletComposePaths {
	return appmanifest.BarkWalletComposePaths{
		WalletDir: manager.Paths.WalletDir, AdminPasswordPath: manager.Paths.AdminPasswordPath,
		SessionSecretPath: manager.Paths.SessionSecretPath, CaddyfilePath: manager.Paths.CaddyfilePath,
		TLSCertificate: manager.Paths.TLSCertificate, TLSPrivateKey: manager.Paths.TLSPrivateKey,
		ManagerCACertificate: manager.Paths.ManagerCACertificate,
	}
}

func (manager *NativeBarkWalletManager) Status(ctx context.Context) (BarkWalletState, error) {
	if manager == nil || manager.Runner == nil {
		return BarkWalletState{}, errors.New("Bark Wallet command runner is unavailable")
	}
	state := BarkWalletState{Installed: manager.snapshotReady() || safeNonEmptyRegularFile(manager.Paths.LegacyComposePath), Status: "stopped"}
	if !state.Installed {
		return state, nil
	}
	if _, err := readBarkWalletSecret(manager.Paths.AdminPasswordPath); err == nil {
		state.PasswordAvailable = true
	}
	output, err := manager.Runner.Run(ctx, dockerPath, "ps",
		"--filter", "label=com.docker.compose.project="+appmanifest.BarkWalletProject,
		"--filter", "label=com.docker.compose.service="+appmanifest.BarkWalletPrimaryService,
		"--filter", "status=running", "--format", "{{.ID}}")
	if err != nil {
		state.Status = "unknown"
		return state, errors.New("Bark Wallet container status failed")
	}
	if strings.TrimSpace(output) != "" {
		if parseDockerContainerID(output) == "" {
			state.Status = "unknown"
			return state, errors.New("Bark Wallet container identity is ambiguous")
		}
		state.Status = "running"
	}
	return state, nil
}

func (manager *NativeBarkWalletManager) Ensure(_ context.Context, dryRun bool) (BarkWalletState, error) {
	if err := manager.validateManagerCA(); err != nil {
		return BarkWalletState{}, err
	}
	composeRaw, err := appmanifest.BarkWalletCompose(manager.composePaths())
	if err != nil {
		return BarkWalletState{}, err
	}
	if dryRun {
		return BarkWalletState{Status: "validated"}, nil
	}
	for _, directory := range []struct {
		path string
		mode os.FileMode
	}{
		{manager.Paths.WalletDir, 0700}, {manager.Paths.AuthDir, 0750},
		{manager.Paths.SnapshotRoot, 0700}, {manager.Paths.TLSDir, 0700},
	} {
		if err := ensureDirectoryTreeNoSymlink(directory.path, directory.mode); err != nil {
			return BarkWalletState{}, errors.New("Bark Wallet directory preparation failed")
		}
	}
	if err := manager.ensureSecrets(); err != nil {
		return BarkWalletState{}, err
	}
	if err := manager.ensureTLS(); err != nil {
		return BarkWalletState{}, err
	}
	if err := prepareBarkWalletWritableData(manager.Paths.WalletDir, manager.Paths.AuthDir,
		manager.Paths.AdminPasswordPath, manager.Paths.SessionSecretPath); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet data permission preparation failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.CaddyfilePath, []byte(appmanifest.BarkWalletCaddyConfig()), 0640); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet proxy configuration write failed")
	}
	if err := setPrivilegedPathGroup(manager.Paths.CaddyfilePath, appmanifest.BarkWalletProxyGID); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet proxy configuration ownership failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.ComposePath, []byte(composeRaw), 0600); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet compose write failed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return BarkWalletState{}, err
	}
	return BarkWalletState{Installed: true, Status: "stopped", PasswordAvailable: true}, nil
}

func (manager *NativeBarkWalletManager) Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (BarkWalletState, error) {
	if action != AppLifecycleStart && action != AppLifecycleStop {
		return BarkWalletState{}, errors.New("Bark Wallet lifecycle action is not allowed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return BarkWalletState{}, err
	}
	if dryRun {
		return BarkWalletState{Installed: true, Status: "validated", PasswordAvailable: true}, nil
	}
	if action == AppLifecycleStart {
		for _, image := range appmanifest.BarkWalletImages() {
			if _, err := manager.Runner.Run(ctx, dockerPath, "image", "inspect", image); err != nil {
				return BarkWalletState{}, errors.New("verified Bark Wallet image is not ready")
			}
		}
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return BarkWalletState{}, err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.BarkWalletProject,
		"--project-directory", manager.Paths.SnapshotRoot, "-f", manager.Paths.ComposePath)
	if action == AppLifecycleStart {
		args = append(args, "up", "-d")
	} else {
		args = append(args, "stop", "--timeout", strconv.Itoa(appmanifest.BarkWalletStopTimeout))
	}
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet lifecycle command failed")
	}
	return manager.Status(ctx)
}

func (manager *NativeBarkWalletManager) Remove(ctx context.Context, dryRun bool) error {
	if !manager.snapshotReady() {
		if dryRun {
			return nil
		}
		return manager.removeLegacyContainers(ctx)
	}
	if err := manager.validateSnapshot(); err != nil {
		return err
	}
	if dryRun {
		return nil
	}
	command, prefix, err := (&ComposeAppManager{Runner: manager.Runner}).resolveCompose(ctx)
	if err != nil {
		return err
	}
	args := append([]string(nil), prefix...)
	args = append(args, "--project-name", appmanifest.BarkWalletProject,
		"--project-directory", manager.Paths.SnapshotRoot, "-f", manager.Paths.ComposePath,
		"down", "--remove-orphans", "--timeout", strconv.Itoa(appmanifest.BarkWalletStopTimeout))
	if _, err := manager.Runner.Run(ctx, command, args...); err != nil {
		return errors.New("Bark Wallet remove command failed")
	}
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}

func (manager *NativeBarkWalletManager) EnsureFirewall(ctx context.Context, dryRun bool) (BarkWalletState, error) {
	if dryRun {
		return BarkWalletState{Status: "validated"}, nil
	}
	status, err := manager.Runner.Run(ctx, ufwPath, "status")
	if err != nil || !strings.Contains(strings.ToLower(status), "status: active") {
		return BarkWalletState{Status: "inactive"}, nil
	}
	networkID, err := manager.Runner.Run(ctx, dockerPath, "network", "inspect", appmanifest.BarkWalletProject+"_default", "--format", "{{.Id}}")
	if err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet network lookup failed")
	}
	id := strings.TrimSpace(networkID)
	if len(id) < 12 || len(id) > 64 {
		return BarkWalletState{}, errors.New("Bark Wallet network ID is invalid")
	}
	for _, char := range id {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return BarkWalletState{}, errors.New("Bark Wallet network ID is invalid")
		}
	}
	bridge := "br-" + id[:12]
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", "in", "on", bridge, "to", "any", "port", "8443", "proto", "tcp"); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet manager authorization firewall preparation failed")
	}
	if _, err := manager.Runner.Run(ctx, ufwPath, "allow", strconv.Itoa(appmanifest.BarkWalletPort)+"/tcp"); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet firewall preparation failed")
	}
	return BarkWalletState{Status: "active", UFWActive: true}, nil
}

func (manager *NativeBarkWalletManager) ReadPassword() (string, error) {
	if !manager.snapshotReady() {
		return "", errors.New("Bark Wallet is not installed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return "", err
	}
	return readBarkWalletSecret(manager.Paths.AdminPasswordPath)
}

func (manager *NativeBarkWalletManager) ResetPassword(dryRun bool) (BarkWalletState, error) {
	if !manager.snapshotReady() {
		return BarkWalletState{}, errors.New("Bark Wallet is not installed")
	}
	if err := manager.validateSnapshot(); err != nil {
		return BarkWalletState{}, err
	}
	if dryRun {
		return BarkWalletState{Installed: true, Status: "validated", PasswordAvailable: true}, nil
	}
	password, err := newBarkWalletToken(24)
	if err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet password generation failed")
	}
	if err := writeBarkWalletSecret(manager.Paths.AdminPasswordPath, password); err != nil {
		return BarkWalletState{}, errors.New("Bark Wallet password reset failed")
	}
	return BarkWalletState{Installed: true, Status: "stopped", PasswordAvailable: true}, nil
}

func (manager *NativeBarkWalletManager) ensureSecrets() error {
	for _, secret := range []struct {
		path  string
		bytes int
		name  string
	}{
		{manager.Paths.AdminPasswordPath, 24, "password"},
		{manager.Paths.SessionSecretPath, 32, "session secret"},
	} {
		if _, err := readBarkWalletSecret(secret.path); err == nil {
			continue
		} else if !os.IsNotExist(rootCause(err)) {
			return errors.New("Bark Wallet " + secret.name + " is invalid")
		}
		value, err := newBarkWalletToken(secret.bytes)
		if err != nil {
			return errors.New("Bark Wallet " + secret.name + " generation failed")
		}
		if err := writeBarkWalletSecret(secret.path, value); err != nil {
			return errors.New("Bark Wallet " + secret.name + " write failed")
		}
	}
	return nil
}

func (manager *NativeBarkWalletManager) ensureTLS() error {
	certificateExists, err := barkWalletPathEntryExists(manager.Paths.TLSCertificate)
	if err != nil {
		return errors.New("Bark Wallet TLS certificate state is invalid")
	}
	privateKeyExists, err := barkWalletPathEntryExists(manager.Paths.TLSPrivateKey)
	if err != nil {
		return errors.New("Bark Wallet TLS private key state is invalid")
	}
	certificateRaw, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
	privateKeyRaw, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
	if certificateExists || privateKeyExists {
		if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificateRaw, privateKeyRaw) != nil {
			return errors.New("Bark Wallet TLS key pair is invalid")
		}
		return secureBarkWalletTLSFiles(manager.Paths.TLSCertificate, manager.Paths.TLSPrivateKey)
	}
	legacyCertificateExists, err := barkWalletPathEntryExists(manager.Paths.LegacyTLSCert)
	if err != nil {
		return errors.New("legacy Bark Wallet TLS certificate state is invalid")
	}
	legacyPrivateKeyExists, err := barkWalletPathEntryExists(manager.Paths.LegacyTLSKey)
	if err != nil {
		return errors.New("legacy Bark Wallet TLS private key state is invalid")
	}
	legacyCertificate, legacyCertificateErr := readRegularFile(manager.Paths.LegacyTLSCert, 64*1024)
	legacyPrivateKey, legacyPrivateKeyErr := readRegularFile(manager.Paths.LegacyTLSKey, 64*1024)
	if legacyCertificateExists || legacyPrivateKeyExists {
		if legacyCertificateErr != nil || legacyPrivateKeyErr != nil || validateTLSKeyPair(legacyCertificate, legacyPrivateKey) != nil {
			return errors.New("legacy Bark Wallet TLS key pair is invalid")
		}
		certificateRaw, privateKeyRaw = legacyCertificate, legacyPrivateKey
	} else {
		var err error
		certificateRaw, privateKeyRaw, err = generateBarkWalletTLS()
		if err != nil {
			return errors.New("Bark Wallet TLS generation failed")
		}
	}
	if err := writeAtomicRegularFile(manager.Paths.TLSCertificate, certificateRaw, 0640); err != nil {
		return errors.New("Bark Wallet TLS certificate write failed")
	}
	if err := writeAtomicRegularFile(manager.Paths.TLSPrivateKey, privateKeyRaw, 0640); err != nil {
		return errors.New("Bark Wallet TLS private key write failed")
	}
	return secureBarkWalletTLSFiles(manager.Paths.TLSCertificate, manager.Paths.TLSPrivateKey)
}

func barkWalletPathEntryExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func (manager *NativeBarkWalletManager) validateSnapshot() error {
	if err := manager.validateManagerCA(); err != nil {
		return err
	}
	if err := validateBarkWalletSnapshotPermissions(manager.Paths); err != nil {
		return err
	}
	if err := validateSnapshotDirectoryEntries(manager.Paths.SnapshotRoot, map[string]bool{
		appmanifest.BarkWalletComposeFile: true, appmanifest.BarkWalletCaddyfile: true,
		appmanifest.BarkWalletTLSDir: false,
	}); err != nil {
		return errors.New("Bark Wallet snapshot contains an unexpected entry")
	}
	if err := validateSnapshotDirectoryEntries(manager.Paths.TLSDir, map[string]bool{"server.crt": true, "server.key": true}); err != nil {
		return errors.New("Bark Wallet TLS snapshot contains an unexpected entry")
	}
	composeRaw, err := readRegularFile(manager.Paths.ComposePath, 64*1024)
	expectedCompose, expectedErr := appmanifest.BarkWalletCompose(manager.composePaths())
	if err != nil || expectedErr != nil || string(composeRaw) != expectedCompose {
		return errors.New("Bark Wallet compose does not match the catalog")
	}
	caddyRaw, err := readRegularFile(manager.Paths.CaddyfilePath, 16*1024)
	if err != nil || string(caddyRaw) != appmanifest.BarkWalletCaddyConfig() {
		return errors.New("Bark Wallet proxy configuration does not match the catalog")
	}
	certificateRaw, certificateErr := readRegularFile(manager.Paths.TLSCertificate, 64*1024)
	privateKeyRaw, privateKeyErr := readRegularFile(manager.Paths.TLSPrivateKey, 64*1024)
	if certificateErr != nil || privateKeyErr != nil || validateTLSKeyPair(certificateRaw, privateKeyRaw) != nil {
		return errors.New("Bark Wallet TLS key pair is invalid")
	}
	if _, err := readBarkWalletSecret(manager.Paths.AdminPasswordPath); err != nil {
		return errors.New("Bark Wallet password is invalid")
	}
	if _, err := readBarkWalletSecret(manager.Paths.SessionSecretPath); err != nil {
		return errors.New("Bark Wallet session secret is invalid")
	}
	return nil
}

func (manager *NativeBarkWalletManager) validateManagerCA() error {
	if manager == nil || manager.Paths.ManagerCACertificate == "" {
		return errors.New("manager CA certificate is unavailable")
	}
	if manager.requireFixed {
		if !fixedBarkManagerCACertificatePath(manager.Paths.ManagerCACertificate) ||
			validateRootOwnedRegularFile(manager.Paths.ManagerCACertificate, 0o644) != nil {
			return errors.New("manager CA certificate metadata is unsafe")
		}
	}
	raw, err := readRegularFile(manager.Paths.ManagerCACertificate, 64*1024)
	if err != nil {
		return errors.New("manager CA certificate is unavailable")
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("manager CA certificate is invalid")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	now := time.Now()
	if err != nil || !certificate.IsCA || now.Before(certificate.NotBefore) || now.After(certificate.NotAfter) {
		return errors.New("manager CA certificate is invalid")
	}
	return nil
}

func (manager *NativeBarkWalletManager) snapshotReady() bool {
	return safeNonEmptyRegularFile(manager.Paths.ComposePath) && safeNonEmptyRegularFile(manager.Paths.CaddyfilePath) &&
		safeNonEmptyRegularFile(manager.Paths.TLSCertificate) && safeNonEmptyRegularFile(manager.Paths.TLSPrivateKey)
}

func (manager *NativeBarkWalletManager) removeLegacyContainers(ctx context.Context) error {
	for _, service := range []string{"web", "api", "barkd", appmanifest.BarkWalletPrimaryService} {
		output, err := manager.Runner.Run(ctx, dockerPath, "ps", "-a",
			"--filter", "label=com.docker.compose.project="+appmanifest.BarkWalletProject,
			"--filter", "label=com.docker.compose.service="+service, "--format", "{{.ID}}")
		if err != nil {
			return errors.New("Bark Wallet legacy container status failed")
		}
		if id := parseDockerContainerID(output); id != "" {
			if _, err := manager.Runner.Run(ctx, dockerPath, "rm", "-f", id); err != nil {
				return errors.New("Bark Wallet legacy container removal failed")
			}
		} else if strings.TrimSpace(output) != "" {
			return errors.New("Bark Wallet legacy container identity is ambiguous")
		}
	}
	return removeFixedTree(manager.Paths.SnapshotRoot, filepath.Dir(manager.Paths.SnapshotRoot))
}

func readBarkWalletSecret(path string) (string, error) {
	raw, err := readRegularFile(path, maxBarkWalletSecretBytes)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(raw), "\n")
	if len(value) < 16 || len(value) > 256 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("Bark Wallet secret is invalid")
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return "", errors.New("Bark Wallet secret is invalid")
		}
	}
	return value, nil
}

func writeBarkWalletSecret(path, value string) error {
	if len(value) < 16 || len(value) > 256 {
		return errors.New("Bark Wallet secret is invalid")
	}
	if err := writeAtomicRegularFile(path, []byte(value+"\n"), 0640); err != nil {
		return err
	}
	return setPrivilegedPathGroup(path, appmanifest.BarkWalletAPIGID)
}

func newBarkWalletToken(bytes int) (string, error) {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func secureBarkWalletTLSFiles(paths ...string) error {
	for _, path := range paths {
		if err := os.Chmod(path, 0640); err != nil {
			return err
		}
		if err := setPrivilegedPathGroup(path, appmanifest.BarkWalletProxyGID); err != nil {
			return err
		}
	}
	return nil
}

func generateBarkWalletTLS() ([]byte, []byte, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "lightningos-bark-wallet"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, err
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	return certificate, key, nil
}

func rootCause(err error) error {
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err
}
