package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lightningos-light/internal/appmanifest"
	"lightningos-light/internal/system"
)

const (
	mempoolAppID            = appmanifest.MempoolID
	mempoolFrontendHostPort = appmanifest.MempoolPort
)

type mempoolPaths struct {
	Root        string
	ComposePath string
	EnvPath     string
}

type mempoolApp struct{ server *Server }

func newMempoolApp(s *Server) appHandler { return mempoolApp{server: s} }

func mempoolDefinition() appDefinition {
	return appDefinition{
		ID: mempoolAppID, Name: "Mempool",
		Description: "Self-hosted mempool.space dashboard for your Bitcoin Core node. Requires Electrs installed and running.",
		Port:        mempoolFrontendHostPort,
	}
}

func (a mempoolApp) Definition() appDefinition { return mempoolDefinition() }

func (a mempoolApp) Info(ctx context.Context) (appInfo, error) {
	info := newAppInfo(a.Definition())
	if !fileExists(mempoolAppPaths().ComposePath) {
		return info, nil
	}
	info.Installed = true
	handled, status, _, err := system.InspectAppWithBroker(ctx, mempoolAppID)
	if !handled {
		info.Status = "unknown"
		return info, errors.New("Mempool status requires privileged broker enforce mode")
	}
	if err != nil {
		info.Status = "unknown"
		return info, err
	}
	info.Status = status
	return info, nil
}

func (a mempoolApp) Install(ctx context.Context) error { return a.server.applyMempool(ctx) }
func (a mempoolApp) Start(ctx context.Context) error   { return a.server.applyMempool(ctx) }

func (a mempoolApp) Stop(ctx context.Context) error {
	if !fileExists(mempoolAppPaths().ComposePath) {
		return errors.New("Mempool is not installed")
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, mempoolAppID, "stop"); !handled {
		return errors.New("Mempool lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Mempool stop failed: %w", err)
	}
	return nil
}

func (a mempoolApp) Uninstall(ctx context.Context) error {
	paths := mempoolAppPaths()
	if fileExists(paths.ComposePath) {
		if handled, err := system.RemoveAppWithBroker(ctx, mempoolAppID); !handled {
			return errors.New("Mempool removal requires privileged broker enforce mode")
		} else if err != nil {
			return fmt.Errorf("Mempool removal failed: %w", err)
		}
	}
	if err := os.RemoveAll(paths.Root); err != nil {
		return fmt.Errorf("failed to remove app files: %w", err)
	}
	return nil
}

func mempoolAppPaths() mempoolPaths {
	root := filepath.Join(appsRoot, mempoolAppID)
	return mempoolPaths{
		Root: root, ComposePath: filepath.Join(root, appmanifest.MempoolComposeFile),
		EnvPath: filepath.Join(root, appmanifest.MempoolEnvFile),
	}
}

func (s *Server) applyMempool(ctx context.Context) error {
	if err := ensureDockerForCatalogAppEnforce(ctx); err != nil {
		return err
	}
	if err := s.requireFullIndexApps(ctx); err != nil {
		return err
	}
	if !fileExists(electrsAppPaths().ComposePath) {
		return errors.New("Mempool requires the Electrs app to be installed")
	}
	electrsInfo, err := newElectrsApp(s).Info(ctx)
	if err != nil {
		return fmt.Errorf("failed to read Electrs status: %w", err)
	}
	if electrsInfo.Status != "running" {
		return errors.New("Mempool requires Electrs to be installed and running")
	}

	paths := mempoolAppPaths()
	if err := os.MkdirAll(paths.Root, 0750); err != nil {
		return fmt.Errorf("failed to create app directory: %w", err)
	}
	runtime, err := s.resolveMempoolRuntime(ctx, paths)
	if err != nil {
		return err
	}
	for _, variant := range appmanifest.MempoolImageVariants() {
		if handled, err := system.PrepareAppImageWithBroker(ctx, mempoolAppID, string(variant)); !handled {
			return errors.New("Mempool image preparation requires privileged broker enforce mode")
		} else if err != nil {
			return err
		}
		if handled, runnable, err := system.ProbeAppImageWithBroker(ctx, mempoolAppID, string(variant)); !handled {
			return errors.New("Mempool image probe requires privileged broker enforce mode")
		} else if err != nil {
			return err
		} else if !runnable {
			return errors.New("Mempool image failed the closed runtime probe")
		}
	}
	env, err := appmanifest.MempoolRuntimeEnv(runtime)
	if err != nil {
		return err
	}
	if err := writeFile(paths.EnvPath, env, 0600); err != nil {
		return fmt.Errorf("failed to write Mempool environment: %w", err)
	}
	if err := os.Chmod(paths.EnvPath, 0600); err != nil {
		return fmt.Errorf("failed to secure Mempool environment: %w", err)
	}
	compose, err := appmanifest.MempoolCompose(runtime)
	if err != nil {
		return err
	}
	if _, err := ensureFileWithChange(paths.ComposePath, compose); err != nil {
		return err
	}
	if handled, err := system.AppLifecycleWithBroker(ctx, mempoolAppID, "start"); !handled {
		return errors.New("Mempool lifecycle requires privileged broker enforce mode")
	} else if err != nil {
		return fmt.Errorf("Mempool start failed: %w", err)
	}
	if handled, _, err := system.EnsureAppFirewallWithBroker(ctx, mempoolAppID); !handled {
		return errors.New("Mempool firewall requires privileged broker enforce mode")
	} else if err != nil && s.logger != nil {
		s.logger.Printf("mempool: broker firewall rule failed: %v", err)
	}
	return nil
}

func (s *Server) resolveMempoolRuntime(ctx context.Context, paths mempoolPaths) (appmanifest.MempoolRuntime, error) {
	localCfg, err := readBitcoinLocalRPCConfig(ctx)
	if err != nil {
		return appmanifest.MempoolRuntime{}, fmt.Errorf("local bitcoin RPC unavailable: %w", err)
	}
	if strings.TrimSpace(localCfg.User) == "" || strings.TrimSpace(localCfg.Pass) == "" {
		return appmanifest.MempoolRuntime{}, errors.New("local bitcoin RPC credentials missing")
	}
	_, rpcPort := parseMainchainRPC(localCfg.Host)
	if rpcPort != 8332 {
		return appmanifest.MempoolRuntime{}, errors.New("Mempool requires the catalog Bitcoin mainnet RPC port")
	}
	mode := appmanifest.MempoolBitcoinModeApp
	if !fileExists(bitcoinCoreAppPaths().ComposePath) {
		if !isLocalRPCHost(localCfg.Host) {
			return appmanifest.MempoolRuntime{}, fmt.Errorf("local bitcoin RPC host is not local: %s", localCfg.Host)
		}
		if err := ensureLocalExternalBitcoinConsumerNetwork(ctx); err != nil {
			return appmanifest.MempoolRuntime{}, err
		}
		mode = appmanifest.MempoolBitcoinModeNative
	}
	runtime := appmanifest.MempoolRuntime{
		BitcoinMode: mode, Network: "bitcoin", BitcoinRPCUser: localCfg.User, BitcoinRPCPass: localCfg.Pass,
	}
	// Canonical 0.5.3 environments are preferred. The two legacy database
	// keys are read only as a one-time migration so an existing MariaDB volume
	// remains usable after the broker takes ownership of execution.
	if raw, readErr := os.ReadFile(paths.EnvPath); readErr == nil {
		if existing, parseErr := appmanifest.ParseMempoolRuntimeEnv(raw); parseErr == nil {
			runtime.DBPassword = existing.DBPassword
			runtime.DBRootPassword = existing.DBRootPassword
		} else {
			runtime.DBPassword = strings.TrimSpace(readEnvValue(paths.EnvPath, "MEMPOOL_DB_PASSWORD"))
			runtime.DBRootPassword = strings.TrimSpace(readEnvValue(paths.EnvPath, "MEMPOOL_DB_ROOT_PASSWORD"))
		}
	}
	if runtime.DBPassword == "" {
		runtime.DBPassword, err = randomToken(24)
		if err != nil {
			return appmanifest.MempoolRuntime{}, fmt.Errorf("failed to generate database credential: %w", err)
		}
	}
	if runtime.DBRootPassword == "" {
		runtime.DBRootPassword, err = randomToken(24)
		if err != nil {
			return appmanifest.MempoolRuntime{}, fmt.Errorf("failed to generate database root credential: %w", err)
		}
	}
	if err := appmanifest.ValidateMempoolRuntime(runtime); err != nil {
		return appmanifest.MempoolRuntime{}, err
	}
	return runtime, nil
}

func mempoolComposeContents(_ mempoolPaths, runtime appmanifest.MempoolRuntime) string {
	compose, _ := appmanifest.MempoolCompose(runtime)
	return compose
}
