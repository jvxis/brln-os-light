//go:build linux

package privileged

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lightningos-light/internal/appmanifest"
)

// TestElectrsFunctionalGate is an opt-in destructive integration test for a
// disposable Linux host. It refuses to run if any fixed Docker resource it
// would own already exists, builds the attested image through the broker,
// starts an isolated unpruned txindex regtest node, exercises the real Compose
// lifecycle, and restores every resource it created.
func TestElectrsFunctionalGate(t *testing.T) {
	if os.Getenv("LIGHTNINGOS_ELECTRS_FUNCTIONAL_GATE") != "1" {
		t.Skip("set LIGHTNINGOS_ELECTRS_FUNCTIONAL_GATE=1 on a disposable Linux host")
	}
	if os.Geteuid() != 0 {
		t.Fatal("Electrs functional gate requires root on a disposable host")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	runner := &ExecCommandRunner{}
	const bitcoinContainer = "lightningos-electrs-gate-bitcoind"
	const bitcoinImage = appmanifest.BitcoinCoreImage

	for _, check := range []struct {
		args []string
		name string
	}{
		{args: []string{"container", "inspect", bitcoinContainer}, name: bitcoinContainer},
		{args: []string{"container", "inspect", appmanifest.ElectrsID}, name: appmanifest.ElectrsID},
		{args: []string{"network", "inspect", appmanifest.BitcoinConsumerNetwork}, name: appmanifest.BitcoinConsumerNetwork},
		{args: []string{"network", "inspect", "electrs_default"}, name: "electrs_default"},
		{args: []string{"volume", "inspect", appmanifest.ElectrsVolume}, name: appmanifest.ElectrsVolume},
		{args: []string{"image", "inspect", appmanifest.ElectrsImage}, name: appmanifest.ElectrsImage},
	} {
		if _, err := runner.Run(ctx, dockerPath, check.args...); err == nil {
			t.Fatalf("functional gate resource %s already exists", check.name)
		}
	}
	if _, err := runner.Run(ctx, dockerPath, "image", "inspect", bitcoinImage); err != nil {
		t.Fatal("verified LightningOS Bitcoin Core image is unavailable")
	}

	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, _ = runner.Run(cleanupCtx, dockerPath, "rm", "-f", appmanifest.ElectrsID)
		_, _ = runner.Run(cleanupCtx, dockerPath, "rm", "-f", bitcoinContainer)
		_, _ = runner.Run(cleanupCtx, dockerPath, "network", "rm", "electrs_default")
		_, _ = runner.Run(cleanupCtx, dockerPath, "network", "rm", appmanifest.BitcoinConsumerNetwork)
		_, _ = runner.Run(cleanupCtx, dockerPath, "volume", "rm", appmanifest.ElectrsVolume)
		_, _ = runner.Run(cleanupCtx, dockerPath, "image", "rm", appmanifest.ElectrsImage)
	}
	t.Cleanup(cleanup)

	stateRoot := t.TempDir()
	appsRoot := filepath.Join(stateRoot, "manager-apps")
	privilegedRoot := filepath.Join(stateRoot, "broker-apps")
	manager := &ComposeAppManager{
		Runner:             runner,
		AppsRoot:           appsRoot,
		PrivilegedAppsRoot: privilegedRoot,
		TempRoot:           stateRoot,
	}

	state, err := manager.PrepareImage(ctx, appmanifest.ElectrsID, appmanifest.ElectrsImageApp, false)
	if err != nil || (state.Status != "ready" && state.Status != "preparing") {
		t.Fatalf("Electrs image preparation start failed: state=%s err=%v", state.Status, err)
	}
	if state.Status != "ready" {
		if err := waitElectrsGate(ctx, 2*time.Second, func() (bool, error) {
			current, err := manager.ImageStatus(ctx, appmanifest.ElectrsID, appmanifest.ElectrsImageApp)
			if err != nil {
				return false, err
			}
			switch current.Status {
			case "ready":
				return true, nil
			case "preparing":
				return false, nil
			default:
				return false, fmt.Errorf("Electrs image entered state %s", current.Status)
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	imageID, err := runner.Run(ctx, dockerPath, "image", "inspect", "--format", "{{.Id}}", appmanifest.ElectrsImage)
	if err != nil {
		t.Fatal("attested Electrs image inspection failed")
	}
	attestation, err := readElectrsImageAttestation(filepath.Join(privilegedRoot, appmanifest.ElectrsID, electrsImageAttestationFile))
	if err != nil || strings.TrimSpace(imageID) != attestation.ImageID {
		t.Fatalf("Electrs image attestation mismatch: %v", err)
	}
	t.Logf("electrs_image_id=%s", attestation.ImageID)

	if _, err := runner.Run(ctx, dockerPath, "network", "create",
		"--subnet", appmanifest.BitcoinConsumerRPCSubnet,
		"--gateway", appmanifest.BitcoinConsumerHostGateway,
		appmanifest.BitcoinConsumerNetwork,
	); err != nil {
		t.Fatal("isolated Bitcoin consumer network creation failed")
	}
	rpcUser := "electrs-gate"
	rpcPassword, rpcAuth, err := electrsGateRPCAuth(rpcUser)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, dockerPath, "run", "-d",
		"--name", bitcoinContainer,
		"--network", appmanifest.BitcoinConsumerNetwork,
		"--network-alias", "bitcoind",
		"-p", "127.0.0.1:18443:18443",
		bitcoinImage,
		"-regtest=1",
		"-server=1",
		"-txindex=1",
		"-prune=0",
		"-listen=1",
		"-port=18444",
		"-rpcport=18443",
		"-rpcbind=0.0.0.0:18443",
		"-rpcallowip="+appmanifest.BitcoinConsumerRPCSubnet,
		"-rpcauth="+rpcAuth,
		"-fallbackfee=0.00001",
	); err != nil {
		t.Fatal("isolated Bitcoin regtest start failed")
	}
	if err := waitElectrsGate(ctx, 500*time.Millisecond, func() (bool, error) {
		_, err := runner.Run(ctx, dockerPath, "exec", bitcoinContainer, "bitcoin-cli", "-regtest", "getblockchaininfo")
		return err == nil, nil
	}); err != nil {
		t.Fatal("isolated Bitcoin RPC did not become ready")
	}
	const bitcoinWallet = "electrs-gate"
	if _, err := runner.Run(ctx, dockerPath, "exec", bitcoinContainer, "bitcoin-cli", "-regtest", "createwallet", bitcoinWallet); err != nil {
		t.Fatal("isolated Bitcoin wallet creation failed")
	}
	address, err := runner.Run(ctx, dockerPath, "exec", bitcoinContainer, "bitcoin-cli", "-regtest", "-rpcwallet="+bitcoinWallet, "getnewaddress")
	if err != nil || strings.TrimSpace(address) == "" {
		t.Fatal("isolated Bitcoin address generation failed")
	}
	if _, err := runner.Run(ctx, dockerPath, "exec", bitcoinContainer, "bitcoin-cli", "-regtest", "generatetoaddress", "101", strings.TrimSpace(address)); err != nil {
		t.Fatal("isolated Bitcoin block generation failed")
	}

	runtime := appmanifest.ElectrsRuntime{BitcoinMode: appmanifest.ElectrsBitcoinModeApp, Network: "regtest"}
	appRoot := filepath.Join(appsRoot, appmanifest.ElectrsID)
	if err := os.MkdirAll(appRoot, 0750); err != nil {
		t.Fatal(err)
	}
	env, _ := appmanifest.ElectrsRuntimeEnv(runtime)
	compose, _ := appmanifest.ElectrsCompose(runtime)
	for _, file := range []struct {
		name string
		data string
	}{
		{name: appmanifest.ElectrsEnvFile, data: env},
		{name: appmanifest.ElectrsComposeFile, data: compose},
		{name: appmanifest.ElectrsCookieFile, data: rpcUser + ":" + rpcPassword},
	} {
		if err := os.WriteFile(filepath.Join(appRoot, file.name), []byte(file.data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	indexCtx, indexCancel := context.WithTimeout(ctx, 30*time.Second)
	defer indexCancel()
	if err := waitElectrsGate(indexCtx, 250*time.Millisecond, func() (bool, error) {
		err := manager.validateElectrsBitcoin(indexCtx, runtime, []byte(rpcUser+":"+rpcPassword))
		if err == nil {
			return true, nil
		}
		if err.Error() == "Electrs requires a fully synchronized Bitcoin txindex" {
			return false, nil
		}
		return false, err
	}); err != nil {
		t.Fatalf("isolated Full Node contract failed: %v", err)
	}
	if err := manager.Lifecycle(ctx, appmanifest.ElectrsID, AppLifecycleStart, false); err != nil {
		t.Fatalf("Electrs start failed: %v", err)
	}
	if err := waitElectrsGate(ctx, time.Second, func() (bool, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:4224/metrics", nil)
		if err != nil {
			return false, err
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false, nil
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
		return response.StatusCode == http.StatusOK && bytesContainElectrsTip(body), nil
	}); err != nil {
		t.Fatal("Electrs metrics did not become ready")
	}
	if err := electrsGateRPC(ctx); err != nil {
		t.Fatalf("Electrum RPC gate failed: %v", err)
	}
	inspection, err := manager.Inspect(ctx, appmanifest.ElectrsID)
	if err != nil || inspection.Status != "running" {
		t.Fatalf("Electrs running inspection failed: %#v/%v", inspection, err)
	}
	if err := manager.Lifecycle(ctx, appmanifest.ElectrsID, AppLifecycleStop, false); err != nil {
		t.Fatalf("Electrs stop failed: %v", err)
	}
	inspection, err = manager.Inspect(ctx, appmanifest.ElectrsID)
	if err != nil || inspection.Status != "stopped" {
		t.Fatalf("Electrs stopped inspection failed: %#v/%v", inspection, err)
	}
	if err := manager.Lifecycle(ctx, appmanifest.ElectrsID, AppLifecycleStart, false); err != nil {
		t.Fatalf("Electrs restart-through-start failed: %v", err)
	}
	if err := waitElectrsGate(ctx, time.Second, func() (bool, error) {
		return electrsGateRPC(ctx) == nil, nil
	}); err != nil {
		t.Fatal("Electrs did not recover after stop/start")
	}
	if err := manager.Remove(ctx, appmanifest.ElectrsID, false); err != nil {
		t.Fatalf("Electrs remove failed: %v", err)
	}
	if _, err := runner.Run(ctx, dockerPath, "volume", "inspect", appmanifest.ElectrsVolume); err == nil {
		t.Fatal("Electrs reproducible index volume survived catalog removal")
	}
	if _, err := readElectrsImageAttestation(filepath.Join(privilegedRoot, appmanifest.ElectrsID, electrsImageAttestationFile)); err != nil {
		t.Fatalf("Electrs image attestation did not survive lifecycle removal: %v", err)
	}
	t.Log("electrs_functional_lifecycle=passed bitcoin_regtest_blocks=101 pruned=false txindex_synced=true")
}

func electrsGateRPCAuth(user string) (password string, rpcauth string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	password = hex.EncodeToString(raw)
	if _, err := rand.Read(raw[:16]); err != nil {
		return "", "", err
	}
	salt := hex.EncodeToString(raw[:16])
	digest := hmac.New(sha256.New, []byte(salt))
	_, _ = digest.Write([]byte(password))
	return password, user + ":" + salt + "$" + hex.EncodeToString(digest.Sum(nil)), nil
}

func waitElectrsGate(ctx context.Context, interval time.Duration, check func() (bool, error)) error {
	for {
		ready, err := check()
		if err != nil || ready {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func bytesContainElectrsTip(raw []byte) bool {
	return strings.Contains(string(raw), `electrs_index_height{type="tip"}`)
}

func electrsGateRPC(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:50001")
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(connection, "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"blockchain.headers.subscribe\",\"params\":[]}\n"); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(connection, 64*1024)).ReadString('\n')
	if err != nil {
		return err
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &response); err != nil || len(response.Result) == 0 || (len(response.Error) > 0 && string(response.Error) != "null") {
		return errors.New("invalid Electrum response")
	}
	return nil
}
