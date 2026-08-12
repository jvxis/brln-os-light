//go:build linux

package privileged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

func TestBitcoinCoreGeneratedRPCAuthFunctionalGate(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_BITCOIN_RPCAUTH_GATE") != "1" {
		t.Skip("set LIGHTNINGOS_BITCOIN_RPCAUTH_GATE=1 on a disposable Linux host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("Bitcoin rpcauth functional gate requires root on a disposable host")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	runner := &ExecCommandRunner{}
	const container = "lightningos-bitcoincore-rpcauth-gate"
	if _, err := runner.Run(ctx, dockerPath, "container", "inspect", container); err == nil {
		t.Fatalf("functional gate container %s already exists", container)
	}
	if _, err := runner.Run(ctx, dockerPath, "image", "inspect", appmanifest.BitcoinCoreImage); err != nil {
		t.Fatal("verified LightningOS Bitcoin Core image is unavailable")
	}

	_, _, privilegedRoot, dataDir := newTestBitcoinCoreLifecycleManager(t)
	if err := os.Remove(filepath.Join(dataDir, bitcoinCoreConfigFile)); err != nil {
		t.Fatal(err)
	}
	manager := &BitcoinCoreConfigManager{PrivilegedAppsRoot: privilegedRoot}
	const template = "server=1\nregtest=1\nprinttoconsole=1\n[regtest]\nrpcport=18443\nrpcbind=0.0.0.0:18443\nrpcallowip=0.0.0.0/0\n"
	if state, err := manager.Ensure(ctx, dataDir, template, true, false); err != nil || state.Status != "ready" {
		t.Fatalf("generated config state/error=%#v/%v", state, err)
	}
	credentials, err := manager.Credentials(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = runner.Run(cleanupCtx, dockerPath, "rm", "-f", container)
	}
	t.Cleanup(cleanup)
	if _, err := runner.Run(ctx, dockerPath, "run", "-d",
		"--name", container,
		"-p", "127.0.0.1:18445:18443",
		"-v", dataDir+":"+appmanifest.BitcoinCoreContainerDataDir,
		appmanifest.BitcoinCoreImage,
	); err != nil {
		t.Fatal("isolated Bitcoin rpcauth container failed to start")
	}

	var chain string
	for {
		chain, err = bitcoinRPCAuthGateCall(ctx, credentials.User, credentials.Password)
		if err == nil {
			break
		}
		if !errors.Is(err, errBitcoinRPCAuthGateNotReady) {
			t.Fatal(err)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			logs, _ := runner.Run(context.Background(), dockerPath, "logs", "--tail", "80", container)
			t.Fatalf("isolated Bitcoin rpcauth RPC did not become ready: %s", strings.TrimSpace(logs))
		case <-timer.C:
		}
	}
	if chain != "regtest" {
		t.Fatalf("unexpected Bitcoin chain %q", chain)
	}
	if _, err := bitcoinRPCAuthGateCall(ctx, credentials.User, credentials.Password+"wrong"); !errors.Is(err, errBitcoinRPCAuthGateUnauthorized) {
		t.Fatalf("wrong Bitcoin RPC password was not rejected: %v", err)
	}
	t.Log("bitcoin_generated_rpcauth_functional_gate=passed chain=regtest wrong_password=rejected")
}

var (
	errBitcoinRPCAuthGateNotReady     = errors.New("bitcoin RPC is not ready")
	errBitcoinRPCAuthGateUnauthorized = errors.New("bitcoin RPC credentials were rejected")
)

func bitcoinRPCAuthGateCall(ctx context.Context, user string, password string) (string, error) {
	requestBody := []byte(`{"jsonrpc":"1.0","id":"lightningos-rpcauth-gate","method":"getblockchaininfo","params":[]}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:18445/", bytes.NewReader(requestBody))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(user, password)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", errBitcoinRPCAuthGateNotReady
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized {
		return "", errBitcoinRPCAuthGateUnauthorized
	}
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4*1024))
		return "", errBitcoinRPCAuthGateNotReady
	}
	var payload struct {
		Result struct {
			Chain string `json:"chain"`
		} `json:"result"`
		Error any `json:"error"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	if err := decoder.Decode(&payload); err != nil || payload.Error != nil || payload.Result.Chain == "" {
		return "", errors.New("invalid Bitcoin RPC gate response")
	}
	return payload.Result.Chain, nil
}

func TestBitcoinCoreConfigEnsureGeneratesProtectedRPCAuth(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate Bitcoin storage ownership")
	}
	_, _, privilegedRoot, dataDir := newTestBitcoinCoreLifecycleManager(t)
	configPath := filepath.Join(dataDir, bitcoinCoreConfigFile)
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}

	manager := &BitcoinCoreConfigManager{PrivilegedAppsRoot: privilegedRoot}
	const template = "server=1\nrpcbind=0.0.0.0:8332\n"
	state, err := manager.Ensure(context.Background(), dataDir, template, true, false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("ensure state/error=%#v/%v", state, err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	if !strings.Contains(config, "rpcauth="+appmanifest.BitcoinCoreRPCUser+":") ||
		strings.Contains(config, "rpcpassword=") || strings.Contains(config, "rpcuser=") {
		t.Fatalf("generated bitcoin.conf did not contain only rpcauth:\n%s", config)
	}
	var configStat os.FileInfo
	if configStat, err = os.Stat(configPath); err != nil || configStat.Mode().Perm() != 0o640 {
		t.Fatalf("bitcoin.conf mode/error=%v/%v", configStat, err)
	}

	credentialPath := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID, bitcoinCoreCredentialsFile)
	if err := validateRootOwnedRegularFile(credentialPath, 0o600); err != nil {
		t.Fatalf("credential file is not root-only: %v", err)
	}
	credentials, err := manager.Credentials(context.Background(), dataDir)
	if err != nil || credentials.Status != "ready" || credentials.User != appmanifest.BitcoinCoreRPCUser || len(credentials.Password) != 64 {
		t.Fatalf("credentials state/error=%#v/%v", credentials, err)
	}

	beforeConfig := config
	beforePassword := credentials.Password
	state, err = manager.Ensure(context.Background(), dataDir, "server=0\n", true, false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("idempotent ensure state/error=%#v/%v", state, err)
	}
	afterRaw, err := os.ReadFile(configPath)
	if err != nil || string(afterRaw) != beforeConfig {
		t.Fatalf("idempotent ensure changed config: %v", err)
	}
	afterCredentials, err := manager.Credentials(context.Background(), dataDir)
	if err != nil || afterCredentials.Password != beforePassword {
		t.Fatalf("idempotent ensure rotated credentials: %v", err)
	}
}

func TestBitcoinCoreConfigEnsurePreservesExistingLegacyAuth(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to validate Bitcoin storage ownership")
	}
	_, _, privilegedRoot, dataDir := newTestBitcoinCoreLifecycleManager(t)
	configPath := filepath.Join(dataDir, bitcoinCoreConfigFile)
	const legacy = "server=1\nrpcuser=legacy\nrpcpassword=preserved\n"
	if err := os.WriteFile(configPath, []byte(legacy), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(configPath, 0, 101); err != nil {
		t.Fatal(err)
	}

	manager := &BitcoinCoreConfigManager{PrivilegedAppsRoot: privilegedRoot}
	state, err := manager.Ensure(context.Background(), dataDir, "server=1\n", true, false)
	if err != nil || state.Status != "ready" {
		t.Fatalf("ensure state/error=%#v/%v", state, err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil || string(raw) != legacy {
		t.Fatalf("existing config was changed: %v", err)
	}
	credentialPath := filepath.Join(privilegedRoot, appmanifest.BitcoinCoreID, bitcoinCoreCredentialsFile)
	if _, err := os.Lstat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("credentials were unexpectedly generated for a legacy config: %v", err)
	}
}
