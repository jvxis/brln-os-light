package privileged

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	BrokerExecutablePath = "/usr/local/libexec/lightningos-privileged"
	BrokerSocketPath     = "/run/lightningos-privileged/broker.sock"
)

type Mode string

const (
	ModeDisabled Mode = "disabled"
	ModeShadow   Mode = "shadow"
	ModeEnforce  Mode = "enforce"
)

type Transport interface {
	Do(ctx context.Context, request Request) (Response, error)
}

type Client struct {
	mode      Mode
	timeout   time.Duration
	transport Transport
	logger    *log.Logger
}

type ClientError struct {
	Code    string
	Message string
}

func (err *ClientError) Error() string {
	if err == nil {
		return ""
	}
	if err.Code == "" {
		return err.Message
	}
	return err.Code + ": " + err.Message
}

func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeDisabled:
		return ModeDisabled, nil
	case ModeShadow:
		return ModeShadow, nil
	case ModeEnforce:
		return ModeEnforce, nil
	default:
		return "", fmt.Errorf("invalid privileged broker mode")
	}
}

func NewClient(modeValue string, timeout time.Duration, logger *log.Logger) (*Client, error) {
	mode, err := ParseMode(modeValue)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &Client{
		mode:      mode,
		timeout:   timeout,
		transport: &SocketTransport{},
		logger:    logger,
	}, nil
}

func NewClientWithTransport(mode Mode, timeout time.Duration, transport Transport, logger *log.Logger) *Client {
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	return &Client{mode: mode, timeout: timeout, transport: transport, logger: logger}
}

func (client *Client) Mode() string {
	if client == nil {
		return string(ModeDisabled)
	}
	return string(client.mode)
}

func (client *Client) SelfTest(ctx context.Context) error {
	_, err := client.call(ctx, OperationSelfTest, struct{}{}, false)
	return err
}

func (client *Client) ServiceStatus(ctx context.Context, unit string) (string, error) {
	response, err := client.call(ctx, OperationServiceStatus, ServiceStatusParams{Unit: unit}, false)
	if err != nil {
		return "", err
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker service status response")
	}
	return result.Status, nil
}

func (client *Client) ManagerFirewallStatus(ctx context.Context) (string, error) {
	response, err := client.call(ctx, OperationManagerFirewallStatus, struct{}{}, false)
	if err != nil {
		return "", err
	}
	var state ManagerFirewallState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker manager firewall status response")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", errors.New("invalid broker manager firewall status response")
	}
	return string(raw), nil
}

func (client *Client) StartLNDUpgrade(ctx context.Context, version string, helperContent string, verifyOnly bool, dryRun bool) (string, string, error) {
	response, err := client.call(ctx, OperationLNDUpgradeStart, LNDUpgradeStartParams{
		Version: version, HelperContent: helperContent, VerifyOnly: verifyOnly,
	}, dryRun)
	if err != nil {
		return "", "", err
	}
	var state LNDUpgradeState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", "", errors.New("invalid broker LND upgrade response")
	}
	expectedUnit := lndUpgradeUnit
	if verifyOnly {
		expectedUnit = lndVerifyUnit
	}
	if state.Version != version || state.VerifyOnly != verifyOnly || state.Unit != expectedUnit ||
		(state.Status != "validated" && state.Status != "started") {
		return "", "", errors.New("invalid broker LND upgrade state")
	}
	if dryRun && state.Status != "validated" {
		return "", "", errors.New("invalid broker LND upgrade dry-run state")
	}
	return state.Status, state.Unit, nil
}

func (client *Client) RefreshTorMetadata(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationTorMetadataRefresh, struct{}{}, dryRun)
	if err != nil {
		return "", err
	}
	var state TorUpgradeState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker Tor metadata response")
	}
	expected := "refreshed"
	if dryRun {
		expected = "validated"
	}
	if state.Status != expected || state.Unit != "" || state.VerifyOnly {
		return "", errors.New("invalid broker Tor metadata state")
	}
	return state.Status, nil
}

func (client *Client) StartTorUpgrade(ctx context.Context, helperContent string, verifyOnly bool, dryRun bool) (string, string, error) {
	response, err := client.call(ctx, OperationTorUpgradeStart, TorUpgradeStartParams{HelperContent: helperContent, VerifyOnly: verifyOnly}, dryRun)
	if err != nil {
		return "", "", err
	}
	var state TorUpgradeState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", "", errors.New("invalid broker Tor upgrade response")
	}
	expectedUnit := torUpgradeUnit
	if verifyOnly {
		expectedUnit = torVerifyUnit
	}
	expectedStatus := "started"
	if dryRun {
		expectedStatus = "validated"
	}
	if state.Unit != expectedUnit || state.VerifyOnly != verifyOnly || state.Status != expectedStatus {
		return "", "", errors.New("invalid broker Tor upgrade state")
	}
	return state.Status, state.Unit, nil
}

func (client *Client) StartLightningOSUpgrade(ctx context.Context, version, tag, commit, helperContent string, verifyOnly bool, dryRun bool) (string, string, error) {
	response, err := client.call(ctx, OperationLightningOSUpgradeStart, LightningOSUpgradeStartParams{
		Version: version, Tag: tag, Commit: commit, HelperContent: helperContent, VerifyOnly: verifyOnly,
	}, dryRun)
	if err != nil {
		return "", "", err
	}
	var state LightningOSUpgradeState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", "", errors.New("invalid broker LightningOS upgrade response")
	}
	expectedUnit := lightningOSUpgradeUnit
	if verifyOnly {
		expectedUnit = lightningOSVerifyUnit
	}
	expectedStatus := "started"
	if dryRun {
		expectedStatus = "validated"
	}
	if state.Status != expectedStatus || state.Version != version || state.Commit != commit ||
		state.Unit != expectedUnit || state.VerifyOnly != verifyOnly {
		return "", "", errors.New("invalid broker LightningOS upgrade state")
	}
	return state.Status, state.Unit, nil
}

func (client *Client) LoopStatus(ctx context.Context) (bool, string, bool, bool, error) {
	response, err := client.call(ctx, OperationLoopStatus, struct{}{}, false)
	if err != nil {
		return false, "", false, false, err
	}
	state, err := decodeLoopState(response, false)
	return state.Installed, state.Status, state.HasLNDMacaroon, state.HasPersistentState, err
}

func (client *Client) ElementsStatus(ctx context.Context, dataDir string) (string, error) {
	response, err := client.call(ctx, OperationElementsStatus, ElementsTargetParams{DataDir: dataDir}, false)
	if err != nil {
		return "", err
	}
	var state ElementsState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker Elements status response")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", errors.New("invalid broker Elements status response")
	}
	return string(raw), nil
}

func (client *Client) ReadElementsConfig(ctx context.Context, dataDir string) (string, error) {
	response, err := client.call(ctx, OperationElementsConfigRead, ElementsTargetParams{DataDir: dataDir}, false)
	if err != nil {
		return "", err
	}
	var state ElementsConfigState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker Elements config response")
	}
	if state.Status == "missing" {
		return "", nil
	}
	if state.Status != "ready" {
		return "", errors.New("invalid broker Elements config status")
	}
	return state.Content, nil
}

func (client *Client) EnsureElements(ctx context.Context, dataDir, content string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationElementsEnsure, ElementsEnsureParams{DataDir: dataDir, Content: content}, dryRun)
	if err != nil {
		return "", err
	}
	return decodeElementsStateStatus(response, dryRun)
}

func (client *Client) ElementsLifecycle(ctx context.Context, dataDir, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationElementsLifecycle, ElementsLifecycleParams{DataDir: dataDir, Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	return decodeElementsStateStatus(response, dryRun)
}

func (client *Client) RemoveElements(ctx context.Context, dataDir string, dryRun bool) error {
	_, err := client.call(ctx, OperationElementsRemove, ElementsTargetParams{DataDir: dataDir}, dryRun)
	return err
}

func decodeElementsStateStatus(response Response, dryRun bool) (string, error) {
	var state ElementsState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker Elements state response")
	}
	if dryRun && state.Status == "validated" {
		return state.Status, nil
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" {
		return "", errors.New("invalid broker Elements status")
	}
	return state.Status, nil
}

func (client *Client) PeerSwapStatus(ctx context.Context) (bool, string, bool, string, error) {
	response, err := client.call(ctx, OperationPeerSwapStatus, struct{}{}, false)
	if err != nil {
		return false, "", false, "", err
	}
	state, err := decodePeerSwapState(response, false)
	return state.Installed, state.Status, state.HasLNDMacaroon, state.ElementsMode, err
}

func (client *Client) ReadPeerSwapSource(ctx context.Context) (bool, string, string, string, string, string, error) {
	response, err := client.call(ctx, OperationPeerSwapSourceRead, struct{}{}, false)
	if err != nil {
		return false, "", "", "", "", "", err
	}
	var state PeerSwapSourceState
	if err := decodeStrict(response.Result, &state); err != nil {
		return false, "", "", "", "", "", errors.New("invalid broker PeerSwap source response")
	}
	if state.Configured {
		if err := appmanifest.ValidatePeerSwapSource(state.Source.Mode, state.Source.URL, state.Source.User, state.Source.Password, state.Source.Wallet); err != nil {
			return false, "", "", "", "", "", errors.New("invalid broker PeerSwap source")
		}
	}
	return state.Configured, state.Source.Mode, state.Source.URL, state.Source.User, state.Source.Password, state.Source.Wallet, nil
}

func (client *Client) WritePeerSwapSource(ctx context.Context, mode, rawURL, user, password, wallet string, dryRun bool) error {
	_, err := client.call(ctx, OperationPeerSwapSourceWrite, PeerSwapSource{Mode: mode, URL: rawURL, User: user, Password: password, Wallet: wallet}, dryRun)
	return err
}

func (client *Client) EnsurePeerSwap(ctx context.Context, elementsMode, config, webConfig string, tlsCertificate, macaroon []byte, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPeerSwapEnsure, PeerSwapEnsureParams{ElementsMode: elementsMode, Config: config, WebConfig: webConfig, LNDTLSCertificate: tlsCertificate, LNDMacaroon: macaroon}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodePeerSwapState(response, dryRun)
	return state.Status, err
}

func (client *Client) PeerSwapLifecycle(ctx context.Context, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPeerSwapLifecycle, PeerSwapLifecycleParams{Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodePeerSwapState(response, dryRun)
	return state.Status, err
}

func (client *Client) RemovePeerSwap(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationPeerSwapRemove, struct{}{}, dryRun)
	return err
}

func (client *Client) TapdStatus(ctx context.Context) (bool, string, bool, bool, error) {
	response, err := client.call(ctx, OperationTapdStatus, struct{}{}, false)
	if err != nil {
		return false, "", false, false, err
	}
	state, err := decodeTapdState(response, false)
	return state.Installed, state.Status, state.HasLNDMacaroon, state.InterceptorConflict, err
}

func (client *Client) EnsureTapd(ctx context.Context, databasePassword string, tlsCertificate, macaroon []byte, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationTapdEnsure, TapdEnsureParams{
		DatabasePassword: databasePassword, LNDTLSCertificate: tlsCertificate, LNDMacaroon: macaroon,
	}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeTapdState(response, dryRun)
	return state.Status, err
}

func (client *Client) TapdLifecycle(ctx context.Context, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationTapdLifecycle, TapdLifecycleParams{Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeTapdState(response, dryRun)
	return state.Status, err
}

func (client *Client) RemoveTapd(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationTapdRemove, struct{}{}, dryRun)
	return err
}

func (client *Client) TapdCLI(ctx context.Context, request appmanifest.TapdCLIRequest) (string, error) {
	response, err := client.call(ctx, OperationTapdCLI, request, false)
	if err != nil {
		return "", err
	}
	var result TapdCLIResult
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker Tapd command response")
	}
	return result.Output, nil
}

func decodeTapdState(response Response, dryRun bool) (TapdState, error) {
	var state TapdState
	if err := decodeStrict(response.Result, &state); err != nil {
		return state, errors.New("invalid broker Tapd state response")
	}
	if dryRun && state.Status == "validated" {
		return state, nil
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" {
		return TapdState{}, errors.New("invalid broker Tapd status")
	}
	return state, nil
}

func (client *Client) PublicPoolStatus(ctx context.Context) (bool, string, bool, error) {
	response, err := client.call(ctx, OperationPublicPoolStatus, struct{}{}, false)
	if err != nil {
		return false, "", false, err
	}
	state, err := decodePublicPoolState(response, false)
	return state.Installed, state.Status, state.UFWActive, err
}

func (client *Client) EnsurePublicPool(ctx context.Context, runtime appmanifest.PublicPoolRuntime, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPublicPoolEnsure, PublicPoolEnsureParams{Runtime: runtime}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodePublicPoolState(response, dryRun)
	return state.Status, err
}

func (client *Client) PublicPoolLifecycle(ctx context.Context, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPublicPoolLifecycle, PublicPoolLifecycleParams{Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodePublicPoolState(response, dryRun)
	return state.Status, err
}

func (client *Client) RemovePublicPool(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationPublicPoolRemove, struct{}{}, dryRun)
	return err
}

func (client *Client) EnsurePublicPoolFirewall(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPublicPoolFirewall, struct{}{}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodePublicPoolState(response, dryRun)
	return state.Status, err
}

func decodePublicPoolState(response Response, dryRun bool) (PublicPoolState, error) {
	var state PublicPoolState
	if err := decodeStrict(response.Result, &state); err != nil {
		return state, errors.New("invalid broker Public Pool state response")
	}
	if dryRun && state.Status == "validated" {
		return state, nil
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" && state.Status != "active" && state.Status != "inactive" {
		return PublicPoolState{}, errors.New("invalid broker Public Pool status")
	}
	return state, nil
}

func (client *Client) BarkWalletStatus(ctx context.Context) (bool, string, bool, bool, error) {
	response, err := client.call(ctx, OperationBarkWalletStatus, struct{}{}, false)
	if err != nil {
		return false, "", false, false, err
	}
	state, err := decodeBarkWalletState(response, false)
	return state.Installed, state.Status, state.UFWActive, state.PasswordAvailable, err
}

func (client *Client) EnsureBarkWallet(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBarkWalletEnsure, struct{}{}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeBarkWalletState(response, dryRun)
	return state.Status, err
}

func (client *Client) BarkWalletLifecycle(ctx context.Context, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBarkWalletLifecycle, BarkWalletLifecycleParams{Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeBarkWalletState(response, dryRun)
	return state.Status, err
}

func (client *Client) RemoveBarkWallet(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationBarkWalletRemove, struct{}{}, dryRun)
	return err
}

func (client *Client) EnsureBarkWalletFirewall(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBarkWalletFirewall, struct{}{}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeBarkWalletState(response, dryRun)
	return state.Status, err
}

func (client *Client) ReadBarkWalletPassword(ctx context.Context) (string, error) {
	response, err := client.call(ctx, OperationBarkWalletPasswordRead, struct{}{}, false)
	if err != nil {
		return "", err
	}
	var result BarkWalletPasswordResult
	if err := decodeStrict(response.Result, &result); err != nil || result.Password == "" {
		return "", errors.New("invalid broker Bark Wallet password response")
	}
	return result.Password, nil
}

func (client *Client) ResetBarkWalletPassword(ctx context.Context, dryRun bool) error {
	response, err := client.call(ctx, OperationBarkWalletPasswordReset, struct{}{}, dryRun)
	if err != nil {
		return err
	}
	_, err = decodeBarkWalletState(response, dryRun)
	return err
}

func decodeBarkWalletState(response Response, dryRun bool) (BarkWalletState, error) {
	var state BarkWalletState
	if err := decodeStrict(response.Result, &state); err != nil {
		return state, errors.New("invalid broker Bark Wallet state response")
	}
	if dryRun && state.Status == "validated" {
		return state, nil
	}
	switch state.Status {
	case "running", "stopped", "unknown", "active", "inactive":
		return state, nil
	default:
		return BarkWalletState{}, errors.New("invalid broker Bark Wallet status")
	}
}

func decodePeerSwapState(response Response, dryRun bool) (PeerSwapState, error) {
	var state PeerSwapState
	if err := decodeStrict(response.Result, &state); err != nil {
		return PeerSwapState{}, errors.New("invalid broker PeerSwap state response")
	}
	if dryRun && state.Status == "validated" {
		return state, nil
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" {
		return PeerSwapState{}, errors.New("invalid broker PeerSwap status")
	}
	return state, nil
}

func (client *Client) EnsureLoop(ctx context.Context, tlsCertificate, macaroon []byte, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationLoopEnsure, LoopEnsureParams{
		LNDTLSCertificate: tlsCertificate,
		LNDMacaroon:       macaroon,
	}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeLoopState(response, dryRun)
	return state.Status, err
}

func (client *Client) LoopLifecycle(ctx context.Context, action string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationLoopLifecycle, LoopLifecycleParams{Action: AppLifecycleAction(action)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeLoopState(response, dryRun)
	return state.Status, err
}

func (client *Client) RemoveLoop(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationLoopRemove, struct{}{}, dryRun)
	return err
}

func (client *Client) EnsureLoopPermissions(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationLoopPermissionsEnsure, struct{}{}, dryRun)
	return err
}

func (client *Client) EnsureAppStorage(ctx context.Context, dryRun bool) (string, bool, error) {
	response, err := client.call(ctx, OperationAppStorageEnsure, struct{}{}, dryRun)
	if err != nil {
		return "", false, err
	}
	var state AppStorageState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", false, errors.New("invalid broker app storage response")
	}
	wantStatus := "ready"
	if dryRun {
		wantStatus = "validated"
	}
	if state.Status != wantStatus {
		return "", false, errors.New("invalid broker app storage state")
	}
	return state.Status, state.Changed, nil
}

func (client *Client) ReadSMART(ctx context.Context, device string) (string, bool, error) {
	response, err := client.call(ctx, OperationSMARTRead, SMARTReadParams{Device: device}, false)
	if err != nil {
		return "", false, err
	}
	var state SMARTReadState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", false, errors.New("invalid broker SMART response")
	}
	if state.Device != device || len(state.Output) > maxCommandOutputBytes || strings.ContainsRune(state.Output, '\x00') || state.Available != (strings.TrimSpace(state.Output) != "") {
		return "", false, errors.New("invalid broker SMART state")
	}
	return state.Output, state.Available, nil
}

func (client *Client) RepairLNDPermissions(ctx context.Context, dryRun bool) (string, bool, error) {
	response, err := client.call(ctx, OperationLNDPermissionsRepair, struct{}{}, dryRun)
	if err != nil {
		return "", false, err
	}
	var state LNDPermissionsState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", false, errors.New("invalid broker LND permissions response")
	}
	if state.Status == "absent" && !state.Changed {
		return state.Status, false, nil
	}
	wantStatus := "ready"
	if dryRun {
		wantStatus = "validated"
	}
	if state.Status != wantStatus {
		return "", false, errors.New("invalid broker LND permissions state")
	}
	return state.Status, state.Changed, nil
}

func (client *Client) EnsureLNDManagerCredential(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	return client.lndManagerCredentialOperation(ctx, OperationLNDManagerCredentialEnsure, dryRun)
}

func (client *Client) RollbackLNDManagerCredential(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	return client.lndManagerCredentialOperation(ctx, OperationLNDManagerCredentialRollback, dryRun)
}

func (client *Client) lndManagerCredentialOperation(ctx context.Context, operation Operation, dryRun bool) (LNDManagerCredentialState, error) {
	response, err := client.call(ctx, operation, struct{}{}, dryRun)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	var state LNDManagerCredentialState
	if err := decodeStrict(response.Result, &state); err != nil {
		return LNDManagerCredentialState{}, errors.New("invalid broker LND manager credential response")
	}
	if state.ConfiguredPath != "" && state.ConfiguredPath != DefaultLNDAdminMacaroonPath && state.ConfiguredPath != DefaultLNDManagerMacaroonPath {
		return LNDManagerCredentialState{}, errors.New("invalid broker LND manager credential path")
	}
	valid := false
	switch operation {
	case OperationLNDManagerCredentialEnsure:
		valid = state.Status == "ready" || state.Status == "pending" || (dryRun && state.Status == "validated")
	case OperationLNDManagerCredentialRollback:
		valid = state.Status == "rolled_back" || state.Status == "absent" || (dryRun && state.Status == "validated")
	}
	if !valid || (state.Status == "ready" && (state.ConfiguredPath != DefaultLNDManagerMacaroonPath || !state.AdminProtected)) ||
		(state.Status == "pending" && state.Changed) || (state.Status == "absent" && state.Changed) {
		return LNDManagerCredentialState{}, errors.New("invalid broker LND manager credential state")
	}
	return state, nil
}

func (client *Client) EnsureLoopClientMaterial(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationLoopClientMaterialEnsure, struct{}{}, dryRun)
	return err
}

func decodeLoopState(response Response, dryRun bool) (LoopState, error) {
	var state LoopState
	if err := decodeStrict(response.Result, &state); err != nil {
		return state, errors.New("invalid broker Lightning Loop state response")
	}
	if dryRun && state.Status == "validated" {
		return state, nil
	}
	if state.Status != "running" && state.Status != "stopped" && state.Status != "unknown" {
		return LoopState{}, errors.New("invalid broker Lightning Loop status")
	}
	return state, nil
}

func (client *Client) RestartService(ctx context.Context, unit string, noBlock bool, dryRun bool) error {
	_, err := client.call(ctx, OperationServiceRestart, ServiceRestartParams{
		Unit:    unit,
		NoBlock: noBlock,
	}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected service.restart: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted service.restart for %s", unit)
		}
	}
	return err
}

func (client *Client) PowerHost(ctx context.Context, action string, dryRun bool) error {
	response, err := client.call(ctx, OperationHostPower, HostPowerParams{Action: action}, dryRun)
	if err != nil {
		return err
	}
	var state HostPowerState
	if err := decodeStrict(response.Result, &state); err != nil {
		return errors.New("invalid broker host power response")
	}
	if !state.Validated || state.Scheduled == dryRun {
		return errors.New("invalid broker host power state")
	}
	if dryRun && client != nil && client.logger != nil {
		client.logger.Printf("privileged broker shadow validation accepted host.power for %s", action)
	}
	return nil
}

func (client *Client) EnableLogin(ctx context.Context, dryRun bool) error {
	_, err := client.call(ctx, OperationFilesEnableLogin, struct{}{}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected files.enable_login: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted files.enable_login")
		}
	}
	return err
}

func (client *Client) InstallSystemIntegrationAsset(ctx context.Context, asset string, content string, dryRun bool) (bool, error) {
	response, err := client.call(ctx, OperationSystemIntegrationAssetInstall, SystemIntegrationAssetInstallParams{
		Asset: SystemIntegrationAsset(asset), Content: content,
	}, dryRun)
	if err != nil {
		return false, err
	}
	var state SystemIntegrationAssetState
	if err := decodeStrict(response.Result, &state); err != nil {
		return false, errors.New("invalid broker system integration asset response")
	}
	expected := "ready"
	if dryRun {
		expected = "validated"
	}
	if state.Status != expected || (dryRun && state.Changed) {
		return false, errors.New("invalid broker system integration asset state")
	}
	return state.Changed, nil
}

func (client *Client) SystemIntegrationsStatus(ctx context.Context) (bool, error) {
	response, err := client.call(ctx, OperationSystemIntegrationsStatus, struct{}{}, false)
	if err != nil {
		return false, err
	}
	var state SystemIntegrationsState
	if err := decodeStrict(response.Result, &state); err != nil {
		return false, errors.New("invalid broker system integrations status response")
	}
	if state.CertificateChanged || state.LNDPolicyChanged {
		return false, errors.New("invalid broker system integrations status state")
	}
	switch state.Status {
	case "ready":
		return true, nil
	case "absent":
		return false, nil
	default:
		return false, errors.New("invalid broker system integrations status state")
	}
}

func (client *Client) ApplySystemIntegrations(ctx context.Context, dryRun bool) (bool, bool, error) {
	response, err := client.call(ctx, OperationSystemIntegrationsApply, struct{}{}, dryRun)
	if err != nil {
		return false, false, err
	}
	state, err := decodeSystemIntegrationsState(response, dryRun)
	if err != nil {
		return false, false, err
	}
	return state.CertificateChanged, state.LNDPolicyChanged, nil
}

func (client *Client) FinalizeSystemIntegrations(ctx context.Context, dryRun bool) error {
	response, err := client.call(ctx, OperationSystemIntegrationsFinalize, struct{}{}, dryRun)
	if err != nil {
		return err
	}
	_, err = decodeSystemIntegrationsState(response, dryRun)
	return err
}

func (client *Client) RotateTerminalCredential(ctx context.Context, operatorUser string, password string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationTerminalCredentialRotate, TerminalCredentialRotateParams{
		OperatorUser: operatorUser,
		Password:     password,
	}, dryRun)
	if err != nil {
		return "", err
	}
	var state TerminalCredentialState
	if err := decodeStrict(response.Result, &state); err != nil {
		return "", errors.New("invalid broker terminal credential response")
	}
	expectedStatus := "applied"
	if dryRun {
		expectedStatus = "validated"
	}
	if state.Status != expectedStatus || state.OperatorUser != operatorUser {
		return "", errors.New("invalid broker terminal credential state")
	}
	return state.OperatorUser, nil
}

func decodeSystemIntegrationsState(response Response, dryRun bool) (SystemIntegrationsState, error) {
	var state SystemIntegrationsState
	if err := decodeStrict(response.Result, &state); err != nil {
		return SystemIntegrationsState{}, errors.New("invalid broker system integrations response")
	}
	expected := "ready"
	if dryRun {
		expected = "validated"
	}
	if state.Status != expected || (dryRun && (state.CertificateChanged || state.LNDPolicyChanged)) {
		return SystemIntegrationsState{}, errors.New("invalid broker system integrations state")
	}
	return state, nil
}

func (client *Client) EnsureDockerRuntime(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationDockerEnsure, struct{}{}, dryRun)
	status := ""
	if err == nil {
		status, err = decodeDockerRuntimeState(response, dryRun)
	}
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected docker.runtime.ensure: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted docker.runtime.ensure")
		}
	}
	if err != nil {
		return "", err
	}
	return status, nil
}

func (client *Client) DockerRuntimeStatus(ctx context.Context) (string, error) {
	response, err := client.call(ctx, OperationDockerStatus, struct{}{}, false)
	if err != nil {
		return "", err
	}
	return decodeDockerRuntimeState(response, false)
}

func (client *Client) EnsurePackageFeature(ctx context.Context, feature string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationPackageEnsure, PackageFeatureParams{Feature: PackageFeature(feature)}, dryRun)
	if err != nil {
		return "", err
	}
	status, err := decodePackageFeatureState(response, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected packages.feature.ensure: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted packages.feature.ensure for %s", feature)
		}
	}
	return status, err
}

func (client *Client) PackageFeatureStatus(ctx context.Context, feature string) (string, error) {
	response, err := client.call(ctx, OperationPackageStatus, PackageFeatureParams{Feature: PackageFeature(feature)}, false)
	if err != nil {
		return "", err
	}
	return decodePackageFeatureState(response, false)
}

func decodePackageFeatureState(response Response, dryRun bool) (string, error) {
	var result PackageFeatureState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker package feature state response")
	}
	if dryRun && result.Status == "validated" {
		return result.Status, nil
	}
	switch result.Status {
	case "ready", "indexing", "indexed", "installing", "absent", "failed":
		return result.Status, nil
	default:
		return "", errors.New("invalid broker package feature state")
	}
}

func decodeDockerRuntimeState(response Response, dryRun bool) (string, error) {
	var result DockerRuntimeState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker docker runtime state response")
	}
	if dryRun && result.Status == "validated" {
		return result.Status, nil
	}
	switch result.Status {
	case "ready", "starting", "stopped", "failed":
		return result.Status, nil
	default:
		return "", errors.New("invalid broker docker runtime state")
	}
}

func (client *Client) PrepareAppImage(ctx context.Context, appID string, variant string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationAppImagePrepare, AppImageParams{AppID: appID, Variant: appmanifest.AppImageVariant(variant)}, dryRun)
	if err != nil {
		return "", err
	}
	state, err := decodeAppImageState(response, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.image.prepare: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.image.prepare for %s/%s", appID, variant)
		}
	}
	return state, err
}

func (client *Client) AppImageStatus(ctx context.Context, appID string, variant string) (string, error) {
	response, err := client.call(ctx, OperationAppImageStatus, AppImageParams{AppID: appID, Variant: appmanifest.AppImageVariant(variant)}, false)
	if err != nil {
		return "", err
	}
	return decodeAppImageState(response, false)
}

func (client *Client) ProbeAppImage(ctx context.Context, appID string, variant string, dryRun bool) (bool, error) {
	response, err := client.call(ctx, OperationAppImageProbe, AppImageParams{AppID: appID, Variant: appmanifest.AppImageVariant(variant)}, dryRun)
	if err != nil {
		return false, err
	}
	var result AppImageProbe
	if err := decodeStrict(response.Result, &result); err != nil {
		return false, errors.New("invalid broker app image probe response")
	}
	if dryRun && client != nil && client.logger != nil {
		client.logger.Printf("privileged broker shadow validation accepted app.image.probe for %s/%s", appID, variant)
	}
	return result.Runnable, nil
}

func (client *Client) EnsureAppFirewall(ctx context.Context, appID string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationAppFirewallEnsure, AppFirewallParams{AppID: appID}, dryRun)
	if err != nil {
		return "", err
	}
	var result AppFirewallState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker app firewall state response")
	}
	if dryRun && result.Status == "validated" {
		if client != nil && client.logger != nil {
			client.logger.Printf("privileged broker shadow validation accepted app.firewall.ensure for %s", appID)
		}
		return result.Status, nil
	}
	switch result.Status {
	case "active", "inactive", "unavailable":
		return result.Status, nil
	default:
		return "", errors.New("invalid broker app firewall state")
	}
}

func (client *Client) EnsureAppLNDHostAccess(ctx context.Context, appID string, dryRun bool) error {
	response, err := client.call(ctx, OperationAppLNDHostAccessEnsure, AppLNDHostAccessParams{AppID: appID}, dryRun)
	if err != nil {
		return err
	}
	var result struct {
		Ready   bool `json:"ready"`
		Changed bool `json:"changed"`
	}
	if err := decodeStrict(response.Result, &result); err != nil || !result.Ready {
		return errors.New("invalid broker app LND host access response")
	}
	return nil
}

func (client *Client) EnsureBitcoinCoreStorage(ctx context.Context, dataDir string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBitcoinStorageEnsure, BitcoinCoreStorageParams{DataDir: dataDir}, dryRun)
	if err != nil {
		return "", err
	}
	var result BitcoinCoreStorageState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker bitcoin storage state response")
	}
	if dryRun && result.Status == "validated" {
		if client != nil && client.logger != nil {
			client.logger.Printf("privileged broker shadow validation accepted app.bitcoincore.storage.ensure")
		}
		return result.Status, nil
	}
	if result.Status != "ready" {
		return "", errors.New("invalid broker bitcoin storage state")
	}
	return result.Status, nil
}

func (client *Client) EnsureBitcoinConsumerNetwork(ctx context.Context, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBitcoinConsumerNetworkEnsure, struct{}{}, dryRun)
	if err != nil {
		return "", err
	}
	var result BitcoinConsumerNetworkState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker bitcoin consumer network response")
	}
	if dryRun && result.Status == "validated" {
		if client != nil && client.logger != nil {
			client.logger.Printf("privileged broker shadow validation accepted bitcoin.consumer-network.ensure")
		}
		return result.Status, nil
	}
	if result.Status != "ready" {
		return "", errors.New("invalid broker bitcoin consumer network state")
	}
	return result.Status, nil
}

func (client *Client) EnsureBitcoinCoreConfig(ctx context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBitcoinConfigEnsure, BitcoinCoreConfigWriteParams{DataDir: dataDir, Content: content, GenerateRPCAuth: generateRPCAuth}, dryRun)
	if err != nil {
		return "", err
	}
	return client.decodeBitcoinCoreConfigUpdate(response, dryRun, OperationBitcoinConfigEnsure)
}

func (client *Client) ReadBitcoinCoreCredentials(ctx context.Context, dataDir string) (string, string, error) {
	response, err := client.call(ctx, OperationBitcoinCredentialsRead, BitcoinCoreConfigTargetParams{DataDir: dataDir}, false)
	if err != nil {
		return "", "", err
	}
	var result BitcoinCoreCredentialsState
	if err := decodeStrict(response.Result, &result); err != nil || result.Status != "ready" ||
		result.User != appmanifest.BitcoinCoreRPCUser || len(result.Password) != 64 {
		return "", "", errors.New("invalid broker bitcoin credentials response")
	}
	if _, err := hex.DecodeString(result.Password); err != nil {
		return "", "", errors.New("invalid broker bitcoin credentials response")
	}
	return result.User, result.Password, nil
}

func (client *Client) EnsureBitcoinCoreCredentials(ctx context.Context, dataDir string, dryRun bool) (string, string, string, bool, error) {
	response, err := client.call(ctx, OperationBitcoinCredentialsEnsure, BitcoinCoreConfigTargetParams{DataDir: dataDir}, dryRun)
	if err != nil {
		return "", "", "", false, err
	}
	var result BitcoinCoreCredentialsEnsureState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", "", "", false, errors.New("invalid broker bitcoin credentials response")
	}
	if dryRun {
		if result.Status != "validated" || result.User != "" || result.Password != "" || result.ConfigChanged {
			return "", "", "", false, errors.New("invalid broker bitcoin credentials dry-run response")
		}
		return "", "", result.Status, false, nil
	}
	if result.Status != "ready" && result.Status != "restart_required" {
		return "", "", "", false, errors.New("invalid broker bitcoin credentials state")
	}
	if result.User != appmanifest.BitcoinCoreRPCUser || len(result.Password) != 64 {
		return "", "", "", false, errors.New("invalid broker bitcoin credentials response")
	}
	if _, err := hex.DecodeString(result.Password); err != nil {
		return "", "", "", false, errors.New("invalid broker bitcoin credentials response")
	}
	return result.User, result.Password, result.Status, result.ConfigChanged, nil
}

func (client *Client) EnsureBitcoinCoreElectrsCredentials(ctx context.Context, dataDir string, dryRun bool) (string, string, string, bool, error) {
	response, err := client.call(ctx, OperationBitcoinElectrsCredentialsEnsure, BitcoinCoreConfigTargetParams{DataDir: dataDir}, dryRun)
	if err != nil {
		return "", "", "", false, err
	}
	var result BitcoinCoreElectrsCredentialsState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", "", "", false, errors.New("invalid broker Electrs bitcoin credentials response")
	}
	if dryRun {
		if result.Status != "validated" || result.User != "" || result.Password != "" || result.ConfigChanged {
			return "", "", "", false, errors.New("invalid broker Electrs bitcoin credentials dry-run response")
		}
		return "", "", result.Status, false, nil
	}
	if (result.Status != "ready" && result.Status != "restart_required") || result.User != appmanifest.ElectrsBitcoinRPCUser || len(result.Password) != 64 {
		return "", "", "", false, errors.New("invalid broker Electrs bitcoin credentials response")
	}
	if _, err := hex.DecodeString(result.Password); err != nil {
		return "", "", "", false, errors.New("invalid broker Electrs bitcoin credentials response")
	}
	if result.Status == "ready" && result.ConfigChanged {
		return "", "", "", false, errors.New("invalid broker Electrs bitcoin credentials response")
	}
	return result.User, result.Password, result.Status, result.ConfigChanged, nil
}

func (client *Client) ReadBitcoinCoreConfig(ctx context.Context, dataDir string) (string, error) {
	response, err := client.call(ctx, OperationBitcoinConfigRead, BitcoinCoreConfigTargetParams{DataDir: dataDir}, false)
	if err != nil {
		return "", err
	}
	var result BitcoinCoreConfigState
	if err := decodeStrict(response.Result, &result); err != nil || result.Status != "ready" {
		return "", errors.New("invalid broker bitcoin config response")
	}
	if err := validateBitcoinCoreConfigContent(result.Content); err != nil {
		return "", errors.New("invalid broker bitcoin config content")
	}
	return result.Content, nil
}

func (client *Client) BitcoinCoreStatus(ctx context.Context) (string, error) {
	response, err := client.call(ctx, OperationBitcoinStatus, struct{}{}, false)
	if err != nil {
		return "", err
	}
	var result BitcoinCoreStatusState
	if err := decodeStrict(response.Result, &result); err != nil || validateBitcoinCoreStatusState(result) != nil {
		return "", errors.New("invalid broker bitcoin status response")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return "", errors.New("invalid broker bitcoin status response")
	}
	return string(raw), nil
}

func (client *Client) WriteBitcoinCoreConfig(ctx context.Context, dataDir string, content string, dryRun bool) (string, error) {
	response, err := client.call(ctx, OperationBitcoinConfigWrite, BitcoinCoreConfigWriteParams{DataDir: dataDir, Content: content}, dryRun)
	if err != nil {
		return "", err
	}
	return client.decodeBitcoinCoreConfigUpdate(response, dryRun, OperationBitcoinConfigWrite)
}

func (client *Client) decodeBitcoinCoreConfigUpdate(response Response, dryRun bool, operation Operation) (string, error) {
	var result BitcoinCoreConfigState
	if err := decodeStrict(response.Result, &result); err != nil || result.Content != "" {
		return "", errors.New("invalid broker bitcoin config state response")
	}
	if dryRun && result.Status == "validated" {
		if client != nil && client.logger != nil {
			client.logger.Printf("privileged broker shadow validation accepted %s", operation)
		}
		return result.Status, nil
	}
	if result.Status != "ready" {
		return "", errors.New("invalid broker bitcoin config state")
	}
	return result.Status, nil
}

func decodeAppImageState(response Response, dryRun bool) (string, error) {
	var result AppImageState
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", errors.New("invalid broker app image state response")
	}
	if dryRun && result.Status == "validated" {
		return result.Status, nil
	}
	switch result.Status {
	case "ready", "preparing", "absent", "failed":
		return result.Status, nil
	default:
		return "", errors.New("invalid broker app image state")
	}
}

func (client *Client) AppLifecycle(ctx context.Context, appID string, action string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppLifecycle, AppLifecycleParams{AppID: appID, Action: AppLifecycleAction(action)}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.compose.lifecycle: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.compose.lifecycle for %s/%s", appID, action)
		}
	}
	return err
}

func (client *Client) SnapshotApp(ctx context.Context, appID string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppSnapshot, AppSnapshotParams{AppID: appID}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.compose.snapshot: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.compose.snapshot for %s", appID)
		}
	}
	return err
}

func (client *Client) RemoveApp(ctx context.Context, appID string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppRemove, AppRemoveParams{AppID: appID}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.compose.remove: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.compose.remove for %s", appID)
		}
	}
	return err
}

func (client *Client) ResetAppAdmin(ctx context.Context, appID string, dryRun bool) error {
	_, err := client.call(ctx, OperationAppAdminReset, AppAdminResetParams{AppID: appID}, dryRun)
	if dryRun && client != nil && client.logger != nil {
		if err != nil {
			client.logger.Printf("privileged broker shadow validation rejected app.admin.reset: %v", err)
		} else {
			client.logger.Printf("privileged broker shadow validation accepted app.admin.reset for %s", appID)
		}
	}
	return err
}

func (client *Client) InspectApp(ctx context.Context, appID string) (string, float64, error) {
	response, err := client.call(ctx, OperationAppInspect, AppInspectParams{AppID: appID}, false)
	if err != nil {
		if client != nil && client.mode == ModeShadow && client.logger != nil {
			client.logger.Printf("privileged broker shadow inspection rejected app.compose.inspect: %v", err)
		}
		return "", 0, err
	}
	var result AppInspection
	if err := decodeStrict(response.Result, &result); err != nil {
		return "", 0, errors.New("invalid broker app inspection response")
	}
	if result.Status != "running" && result.Status != "stopped" {
		return "", 0, errors.New("invalid broker app inspection status")
	}
	if result.CPUPercentRaw < 0 {
		return "", 0, errors.New("invalid broker app CPU percentage")
	}
	if client.mode == ModeShadow && client.logger != nil {
		client.logger.Printf("privileged broker shadow inspection accepted app.compose.inspect for %s", appID)
	}
	return result.Status, result.CPUPercentRaw, nil
}

func (client *Client) AppLogs(ctx context.Context, appID string, lines int, since string) ([]string, string, error) {
	response, err := client.call(ctx, OperationAppLogs, AppLogsParams{AppID: appID, Lines: lines, Since: since}, false)
	if err != nil {
		return nil, "", err
	}
	var result AppLogsState
	if err := decodeStrict(response.Result, &result); err != nil {
		return nil, "", errors.New("invalid broker app log response")
	}
	if result.Source == "" || len(result.Lines) > 500 {
		return nil, "", errors.New("invalid broker app log result")
	}
	return result.Lines, result.Source, nil
}

func (client *Client) call(ctx context.Context, operation Operation, params any, dryRun bool) (Response, error) {
	var response Response
	if client == nil || client.transport == nil {
		return response, errors.New("privileged broker client unavailable")
	}
	if client.mode == ModeDisabled {
		return response, errors.New("privileged broker disabled")
	}
	rawParams, err := MarshalParams(params)
	if err != nil {
		return response, err
	}
	requestID, err := newRequestID()
	if err != nil {
		return response, errors.New("failed to create broker request id")
	}
	request := Request{
		Version:   ProtocolVersion,
		RequestID: requestID,
		Operation: operation,
		DryRun:    dryRun,
		Params:    rawParams,
	}
	if err := ValidateRequest(request); err != nil {
		return response, err
	}
	callTimeout := operationTimeout(client.timeout, operation, dryRun)
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	response, err = client.transport.Do(callCtx, request)
	if err != nil {
		return response, err
	}
	if response.RequestID != request.RequestID {
		return response, errors.New("broker response request_id mismatch")
	}
	if !response.OK {
		return response, &ClientError{Code: response.Error.Code, Message: response.Error.Message}
	}
	return response, nil
}

type SocketTransport struct {
	Path string
}

func (transport *SocketTransport) Do(ctx context.Context, request Request) (Response, error) {
	var response Response
	payload, err := json.Marshal(request)
	if err != nil {
		return response, err
	}
	if len(payload) > MaxMessageBytes {
		return response, errors.New("broker request too large")
	}

	path := strings.TrimSpace(transport.Path)
	if path == "" {
		path = BrokerSocketPath
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return response, fmt.Errorf("privileged broker connection failed: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return response, fmt.Errorf("privileged broker deadline failed: %w", err)
		}
	}
	for written := 0; written < len(payload); {
		count, writeErr := connection.Write(payload[written:])
		if writeErr != nil {
			return response, fmt.Errorf("privileged broker request failed: %w", writeErr)
		}
		if count <= 0 {
			return response, errors.New("privileged broker request made no progress")
		}
		written += count
	}
	writer, ok := connection.(interface{ CloseWrite() error })
	if !ok {
		return response, errors.New("privileged broker socket does not support request framing")
	}
	if err := writer.CloseWrite(); err != nil {
		return response, fmt.Errorf("privileged broker request close failed: %w", err)
	}
	response, err = DecodeResponse(connection)
	if err != nil {
		return response, err
	}
	return response, nil
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
