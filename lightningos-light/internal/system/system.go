package system

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/appmanifest"
)

const installedManagerConfigPath = "/etc/lightningos/config.yaml"

type PrivilegedClient interface {
	Mode() string
	RestartService(ctx context.Context, unit string, noBlock bool, dryRun bool) error
	EnableLogin(ctx context.Context, dryRun bool) error
	AppLifecycle(ctx context.Context, appID string, action string, dryRun bool) error
	InspectApp(ctx context.Context, appID string) (status string, cpuPercentRaw float64, err error)
	RemoveApp(ctx context.Context, appID string, dryRun bool) error
	EnsureDockerRuntime(ctx context.Context, dryRun bool) (status string, err error)
	DockerRuntimeStatus(ctx context.Context) (status string, err error)
	EnsurePackageFeature(ctx context.Context, feature string, dryRun bool) (status string, err error)
	PackageFeatureStatus(ctx context.Context, feature string) (status string, err error)
	PrepareAppImage(ctx context.Context, appID string, variant string, dryRun bool) (status string, err error)
	AppImageStatus(ctx context.Context, appID string, variant string) (status string, err error)
	ProbeAppImage(ctx context.Context, appID string, variant string, dryRun bool) (runnable bool, err error)
	EnsureAppFirewall(ctx context.Context, appID string, dryRun bool) (status string, err error)
	EnsureBitcoinCoreStorage(ctx context.Context, dataDir string, dryRun bool) (status string, err error)
}

type bitcoinCoreConfigPrivilegedClient interface {
	EnsureBitcoinCoreConfig(ctx context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (status string, err error)
	ReadBitcoinCoreConfig(ctx context.Context, dataDir string) (content string, err error)
	WriteBitcoinCoreConfig(ctx context.Context, dataDir string, content string, dryRun bool) (status string, err error)
	ReadBitcoinCoreCredentials(ctx context.Context, dataDir string) (user string, password string, err error)
}

type bitcoinCoreStatusPrivilegedClient interface {
	BitcoinCoreStatus(ctx context.Context) (statusJSON string, err error)
}

type bitcoinConsumerNetworkPrivilegedClient interface {
	EnsureBitcoinConsumerNetwork(ctx context.Context, dryRun bool) (status string, err error)
}

type appSnapshotPrivilegedClient interface {
	SnapshotApp(ctx context.Context, appID string, dryRun bool) error
}

type appAdminResetPrivilegedClient interface {
	ResetAppAdmin(ctx context.Context, appID string, dryRun bool) error
}

type appLogsPrivilegedClient interface {
	AppLogs(ctx context.Context, appID string, lines int, since string) ([]string, string, error)
}

type appLNDHostAccessPrivilegedClient interface {
	EnsureAppLNDHostAccess(ctx context.Context, appID string, dryRun bool) error
}

type managerFirewallPrivilegedClient interface {
	ManagerFirewallStatus(ctx context.Context) (statusJSON string, err error)
}

type loopPrivilegedClient interface {
	LoopStatus(ctx context.Context) (installed bool, status string, hasLNDMacaroon bool, hasPersistentState bool, err error)
	EnsureLoop(ctx context.Context, tlsCertificate, macaroon []byte, dryRun bool) (status string, err error)
	LoopLifecycle(ctx context.Context, action string, dryRun bool) (status string, err error)
	RemoveLoop(ctx context.Context, dryRun bool) error
	EnsureLoopPermissions(ctx context.Context, dryRun bool) error
	EnsureLoopClientMaterial(ctx context.Context, dryRun bool) error
}

type elementsPrivilegedClient interface {
	ElementsStatus(ctx context.Context, dataDir string) (statusJSON string, err error)
	ReadElementsConfig(ctx context.Context, dataDir string) (content string, err error)
	EnsureElements(ctx context.Context, dataDir string, content string, dryRun bool) (status string, err error)
	ElementsLifecycle(ctx context.Context, dataDir string, action string, dryRun bool) (status string, err error)
	RemoveElements(ctx context.Context, dataDir string, dryRun bool) error
}

type peerSwapPrivilegedClient interface {
	PeerSwapStatus(ctx context.Context) (installed bool, status string, hasLNDMacaroon bool, elementsMode string, err error)
	ReadPeerSwapSource(ctx context.Context) (configured bool, mode string, rawURL string, user string, password string, wallet string, err error)
	WritePeerSwapSource(ctx context.Context, mode string, rawURL string, user string, password string, wallet string, dryRun bool) error
	EnsurePeerSwap(ctx context.Context, elementsMode string, config string, webConfig string, tlsCertificate []byte, macaroon []byte, dryRun bool) (status string, err error)
	PeerSwapLifecycle(ctx context.Context, action string, dryRun bool) (status string, err error)
	RemovePeerSwap(ctx context.Context, dryRun bool) error
}

type tapdPrivilegedClient interface {
	TapdStatus(ctx context.Context) (installed bool, status string, hasLNDMacaroon bool, interceptorConflict bool, err error)
	EnsureTapd(ctx context.Context, databasePassword string, tlsCertificate []byte, macaroon []byte, dryRun bool) (status string, err error)
	TapdLifecycle(ctx context.Context, action string, dryRun bool) (status string, err error)
	RemoveTapd(ctx context.Context, dryRun bool) error
	TapdCLI(ctx context.Context, request appmanifest.TapdCLIRequest) (output string, err error)
}

type publicPoolPrivilegedClient interface {
	PublicPoolStatus(ctx context.Context) (installed bool, status string, ufwActive bool, err error)
	EnsurePublicPool(ctx context.Context, runtime appmanifest.PublicPoolRuntime, dryRun bool) (status string, err error)
	PublicPoolLifecycle(ctx context.Context, action string, dryRun bool) (status string, err error)
	RemovePublicPool(ctx context.Context, dryRun bool) error
	EnsurePublicPoolFirewall(ctx context.Context, dryRun bool) (status string, err error)
}

type barkWalletPrivilegedClient interface {
	BarkWalletStatus(ctx context.Context) (installed bool, status string, ufwActive bool, passwordAvailable bool, err error)
	EnsureBarkWallet(ctx context.Context, dryRun bool) (status string, err error)
	BarkWalletLifecycle(ctx context.Context, action string, dryRun bool) (status string, err error)
	RemoveBarkWallet(ctx context.Context, dryRun bool) error
	EnsureBarkWalletFirewall(ctx context.Context, dryRun bool) (status string, err error)
	ReadBarkWalletPassword(ctx context.Context) (password string, err error)
	ResetBarkWalletPassword(ctx context.Context, dryRun bool) error
}

type PublicPoolBrokerState struct {
	Installed bool
	Status    string
	UFWActive bool
}

func PublicPoolStatusWithBroker(ctx context.Context) (bool, PublicPoolBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, PublicPoolBrokerState{}, nil
	}
	poolClient, ok := client.(publicPoolPrivilegedClient)
	if !ok {
		return true, PublicPoolBrokerState{}, errors.New("privileged broker does not support Public Pool")
	}
	installed, status, active, err := poolClient.PublicPoolStatus(ctx)
	return true, PublicPoolBrokerState{Installed: installed, Status: status, UFWActive: active}, err
}

func EnsurePublicPoolWithBroker(ctx context.Context, runtime appmanifest.PublicPoolRuntime) (bool, error) {
	return publicPoolMutationWithBroker(ctx, func(callCtx context.Context, client publicPoolPrivilegedClient, dryRun bool) error {
		_, err := client.EnsurePublicPool(callCtx, runtime, dryRun)
		return err
	})
}
func PublicPoolLifecycleWithBroker(ctx context.Context, action string) (bool, error) {
	return publicPoolMutationWithBroker(ctx, func(callCtx context.Context, client publicPoolPrivilegedClient, dryRun bool) error {
		_, err := client.PublicPoolLifecycle(callCtx, action, dryRun)
		return err
	})
}
func RemovePublicPoolWithBroker(ctx context.Context) (bool, error) {
	return publicPoolMutationWithBroker(ctx, func(callCtx context.Context, client publicPoolPrivilegedClient, dryRun bool) error {
		return client.RemovePublicPool(callCtx, dryRun)
	})
}
func EnsurePublicPoolFirewallWithBroker(ctx context.Context) (bool, error) {
	return publicPoolMutationWithBroker(ctx, func(callCtx context.Context, client publicPoolPrivilegedClient, dryRun bool) error {
		_, err := client.EnsurePublicPoolFirewall(callCtx, dryRun)
		return err
	})
}
func publicPoolMutationWithBroker(ctx context.Context, operation func(context.Context, publicPoolPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	poolClient, ok := client.(publicPoolPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support Public Pool")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, poolClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, poolClient, true)
		return false, nil
	default:
		return false, nil
	}
}

type BarkWalletBrokerState struct {
	Installed         bool
	Status            string
	UFWActive         bool
	PasswordAvailable bool
}

func BarkWalletStatusWithBroker(ctx context.Context) (bool, BarkWalletBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, BarkWalletBrokerState{}, nil
	}
	barkClient, ok := client.(barkWalletPrivilegedClient)
	if !ok {
		return true, BarkWalletBrokerState{}, errors.New("privileged broker does not support Bark Wallet")
	}
	installed, status, ufwActive, passwordAvailable, err := barkClient.BarkWalletStatus(ctx)
	return true, BarkWalletBrokerState{Installed: installed, Status: status, UFWActive: ufwActive, PasswordAvailable: passwordAvailable}, err
}

func EnsureBarkWalletWithBroker(ctx context.Context) (bool, error) {
	return barkWalletMutationWithBroker(ctx, func(callCtx context.Context, client barkWalletPrivilegedClient, dryRun bool) error {
		_, err := client.EnsureBarkWallet(callCtx, dryRun)
		return err
	})
}

func BarkWalletLifecycleWithBroker(ctx context.Context, action string) (bool, error) {
	return barkWalletMutationWithBroker(ctx, func(callCtx context.Context, client barkWalletPrivilegedClient, dryRun bool) error {
		_, err := client.BarkWalletLifecycle(callCtx, action, dryRun)
		return err
	})
}

func RemoveBarkWalletWithBroker(ctx context.Context) (bool, error) {
	return barkWalletMutationWithBroker(ctx, func(callCtx context.Context, client barkWalletPrivilegedClient, dryRun bool) error {
		return client.RemoveBarkWallet(callCtx, dryRun)
	})
}

func EnsureBarkWalletFirewallWithBroker(ctx context.Context) (bool, error) {
	return barkWalletMutationWithBroker(ctx, func(callCtx context.Context, client barkWalletPrivilegedClient, dryRun bool) error {
		_, err := client.EnsureBarkWalletFirewall(callCtx, dryRun)
		return err
	})
}

func ResetBarkWalletPasswordWithBroker(ctx context.Context) (bool, error) {
	return barkWalletMutationWithBroker(ctx, func(callCtx context.Context, client barkWalletPrivilegedClient, dryRun bool) error {
		return client.ResetBarkWalletPassword(callCtx, dryRun)
	})
}

func ReadBarkWalletPasswordWithBroker(ctx context.Context) (bool, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, "", nil
	}
	barkClient, ok := client.(barkWalletPrivilegedClient)
	if !ok {
		return true, "", errors.New("privileged broker does not support Bark Wallet")
	}
	password, err := barkClient.ReadBarkWalletPassword(ctx)
	return true, password, err
}

func barkWalletMutationWithBroker(ctx context.Context, operation func(context.Context, barkWalletPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	barkClient, ok := client.(barkWalletPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support Bark Wallet")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, barkClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, barkClient, true)
		return false, nil
	default:
		return false, nil
	}
}

type TapdBrokerState struct {
	Installed           bool
	Status              string
	HasLNDMacaroon      bool
	InterceptorConflict bool
}

func TapdStatusWithBroker(ctx context.Context) (bool, TapdBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, TapdBrokerState{}, nil
	}
	tapdClient, ok := client.(tapdPrivilegedClient)
	if !ok {
		return true, TapdBrokerState{}, errors.New("privileged broker does not support Tapd")
	}
	installed, status, hasMacaroon, conflict, err := tapdClient.TapdStatus(ctx)
	return true, TapdBrokerState{Installed: installed, Status: status, HasLNDMacaroon: hasMacaroon, InterceptorConflict: conflict}, err
}

func EnsureTapdWithBroker(ctx context.Context, databasePassword string, tlsCertificate, macaroon []byte) (bool, error) {
	return tapdMutationWithBroker(ctx, func(callCtx context.Context, client tapdPrivilegedClient, dryRun bool) error {
		_, err := client.EnsureTapd(callCtx, databasePassword, tlsCertificate, macaroon, dryRun)
		return err
	})
}

func TapdLifecycleWithBroker(ctx context.Context, action string) (bool, error) {
	return tapdMutationWithBroker(ctx, func(callCtx context.Context, client tapdPrivilegedClient, dryRun bool) error {
		_, err := client.TapdLifecycle(callCtx, action, dryRun)
		return err
	})
}

func RemoveTapdWithBroker(ctx context.Context) (bool, error) {
	return tapdMutationWithBroker(ctx, func(callCtx context.Context, client tapdPrivilegedClient, dryRun bool) error {
		return client.RemoveTapd(callCtx, dryRun)
	})
}

func TapdCLIWithBroker(ctx context.Context, request appmanifest.TapdCLIRequest) (bool, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, "", nil
	}
	tapdClient, ok := client.(tapdPrivilegedClient)
	if !ok {
		return true, "", errors.New("privileged broker does not support Tapd")
	}
	output, err := tapdClient.TapdCLI(ctx, request)
	return true, output, err
}

func tapdMutationWithBroker(ctx context.Context, operation func(context.Context, tapdPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	tapdClient, ok := client.(tapdPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support Tapd")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, tapdClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, tapdClient, true)
		return false, nil
	default:
		return false, nil
	}
}

type PeerSwapBrokerState struct {
	Installed      bool
	Status         string
	HasLNDMacaroon bool
	ElementsMode   string
}

type PeerSwapBrokerSource struct {
	Configured bool
	Mode       string
	URL        string
	User       string
	Password   string
	Wallet     string
}

func PeerSwapStatusWithBroker(ctx context.Context) (bool, PeerSwapBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, PeerSwapBrokerState{}, nil
	}
	peerSwapClient, ok := client.(peerSwapPrivilegedClient)
	if !ok {
		return true, PeerSwapBrokerState{}, errors.New("privileged broker does not support PeerSwap")
	}
	installed, status, hasMacaroon, elementsMode, err := peerSwapClient.PeerSwapStatus(ctx)
	return true, PeerSwapBrokerState{Installed: installed, Status: status, HasLNDMacaroon: hasMacaroon, ElementsMode: elementsMode}, err
}

func ReadPeerSwapSourceWithBroker(ctx context.Context) (bool, PeerSwapBrokerSource, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, PeerSwapBrokerSource{}, nil
	}
	peerSwapClient, ok := client.(peerSwapPrivilegedClient)
	if !ok {
		return true, PeerSwapBrokerSource{}, errors.New("privileged broker does not support PeerSwap")
	}
	configured, mode, rawURL, user, password, wallet, err := peerSwapClient.ReadPeerSwapSource(ctx)
	return true, PeerSwapBrokerSource{Configured: configured, Mode: mode, URL: rawURL, User: user, Password: password, Wallet: wallet}, err
}

func WritePeerSwapSourceWithBroker(ctx context.Context, source PeerSwapBrokerSource) (bool, error) {
	return peerSwapMutationWithBroker(ctx, func(callCtx context.Context, client peerSwapPrivilegedClient, dryRun bool) error {
		return client.WritePeerSwapSource(callCtx, source.Mode, source.URL, source.User, source.Password, source.Wallet, dryRun)
	})
}

func EnsurePeerSwapWithBroker(ctx context.Context, elementsMode, config, webConfig string, tlsCertificate, macaroon []byte) (bool, error) {
	return peerSwapMutationWithBroker(ctx, func(callCtx context.Context, client peerSwapPrivilegedClient, dryRun bool) error {
		_, err := client.EnsurePeerSwap(callCtx, elementsMode, config, webConfig, tlsCertificate, macaroon, dryRun)
		return err
	})
}

func PeerSwapLifecycleWithBroker(ctx context.Context, action string) (bool, error) {
	return peerSwapMutationWithBroker(ctx, func(callCtx context.Context, client peerSwapPrivilegedClient, dryRun bool) error {
		_, err := client.PeerSwapLifecycle(callCtx, action, dryRun)
		return err
	})
}

func RemovePeerSwapWithBroker(ctx context.Context) (bool, error) {
	return peerSwapMutationWithBroker(ctx, func(callCtx context.Context, client peerSwapPrivilegedClient, dryRun bool) error {
		return client.RemovePeerSwap(callCtx, dryRun)
	})
}

func peerSwapMutationWithBroker(ctx context.Context, operation func(context.Context, peerSwapPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	peerSwapClient, ok := client.(peerSwapPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support PeerSwap")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, peerSwapClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, peerSwapClient, true)
		return false, nil
	default:
		return false, nil
	}
}

func ElementsStatusWithBroker(ctx context.Context, dataDir string) (bool, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, "", nil
	}
	elementsClient, ok := client.(elementsPrivilegedClient)
	if !ok {
		return true, "", errors.New("privileged broker does not support Elements")
	}
	raw, err := elementsClient.ElementsStatus(ctx, dataDir)
	return true, raw, err
}

func ReadElementsConfigWithBroker(ctx context.Context, dataDir string) (bool, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, "", nil
	}
	elementsClient, ok := client.(elementsPrivilegedClient)
	if !ok {
		return true, "", errors.New("privileged broker does not support Elements")
	}
	content, err := elementsClient.ReadElementsConfig(ctx, dataDir)
	return true, content, err
}

func EnsureElementsWithBroker(ctx context.Context, dataDir, content string) (bool, error) {
	return elementsMutationWithBroker(ctx, func(callCtx context.Context, client elementsPrivilegedClient, dryRun bool) error {
		_, err := client.EnsureElements(callCtx, dataDir, content, dryRun)
		return err
	})
}

func ElementsLifecycleWithBroker(ctx context.Context, dataDir, action string) (bool, error) {
	return elementsMutationWithBroker(ctx, func(callCtx context.Context, client elementsPrivilegedClient, dryRun bool) error {
		_, err := client.ElementsLifecycle(callCtx, dataDir, action, dryRun)
		return err
	})
}

func RemoveElementsWithBroker(ctx context.Context, dataDir string) (bool, error) {
	return elementsMutationWithBroker(ctx, func(callCtx context.Context, client elementsPrivilegedClient, dryRun bool) error {
		return client.RemoveElements(callCtx, dataDir, dryRun)
	})
}

func RestartElementsWithBroker(ctx context.Context) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, nil
	}
	return true, client.RestartService(ctx, "lightningos-elements", false, false)
}

func elementsMutationWithBroker(ctx context.Context, operation func(context.Context, elementsPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	elementsClient, ok := client.(elementsPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support Elements")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, elementsClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, elementsClient, true)
		return false, nil
	default:
		return false, nil
	}
}

type LoopBrokerState struct {
	Installed          bool
	Status             string
	HasLNDMacaroon     bool
	HasPersistentState bool
}

func LoopStatusWithBroker(ctx context.Context) (bool, LoopBrokerState, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || client.Mode() != "enforce" {
		return false, LoopBrokerState{}, nil
	}
	loopClient, ok := client.(loopPrivilegedClient)
	if !ok {
		return true, LoopBrokerState{}, errors.New("privileged broker does not support Lightning Loop")
	}
	installed, status, hasMacaroon, hasState, err := loopClient.LoopStatus(ctx)
	return true, LoopBrokerState{Installed: installed, Status: status, HasLNDMacaroon: hasMacaroon, HasPersistentState: hasState}, err
}

func EnsureLoopWithBroker(ctx context.Context, tlsCertificate, macaroon []byte) (bool, error) {
	return loopMutationWithBroker(ctx, func(callCtx context.Context, client loopPrivilegedClient, dryRun bool) error {
		_, err := client.EnsureLoop(callCtx, tlsCertificate, macaroon, dryRun)
		return err
	})
}

func LoopLifecycleWithBroker(ctx context.Context, action string) (bool, error) {
	return loopMutationWithBroker(ctx, func(callCtx context.Context, client loopPrivilegedClient, dryRun bool) error {
		_, err := client.LoopLifecycle(callCtx, action, dryRun)
		return err
	})
}

func RemoveLoopWithBroker(ctx context.Context) (bool, error) {
	return loopMutationWithBroker(ctx, func(callCtx context.Context, client loopPrivilegedClient, dryRun bool) error {
		return client.RemoveLoop(callCtx, dryRun)
	})
}

func EnsureLoopPermissionsWithBroker(ctx context.Context) (bool, error) {
	return loopMutationWithBroker(ctx, func(callCtx context.Context, client loopPrivilegedClient, dryRun bool) error {
		return client.EnsureLoopPermissions(callCtx, dryRun)
	})
}

func EnsureLoopClientMaterialWithBroker(ctx context.Context) (bool, error) {
	return loopMutationWithBroker(ctx, func(callCtx context.Context, client loopPrivilegedClient, dryRun bool) error {
		return client.EnsureLoopClientMaterial(callCtx, dryRun)
	})
}

func loopMutationWithBroker(ctx context.Context, operation func(context.Context, loopPrivilegedClient, bool) error) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	loopClient, ok := client.(loopPrivilegedClient)
	if !ok {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker does not support Lightning Loop")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, operation(ctx, loopClient, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = operation(shadowCtx, loopClient, true)
		return false, nil
	default:
		return false, nil
	}
}

func ResetAppAdminWithBroker(ctx context.Context, appID string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	adminClient, ok := client.(appAdminResetPrivilegedClient)
	switch client.Mode() {
	case "enforce":
		if !ok {
			return true, errors.New("app admin reset broker capability is unavailable")
		}
		return true, adminClient.ResetAppAdmin(ctx, appID, false)
	case "shadow":
		if ok {
			shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_ = adminClient.ResetAppAdmin(shadowCtx, appID, true)
		}
		return false, nil
	default:
		return false, nil
	}
}

func SnapshotAppWithBroker(ctx context.Context, appID string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	snapshotClient, ok := client.(appSnapshotPrivilegedClient)
	if !ok {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, snapshotClient.SnapshotApp(ctx, appID, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = snapshotClient.SnapshotApp(shadowCtx, appID, true)
		return false, nil
	default:
		return false, nil
	}
}

func EnsureBitcoinConsumerNetworkWithBroker(ctx context.Context) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	networkClient, ok := client.(bitcoinConsumerNetworkPrivilegedClient)
	switch client.Mode() {
	case "enforce":
		if !ok {
			return true, errors.New("bitcoin consumer network broker capability is unavailable")
		}
		status, err := networkClient.EnsureBitcoinConsumerNetwork(ctx, false)
		if err != nil {
			return true, err
		}
		if status != "ready" {
			return true, errors.New("bitcoin consumer network ensure returned an invalid state")
		}
		return true, nil
	case "shadow":
		if ok {
			shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, _ = networkClient.EnsureBitcoinConsumerNetwork(shadowCtx, true)
		}
		return false, nil
	default:
		return false, nil
	}
}

func EnsureBitcoinCoreConfigWithBroker(ctx context.Context, dataDir string, content string, generateRPCAuth bool) (bool, error) {
	client, configClient := configuredBitcoinCoreConfigClient()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		if configClient == nil {
			return true, errors.New("bitcoin config broker capability is unavailable")
		}
		status, err := configClient.EnsureBitcoinCoreConfig(ctx, dataDir, content, generateRPCAuth, false)
		if err != nil {
			return true, err
		}
		if status != "ready" {
			return true, errors.New("bitcoin config ensure returned an invalid state")
		}
		return true, nil
	case "shadow":
		if configClient != nil {
			shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, _ = configClient.EnsureBitcoinCoreConfig(shadowCtx, dataDir, content, generateRPCAuth, true)
		}
		return false, nil
	default:
		return false, nil
	}
}

func ReadBitcoinCoreCredentialsWithBroker(ctx context.Context, dataDir string) (string, string, bool, error) {
	client, configClient := configuredBitcoinCoreConfigClient()
	if client == nil {
		return "", "", false, nil
	}
	if client.Mode() != "enforce" {
		return "", "", false, nil
	}
	if configClient == nil {
		return "", "", true, errors.New("bitcoin credentials broker capability is unavailable")
	}
	user, password, err := configClient.ReadBitcoinCoreCredentials(ctx, dataDir)
	return user, password, true, err
}

func ReadBitcoinCoreConfigWithBroker(ctx context.Context, dataDir string) (string, bool, error) {
	client, configClient := configuredBitcoinCoreConfigClient()
	if client == nil {
		return "", false, nil
	}
	if client.Mode() != "enforce" {
		return "", false, nil
	}
	if configClient == nil {
		return "", true, errors.New("bitcoin config broker capability is unavailable")
	}
	content, err := configClient.ReadBitcoinCoreConfig(ctx, dataDir)
	return content, true, err
}

func ReadBitcoinCoreStatusWithBroker(ctx context.Context) (string, bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return "", false, nil
	}
	statusClient, ok := client.(bitcoinCoreStatusPrivilegedClient)
	switch client.Mode() {
	case "enforce":
		if !ok {
			return "", true, errors.New("bitcoin status broker capability is unavailable")
		}
		status, err := statusClient.BitcoinCoreStatus(ctx)
		return status, true, err
	case "shadow":
		// Shadow retains the established container/log compatibility path. A
		// synchronous RPC probe here can take tens of seconds while bitcoind is
		// indexing and would delay the fallback that already reports progress.
		return "", false, nil
	default:
		return "", false, nil
	}
}

func WriteBitcoinCoreConfigWithBroker(ctx context.Context, dataDir string, content string) (bool, error) {
	client, configClient := configuredBitcoinCoreConfigClient()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		if configClient == nil {
			return true, errors.New("bitcoin config broker capability is unavailable")
		}
		status, err := configClient.WriteBitcoinCoreConfig(ctx, dataDir, content, false)
		if err != nil {
			return true, err
		}
		if status != "ready" {
			return true, errors.New("bitcoin config write returned an invalid state")
		}
		return true, nil
	case "shadow":
		if configClient != nil {
			shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, _ = configClient.WriteBitcoinCoreConfig(shadowCtx, dataDir, content, true)
		}
		return false, nil
	default:
		return false, nil
	}
}

func configuredBitcoinCoreConfigClient() (PrivilegedClient, bitcoinCoreConfigPrivilegedClient) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return nil, nil
	}
	configClient, _ := client.(bitcoinCoreConfigPrivilegedClient)
	return client, configClient
}

func EnsureBitcoinCoreStorageWithBroker(ctx context.Context, dataDir string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		status, err := client.EnsureBitcoinCoreStorage(ctx, dataDir, false)
		if err != nil {
			return true, err
		}
		if status != "ready" {
			return true, errors.New("bitcoin storage enrollment returned an invalid state")
		}
		return true, nil
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.EnsureBitcoinCoreStorage(shadowCtx, dataDir, true)
		return false, nil
	default:
		return false, nil
	}
}

func EnsurePackageFeatureWithBroker(ctx context.Context, feature string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		status, err := client.EnsurePackageFeature(ctx, feature, false)
		if err != nil {
			return true, err
		}
		waitCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			switch status {
			case "ready":
				status, err = client.EnsurePackageFeature(waitCtx, feature, false)
				if err != nil {
					return true, err
				}
				if status != "ready" {
					return true, errors.New("package feature finalization returned an invalid state")
				}
				return true, nil
			case "indexed":
				status, err = client.EnsurePackageFeature(waitCtx, feature, false)
				if err != nil {
					return true, err
				}
				continue
			case "indexing", "installing":
			case "absent", "failed":
				return true, errors.New("package feature preparation failed")
			default:
				return true, errors.New("package feature returned an invalid state")
			}
			select {
			case <-waitCtx.Done():
				return true, errors.New("package feature preparation did not complete")
			case <-ticker.C:
				status, err = client.PackageFeatureStatus(waitCtx, feature)
				if err != nil {
					return true, err
				}
			}
		}
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.EnsurePackageFeature(shadowCtx, feature, true)
		return false, nil
	default:
		return false, nil
	}
}

func EnsureDockerRuntimeWithBroker(ctx context.Context) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		status, err := client.EnsureDockerRuntime(ctx, false)
		if err != nil {
			return true, err
		}
		if status == "ready" {
			return true, nil
		}
		if status != "starting" {
			return true, errors.New("docker runtime returned an invalid state")
		}
		waitCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return true, errors.New("docker runtime did not become ready")
			case <-ticker.C:
				status, err = client.DockerRuntimeStatus(waitCtx)
				if err != nil {
					return true, err
				}
				switch status {
				case "ready":
					return true, nil
				case "starting":
					continue
				case "stopped", "failed":
					return true, errors.New("docker runtime failed to start")
				default:
					return true, errors.New("docker runtime returned an invalid state")
				}
			}
		}
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.EnsureDockerRuntime(shadowCtx, true)
		return false, nil
	default:
		return false, nil
	}
}

func PrepareAppImageWithBroker(ctx context.Context, appID string, variant string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		status, err := client.PrepareAppImage(ctx, appID, variant, false)
		if err != nil {
			return true, err
		}
		if status == "ready" {
			return true, nil
		}
		if status != "preparing" {
			return true, errors.New("app image preparation returned an invalid state")
		}
		// Source-built catalog images (notably Electrs) can legitimately take
		// longer than a registry pull on low-power nodes. The broker's transient
		// unit remains independently bounded and status polling carries no secret.
		waitCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-waitCtx.Done():
				return true, errors.New("app image preparation did not complete")
			case <-ticker.C:
				status, err = client.AppImageStatus(waitCtx, appID, variant)
				if err != nil {
					return true, err
				}
				switch status {
				case "ready":
					return true, nil
				case "preparing":
					continue
				case "absent", "failed":
					return true, errors.New("app image preparation failed")
				default:
					return true, errors.New("app image preparation returned an invalid state")
				}
			}
		}
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.PrepareAppImage(shadowCtx, appID, variant, true)
		return false, nil
	default:
		return false, nil
	}
}

func ProbeAppImageWithBroker(ctx context.Context, appID string, variant string) (bool, bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, false, nil
	}
	switch client.Mode() {
	case "enforce":
		runnable, err := client.ProbeAppImage(ctx, appID, variant, false)
		return true, runnable, err
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.ProbeAppImage(shadowCtx, appID, variant, true)
		return false, false, nil
	default:
		return false, false, nil
	}
}

func EnsureAppFirewallWithBroker(ctx context.Context, appID string) (bool, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, "", nil
	}
	switch client.Mode() {
	case "enforce":
		status, err := client.EnsureAppFirewall(ctx, appID, false)
		return true, status, err
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_, _ = client.EnsureAppFirewall(shadowCtx, appID, true)
		return false, "", nil
	default:
		return false, "", nil
	}
}

func ReadManagerFirewallStatusWithBroker(ctx context.Context) (string, bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil || (client.Mode() != "enforce" && client.Mode() != "shadow") {
		return "", false, nil
	}
	firewallClient, ok := client.(managerFirewallPrivilegedClient)
	if !ok {
		return "", false, nil
	}
	raw, err := firewallClient.ManagerFirewallStatus(ctx)
	return raw, true, err
}

func EnsureAppLNDHostAccessWithBroker(ctx context.Context, appID string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	hostClient, ok := client.(appLNDHostAccessPrivilegedClient)
	if !ok {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, hostClient.EnsureAppLNDHostAccess(ctx, appID, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = hostClient.EnsureAppLNDHostAccess(shadowCtx, appID, true)
		return false, nil
	default:
		return false, nil
	}
}

func InspectAppWithBroker(ctx context.Context, appID string) (handled bool, status string, cpuPercentRaw float64, err error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, "", 0, nil
	}
	switch client.Mode() {
	case "enforce":
		status, cpuPercentRaw, err := client.InspectApp(ctx, appID)
		return true, status, cpuPercentRaw, err
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_, _, _ = client.InspectApp(shadowCtx, appID)
		return false, "", 0, nil
	default:
		return false, "", 0, nil
	}
}

func ReadAppLogsWithBroker(ctx context.Context, appID string, lines int, since string) (bool, []string, string, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil, "", nil
	}
	logClient, ok := client.(appLogsPrivilegedClient)
	if !ok || client.Mode() != "enforce" {
		return false, nil, "", nil
	}
	result, source, err := logClient.AppLogs(ctx, appID, lines, since)
	return true, result, source, err
}

// AppLifecycleWithBroker routes a catalog-defined lifecycle action through the
// privileged broker. Shadow mode validates the manifest and then leaves the
// existing lifecycle path in charge; enforce mode performs the real action.
func AppLifecycleWithBroker(ctx context.Context, appID string, action string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, client.AppLifecycle(ctx, appID, action, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = client.AppLifecycle(shadowCtx, appID, action, true)
		return false, nil
	default:
		return false, nil
	}
}

// RemoveAppWithBroker routes catalog-defined Compose teardown through the
// privileged broker. The caller may remove its own app files only after an
// enforce-mode call succeeds.
func RemoveAppWithBroker(ctx context.Context, appID string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, client.RemoveApp(ctx, appID, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = client.RemoveApp(shadowCtx, appID, true)
		return false, nil
	default:
		return false, nil
	}
}

var privilegedState struct {
	sync.RWMutex
	client PrivilegedClient
}

func ConfigurePrivilegedClient(client PrivilegedClient) {
	privilegedState.Lock()
	privilegedState.client = client
	privilegedState.Unlock()
}

type DiskUsage struct {
	Mount       string  `json:"mount"`
	TotalGB     float64 `json:"total_gb"`
	UsedGB      float64 `json:"used_gb"`
	UsedPercent float64 `json:"used_percent"`
}

type SystemStats struct {
	UptimeSec        int64       `json:"uptime_sec"`
	CPULoad1         float64     `json:"cpu_load_1"`
	CPUPercent       float64     `json:"cpu_percent"`
	CPUPercentNow    float64     `json:"cpu_percent_now,omitempty"`
	CPUPercentAvg30s float64     `json:"cpu_percent_avg_30s,omitempty"`
	CPUCores         int         `json:"cpu_cores,omitempty"`
	RAMTotalMB       int64       `json:"ram_total_mb"`
	RAMUsedMB        int64       `json:"ram_used_mb"`
	Disk             []DiskUsage `json:"disk"`
	TemperatureC     float64     `json:"temperature_c"`
}

func GetSystemStats(ctx context.Context) (SystemStats, error) {
	var stats SystemStats

	uptime, err := readUptime()
	if err != nil {
		return stats, err
	}
	stats.UptimeSec = uptime

	load1, err := readLoad1()
	if err == nil {
		stats.CPULoad1 = load1
	}

	cpuUsage, err := readCPUUsageSnapshot()
	if err == nil {
		stats.CPUPercent = preferredCPUPercent(cpuUsage)
		stats.CPUPercentNow = cpuUsage.Latest
		stats.CPUPercentAvg30s = cpuUsage.Average30s
		stats.CPUCores = cpuUsage.Cores
	}

	totalMB, usedMB, err := readMemInfo()
	if err == nil {
		stats.RAMTotalMB = totalMB
		stats.RAMUsedMB = usedMB
	}

	disks, err := readDiskUsage(ctx)
	if err == nil {
		stats.Disk = disks
	}

	tempC, err := readTemperatureC()
	if err == nil {
		stats.TemperatureC = tempC
	}

	return stats, nil
}

func readUptime() (int64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(b))
	if len(parts) == 0 {
		return 0, errors.New("uptime parse error")
	}
	seconds, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	return int64(seconds), nil
}

func readLoad1() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(b))
	if len(parts) < 1 {
		return 0, errors.New("loadavg parse error")
	}
	return strconv.ParseFloat(parts[0], 64)
}

func readCPUPercent(delay time.Duration) (float64, error) {
	idle1, total1, err := readCPUStat()
	if err != nil {
		return 0, err
	}
	time.Sleep(delay)
	idle2, total2, err := readCPUStat()
	if err != nil {
		return 0, err
	}
	idle := idle2 - idle1
	total := total2 - total1
	if total == 0 {
		return 0, errors.New("cpu total zero")
	}
	usage := (1.0 - float64(idle)/float64(total)) * 100.0
	return usage, nil
}

func readCPUStat() (idle uint64, total uint64, err error) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	if !scanner.Scan() {
		return 0, 0, errors.New("cpu stat empty")
	}
	return parseCPUStatLine(scanner.Text())
}

func parseCPUStatLine(line string) (idle uint64, total uint64, err error) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return 0, 0, errors.New("cpu stat parse error")
	}
	if fields[0] != "cpu" {
		return 0, 0, errors.New("cpu stat missing aggregate")
	}
	for i := 1; i < len(fields); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			continue
		}
		total += v
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return idle, total, nil
}

func readMemInfo() (totalMB int64, usedMB int64, err error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	var totalKB, freeKB, buffersKB, cachedKB int64
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseMemValue(line)
		}
		if strings.HasPrefix(line, "MemFree:") {
			freeKB = parseMemValue(line)
		}
		if strings.HasPrefix(line, "Buffers:") {
			buffersKB = parseMemValue(line)
		}
		if strings.HasPrefix(line, "Cached:") {
			cachedKB = parseMemValue(line)
		}
	}

	if totalKB == 0 {
		return 0, 0, errors.New("meminfo missing")
	}
	usedKB := totalKB - freeKB - buffersKB - cachedKB
	return totalKB / 1024, usedKB / 1024, nil
}

func parseMemValue(line string) int64 {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	v, _ := strconv.ParseInt(parts[1], 10, 64)
	return v
}

func readDiskUsage(ctx context.Context) ([]DiskUsage, error) {
	cmd := exec.CommandContext(ctx, "df", "-B1", "-x", "tmpfs", "-x", "devtmpfs")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var disks []DiskUsage
	scanner := bufio.NewScanner(bytes.NewReader(out))
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		if first {
			first = false
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		totalBytes, _ := strconv.ParseFloat(fields[1], 64)
		usedBytes, _ := strconv.ParseFloat(fields[2], 64)
		usedPercent, _ := strconv.ParseFloat(strings.TrimSuffix(fields[4], "%"), 64)
		disks = append(disks, DiskUsage{
			Mount:       fields[5],
			TotalGB:     totalBytes / (1024 * 1024 * 1024),
			UsedGB:      usedBytes / (1024 * 1024 * 1024),
			UsedPercent: usedPercent,
		})
	}
	return disks, nil
}

func readTemperatureC() (float64, error) {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, err
	}
	if v > 1000 {
		v = v / 1000.0
	}
	return v, nil
}

func RunCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s failed: %w", name, err)
	}
	return string(out), nil
}

func RunCommandWithSudo(ctx context.Context, name string, args ...string) (string, error) {
	out, err := RunCommand(ctx, name, args...)
	if err == nil {
		return out, nil
	}
	sudoPath, sudoErr := exec.LookPath("sudo")
	if sudoErr != nil {
		return out, err
	}
	sudoArgs := append([]string{"-n", name}, args...)
	sudoOut, sudoErr := RunCommand(ctx, sudoPath, sudoArgs...)
	if sudoErr == nil {
		return sudoOut, nil
	}
	return sudoOut, fmt.Errorf("%s failed: %w; sudo failed: %v", name, err, sudoErr)
}

func WriteFileWithSudo(ctx context.Context, path string, data []byte) error {
	sudoPath, sudoErr := exec.LookPath("sudo")
	if sudoErr != nil {
		return sudoErr
	}
	teePath, teeErr := exec.LookPath("tee")
	if teeErr != nil {
		return teeErr
	}

	cmd := exec.CommandContext(ctx, sudoPath, "-n", teePath, path)
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	teeMsg := strings.TrimSpace(string(out))

	systemdRunPath, systemdRunErr := exec.LookPath("systemd-run")
	if systemdRunErr == nil {
		fallback := exec.CommandContext(
			ctx,
			sudoPath,
			"-n",
			systemdRunPath,
			"--quiet",
			"--wait",
			"--pipe",
			"--collect",
			teePath,
			path,
		)
		fallback.Stdin = bytes.NewReader(data)
		fallbackOut, fallbackErr := fallback.CombinedOutput()
		if fallbackErr == nil {
			return nil
		}
		fallbackMsg := strings.TrimSpace(string(fallbackOut))
		if teeMsg != "" && fallbackMsg != "" {
			return fmt.Errorf("sudo tee failed: %w (%s); sudo systemd-run tee failed: %w (%s)", err, teeMsg, fallbackErr, fallbackMsg)
		}
		if teeMsg != "" {
			return fmt.Errorf("sudo tee failed: %w (%s); sudo systemd-run tee failed: %w", err, teeMsg, fallbackErr)
		}
		if fallbackMsg != "" {
			return fmt.Errorf("sudo tee failed: %w; sudo systemd-run tee failed: %w (%s)", err, fallbackErr, fallbackMsg)
		}
		return fmt.Errorf("sudo tee failed: %w; sudo systemd-run tee failed: %w", err, fallbackErr)
	}

	if teeMsg != "" {
		return fmt.Errorf("sudo tee failed: %w (%s)", err, teeMsg)
	}
	return fmt.Errorf("sudo tee failed: %w", err)
}

func systemctlPath() string {
	if path, err := exec.LookPath("systemctl"); err == nil {
		return path
	}
	return "systemctl"
}

func SystemctlIsActive(ctx context.Context, service string) bool {
	out, err := RunCommand(ctx, systemctlPath(), "is-active", service)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "active"
}

func SystemctlRestart(ctx context.Context, service string) error {
	return systemctlRestart(ctx, service, service == "lightningos-manager")
}

func SystemctlRestartNoBlock(ctx context.Context, service string) error {
	return systemctlRestart(ctx, service, true)
}

func systemctlRestart(ctx context.Context, service string, noBlock bool) error {
	if handled, err := restartServiceWithBroker(ctx, service, noBlock); handled {
		return err
	}
	systemctl := systemctlPath()
	args := []string{"restart"}
	if noBlock {
		args = append(args, "--no-block")
	}
	args = append(args, service)
	_, err := RunCommand(ctx, systemctl, args...)
	if err == nil {
		return nil
	}
	sudoPath, sudoErr := exec.LookPath("sudo")
	if sudoErr != nil {
		return err
	}
	sudoArgs := append([]string{"-n", systemctl}, args...)
	if _, sudoErr = RunCommand(ctx, sudoPath, sudoArgs...); sudoErr == nil {
		return nil
	}
	if !noBlock {
		return fmt.Errorf("systemctl restart failed: %w; sudo restart failed: %v", err, sudoErr)
	}
	if runErr := systemdRunSystemctlRestartNoBlock(ctx, sudoPath, systemctl, service); runErr == nil {
		return nil
	} else {
		return fmt.Errorf("systemctl restart --no-block failed: %w; sudo restart failed: %v; sudo systemd-run restart failed: %v", err, sudoErr, runErr)
	}
}

func restartServiceWithBroker(ctx context.Context, service string, noBlock bool) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, client.RestartService(ctx, service, noBlock, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = client.RestartService(shadowCtx, service, noBlock, true)
		return false, nil
	default:
		return false, nil
	}
}

func EnableLoginConfigWithBroker(ctx context.Context, configPath string) (bool, error) {
	privilegedState.RLock()
	client := privilegedState.client
	privilegedState.RUnlock()
	if client == nil {
		return false, nil
	}
	if configPath != installedManagerConfigPath {
		if client.Mode() == "enforce" {
			return true, errors.New("privileged broker supports only the installed manager config path")
		}
		return false, nil
	}
	switch client.Mode() {
	case "enforce":
		return true, client.EnableLogin(ctx, false)
	case "shadow":
		shadowCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = client.EnableLogin(shadowCtx, true)
		return false, nil
	default:
		return false, nil
	}
}

func systemdRunSystemctlRestartNoBlock(ctx context.Context, sudoPath string, systemctl string, service string) error {
	systemdRunPath, err := exec.LookPath("systemd-run")
	if err != nil {
		return err
	}
	unit := transientRestartUnitName(service)
	_, err = RunCommand(
		ctx,
		sudoPath,
		"-n",
		systemdRunPath,
		"--quiet",
		"--collect",
		"--unit",
		unit,
		systemctl,
		"restart",
		"--no-block",
		service,
	)
	return err
}

func transientRestartUnitName(service string) string {
	var b strings.Builder
	for _, r := range service {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "service"
	}
	return fmt.Sprintf("lightningos-restart-%s-%d", name, time.Now().UnixNano())
}

func SystemctlPower(ctx context.Context, action string) error {
	if action != "reboot" && action != "poweroff" {
		return fmt.Errorf("unsupported system action")
	}
	systemctl := systemctlPath()
	if _, err := RunCommandWithSudo(ctx, systemctl, action); err != nil {
		return fmt.Errorf("systemctl %s failed: %w", action, err)
	}
	return nil
}

func JournalTail(ctx context.Context, service string, lines int) ([]string, error) {
	return JournalTailSince(ctx, service, lines, "")
}

func JournalTailSince(ctx context.Context, service string, lines int, since string) ([]string, error) {
	if lines <= 0 {
		lines = 200
	}
	args := []string{"-u", service, "-n", strconv.Itoa(lines), "--no-pager"}
	if strings.TrimSpace(since) != "" {
		args = append(args, "--since", since)
	}
	out, err := RunCommand(ctx, "journalctl", args...)
	if err != nil {
		return nil, err
	}
	raw := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	var trimmed []string
	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed = append(trimmed, line)
	}
	return trimmed, nil
}
