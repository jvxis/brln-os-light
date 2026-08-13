package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const publicPoolAppID = appmanifest.PublicPoolID

type publicPoolPaths struct {
	Root, DataDir, ComposePath, EnvPath string
}

type publicPoolApp struct{ server *Server }

func newPublicPoolApp(s *Server) appHandler { return publicPoolApp{server: s} }
func publicPoolDefinition() appDefinition {
	return appDefinition{ID: publicPoolAppID, Name: "Public Pool", Description: "Run your own Public Pool backend + web UI with local or remote Bitcoin RPC.", Port: appmanifest.PublicPoolUIPort}
}
func (a publicPoolApp) Definition() appDefinition { return publicPoolDefinition() }
func (a publicPoolApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	handled, state, err := system.PublicPoolStatusWithBroker(ctx)
	if !handled {
		return info, errors.New("Public Pool status requires privileged broker enforce mode")
	}
	info.Installed, info.Status, info.UFWActive = state.Installed, state.Status, state.UFWActive
	return info, err
}
func (a publicPoolApp) Install(ctx context.Context) error   { return a.server.installPublicPool(ctx) }
func (a publicPoolApp) Uninstall(ctx context.Context) error { return a.server.uninstallPublicPool(ctx) }
func (a publicPoolApp) Start(ctx context.Context) error     { return a.server.startPublicPool(ctx) }
func (a publicPoolApp) Stop(ctx context.Context) error      { return a.server.stopPublicPool(ctx) }

func publicPoolAppPaths() publicPoolPaths {
	root := filepath.Join(appsRoot, publicPoolAppID)
	return publicPoolPaths{Root: root, DataDir: filepath.Join(appsDataRoot, publicPoolAppID, "db"), ComposePath: filepath.Join(root, appmanifest.PublicPoolComposeFile), EnvPath: filepath.Join(root, appmanifest.PublicPoolEnvFile)}
}

func (s *Server) installPublicPool(ctx context.Context) error {
	return s.prepareAndStartPublicPool(ctx)
}
func (s *Server) startPublicPool(ctx context.Context) error { return s.prepareAndStartPublicPool(ctx) }

func (s *Server) prepareAndStartPublicPool(ctx context.Context) error {
	if err := ensureDockerForCatalogApp(ctx); err != nil {
		return err
	}
	runtime, err := s.resolvePublicPoolRuntime(ctx)
	if err != nil {
		return err
	}
	if runtime.BitcoinMode != appmanifest.PublicPoolBitcoinRemote {
		if handled, err := system.EnsureBitcoinConsumerNetworkWithBroker(ctx); !handled {
			return errors.New("Public Pool Bitcoin network requires privileged broker enforce mode")
		} else if err != nil {
			return err
		}
	}
	if handled, err := system.EnsurePublicPoolWithBroker(ctx, runtime); !handled {
		return errors.New("Public Pool preparation requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	for _, variant := range appmanifest.PublicPoolImageVariants() {
		if handled, err := system.PrepareAppImageWithBroker(ctx, appmanifest.PublicPoolID, string(variant)); !handled {
			return errors.New("Public Pool image preparation requires privileged broker enforce mode")
		} else if err != nil {
			return err
		}
		if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, appmanifest.PublicPoolID, string(variant)); !handled {
			return errors.New("Public Pool image verification requires privileged broker enforce mode")
		} else if err != nil || !runnable {
			return errors.New("Public Pool image verification failed")
		}
	}
	if handled, err := system.PublicPoolLifecycleWithBroker(ctx, "start"); !handled {
		return errors.New("Public Pool lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	if handled, err := system.EnsurePublicPoolFirewallWithBroker(ctx); !handled {
		return errors.New("Public Pool firewall requires privileged broker enforce mode")
	} else if err != nil && s.logger != nil {
		s.logger.Printf("public-pool: firewall rule failed: %v", err)
	}
	return nil
}

func (s *Server) stopPublicPool(ctx context.Context) error {
	if handled, err := system.PublicPoolLifecycleWithBroker(ctx, "stop"); !handled {
		return errors.New("Public Pool lifecycle requires privileged broker enforce mode")
	} else {
		return err
	}
}

func (s *Server) uninstallPublicPool(ctx context.Context) error {
	if handled, err := system.RemovePublicPoolWithBroker(ctx); !handled {
		return errors.New("Public Pool removal requires privileged broker enforce mode")
	} else if err != nil {
		return err
	}
	paths := publicPoolAppPaths()
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove legacy app files: %w", err)
	}
	return nil
}

func (s *Server) resolvePublicPoolRuntime(ctx context.Context) (appmanifest.PublicPoolRuntime, error) {
	if readBitcoinSource() == "remote" {
		host, port := parseMainchainRPC(s.cfg.BitcoinRemote.RPCHost)
		user, pass := readBitcoinSecrets()
		if user == "" || pass == "" {
			return appmanifest.PublicPoolRuntime{}, errors.New("bitcoin remote RPC credentials missing")
		}
		runtime := appmanifest.PublicPoolRuntime{BitcoinMode: appmanifest.PublicPoolBitcoinRemote, BitcoinRPCURL: toHTTPRPCURL(host), BitcoinRPCPort: port, BitcoinRPCUser: user, BitcoinRPCPass: pass, BitcoinZMQHost: publicPoolExternalZMQHost(s.cfg.BitcoinRemote.ZMQRawBlock)}
		return runtime, appmanifest.ValidatePublicPoolRuntime(runtime)
	}
	local, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return appmanifest.PublicPoolRuntime{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
	}
	if strings.TrimSpace(local.User) == "" || strings.TrimSpace(local.Pass) == "" {
		return appmanifest.PublicPoolRuntime{}, errors.New("local bitcoin RPC credentials missing")
	}
	runtime := appmanifest.PublicPoolRuntime{BitcoinRPCPort: 8332, BitcoinRPCUser: local.User, BitcoinRPCPass: local.Pass}
	if fileExists(bitcoinCoreAppPaths().ComposePath) {
		runtime.BitcoinMode, runtime.BitcoinRPCURL, runtime.BitcoinZMQHost = appmanifest.PublicPoolBitcoinLocalApp, "http://bitcoind", "tcp://bitcoind:28332"
	} else {
		runtime.BitcoinMode, runtime.BitcoinRPCURL, runtime.BitcoinZMQHost = appmanifest.PublicPoolBitcoinLocalExternal, "http://"+appmanifest.BitcoinConsumerHostGateway, "tcp://"+appmanifest.BitcoinConsumerHostGateway+":28332"
	}
	return runtime, appmanifest.ValidatePublicPoolRuntime(runtime)
}

func toHTTPRPCURL(host string) string {
	trimmed := strings.TrimSpace(host)
	if trimmed == "" {
		trimmed = "127.0.0.1"
	}
	if strings.Contains(trimmed, ":") && !strings.HasPrefix(trimmed, "[") {
		trimmed = "[" + trimmed + "]"
	}
	return "http://" + trimmed
}
func normalizePublicPoolZMQHost(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "tcp://") {
		return "tcp://" + trimmed
	}
	return trimmed
}
func publicPoolExternalZMQHost(endpoint string) string {
	normalized := normalizePublicPoolZMQHost(endpoint)
	if normalized == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(normalized, "tcp://"))
	if err != nil {
		return ""
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return ""
	}
	return normalized
}
