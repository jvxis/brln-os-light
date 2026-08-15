package privileged

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"lightningos-light/internal/appmanifest"
)

const (
	ProtocolVersion = 1
	MaxMessageBytes = 64 * 1024
)

type Operation string

const (
	OperationSelfTest                        Operation = "self_test"
	OperationServiceStatus                   Operation = "service.status"
	OperationServiceRestart                  Operation = "service.restart"
	OperationHostPower                       Operation = "host.power"
	OperationFilesEnableLogin                Operation = "files.enable_login"
	OperationSystemIntegrationAssetInstall   Operation = "files.system-integration.install"
	OperationSystemIntegrationsStatus        Operation = "system.integrations.status"
	OperationSystemIntegrationsApply         Operation = "system.integrations.apply"
	OperationSystemIntegrationsFinalize      Operation = "system.integrations.finalize"
	OperationTerminalCredentialRotate        Operation = "terminal.credential.rotate"
	OperationTerminalControl                 Operation = "terminal.control"
	OperationManagerFirewallStatus           Operation = "manager.firewall.status"
	OperationLNDUpgradeStart                 Operation = "upgrade.lnd.start"
	OperationTorMetadataRefresh              Operation = "packages.tor.refresh"
	OperationTorUpgradeStart                 Operation = "upgrade.tor.start"
	OperationLightningOSUpgradeStart         Operation = "upgrade.lightningos.start"
	OperationAppLifecycle                    Operation = "app.compose.lifecycle"
	OperationAppSnapshot                     Operation = "app.compose.snapshot"
	OperationAppInspect                      Operation = "app.compose.inspect"
	OperationAppLogs                         Operation = "app.compose.logs"
	OperationAppRemove                       Operation = "app.compose.remove"
	OperationAppAdminReset                   Operation = "app.admin.reset"
	OperationDockerEnsure                    Operation = "docker.runtime.ensure"
	OperationDockerStatus                    Operation = "docker.runtime.status"
	OperationPackageEnsure                   Operation = "packages.feature.ensure"
	OperationPackageStatus                   Operation = "packages.feature.status"
	OperationAppStorageEnsure                Operation = "storage.apps.ensure"
	OperationCatalogStorageEnsure            Operation = "app.catalog.storage.ensure"
	OperationSMARTRead                       Operation = "storage.smart.read"
	OperationLNDPermissionsRepair            Operation = "storage.lnd.permissions.repair"
	OperationLNDManagerCredentialEnsure      Operation = "lnd.manager-credential.ensure"
	OperationLNDManagerCredentialRollback    Operation = "lnd.manager-credential.rollback"
	OperationAppImagePrepare                 Operation = "app.image.prepare"
	OperationAppImageStatus                  Operation = "app.image.status"
	OperationAppImageProbe                   Operation = "app.image.probe"
	OperationAppFirewallEnsure               Operation = "app.firewall.ensure"
	OperationAppLNDHostAccessEnsure          Operation = "app.lnd-host-access.ensure"
	OperationBitcoinStorageEnsure            Operation = "app.bitcoincore.storage.ensure"
	OperationBitcoinConfigEnsure             Operation = "app.bitcoincore.config.ensure"
	OperationBitcoinConfigRead               Operation = "app.bitcoincore.config.read"
	OperationBitcoinConfigWrite              Operation = "app.bitcoincore.config.write"
	OperationBitcoinCredentialsRead          Operation = "app.bitcoincore.credentials.read"
	OperationBitcoinCredentialsEnsure        Operation = "app.bitcoincore.credentials.ensure"
	OperationBitcoinElectrsCredentialsEnsure Operation = "app.bitcoincore.electrs-credentials.ensure"
	OperationBitcoinStatus                   Operation = "app.bitcoincore.status"
	OperationBitcoinConsumerNetworkEnsure    Operation = "bitcoin.consumer-network.ensure"
	OperationLoopStatus                      Operation = "app.loop.status"
	OperationLoopEnsure                      Operation = "app.loop.ensure"
	OperationLoopLifecycle                   Operation = "app.loop.lifecycle"
	OperationLoopRemove                      Operation = "app.loop.remove"
	OperationLoopPermissionsEnsure           Operation = "app.loop.permissions.ensure"
	OperationLoopClientMaterialEnsure        Operation = "app.loop.client-material.ensure"
	OperationElementsStatus                  Operation = "app.elements.status"
	OperationElementsConfigRead              Operation = "app.elements.config.read"
	OperationElementsEnsure                  Operation = "app.elements.ensure"
	OperationElementsLifecycle               Operation = "app.elements.lifecycle"
	OperationElementsRemove                  Operation = "app.elements.remove"
	OperationPeerSwapStatus                  Operation = "app.peerswap.status"
	OperationPeerSwapSourceRead              Operation = "app.peerswap.source.read"
	OperationPeerSwapSourceWrite             Operation = "app.peerswap.source.write"
	OperationPeerSwapEnsure                  Operation = "app.peerswap.ensure"
	OperationPeerSwapLifecycle               Operation = "app.peerswap.lifecycle"
	OperationPeerSwapRemove                  Operation = "app.peerswap.remove"
	OperationTapdStatus                      Operation = "app.tapd.status"
	OperationTapdEnsure                      Operation = "app.tapd.ensure"
	OperationTapdLifecycle                   Operation = "app.tapd.lifecycle"
	OperationTapdRemove                      Operation = "app.tapd.remove"
	OperationTapdCLI                         Operation = "app.tapd.cli"
	OperationPublicPoolStatus                Operation = "app.publicpool.status"
	OperationPublicPoolEnsure                Operation = "app.publicpool.ensure"
	OperationPublicPoolLifecycle             Operation = "app.publicpool.lifecycle"
	OperationPublicPoolRemove                Operation = "app.publicpool.remove"
	OperationPublicPoolFirewall              Operation = "app.publicpool.firewall"
	OperationBarkWalletStatus                Operation = "app.bark.status"
	OperationBarkWalletEnsure                Operation = "app.bark.ensure"
	OperationBarkWalletLifecycle             Operation = "app.bark.lifecycle"
	OperationBarkWalletRemove                Operation = "app.bark.remove"
	OperationBarkWalletFirewall              Operation = "app.bark.firewall"
	OperationBarkWalletPasswordRead          Operation = "app.bark.password.read"
	OperationBarkWalletPasswordReset         Operation = "app.bark.password.reset"
)

type Request struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	Operation Operation       `json:"operation"`
	DryRun    bool            `json:"dry_run,omitempty"`
	Params    json.RawMessage `json:"params"`
}

type Response struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id,omitempty"`
	OK        bool            `json:"ok"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *ResponseError  `json:"error,omitempty"`
}

type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ServiceStatusParams struct {
	Unit string `json:"unit"`
}

type ServiceRestartParams struct {
	Unit    string `json:"unit"`
	NoBlock bool   `json:"no_block,omitempty"`
}

type HostPowerParams struct {
	Action string `json:"action"`
}

type HostPowerState struct {
	Validated bool `json:"validated"`
	Scheduled bool `json:"scheduled"`
}

type SystemIntegrationAsset string

const (
	SystemIntegrationAssetTerminal        SystemIntegrationAsset = "terminal"
	SystemIntegrationAssetManagerFirewall SystemIntegrationAsset = "manager_firewall"
	SystemIntegrationAssetManagerTLSMDNS  SystemIntegrationAsset = "manager_tls_mdns"
)

type SystemIntegrationAssetInstallParams struct {
	Asset   SystemIntegrationAsset `json:"asset"`
	Content string                 `json:"content"`
}

type SystemIntegrationAssetState struct {
	Status  string `json:"status"`
	Changed bool   `json:"changed,omitempty"`
}

type SystemIntegrationsState struct {
	Status             string `json:"status"`
	CertificateChanged bool   `json:"certificate_changed,omitempty"`
	LNDPolicyChanged   bool   `json:"lnd_policy_changed,omitempty"`
}

type TerminalCredentialRotateParams struct {
	OperatorUser string `json:"operator_user"`
	Password     string `json:"password"`
}

type TerminalCredentialState struct {
	Status       string `json:"status"`
	OperatorUser string `json:"operator_user"`
}

type TerminalControlAction string

const (
	TerminalControlEnable  TerminalControlAction = "enable"
	TerminalControlDisable TerminalControlAction = "disable"
)

type TerminalControlParams struct {
	Action TerminalControlAction `json:"action"`
}

type TerminalControlState struct {
	Status  string `json:"status"`
	Enabled bool   `json:"enabled"`
}

type ManagerFirewallState struct {
	Installed          bool   `json:"installed"`
	Active             bool   `json:"active"`
	ConfiguredCIDR     string `json:"configured_cidr"`
	ConfigValid        bool   `json:"config_valid"`
	LANRulePresent     bool   `json:"lan_rule_present"`
	TailscaleRule      bool   `json:"tailscale_rule"`
	BroadRulePresent   bool   `json:"broad_rule_present"`
	ManagerAccessBound bool   `json:"manager_access_bound"`
	StatusAvailable    bool   `json:"status_available"`
}

type AppStorageState struct {
	Status  string `json:"status"`
	Changed bool   `json:"changed,omitempty"`
}

type SMARTReadParams struct {
	Device string `json:"device"`
}

type SMARTReadState struct {
	Device    string `json:"device"`
	Output    string `json:"output,omitempty"`
	Available bool   `json:"available"`
}

type LNDPermissionsState struct {
	Status  string `json:"status"`
	Changed bool   `json:"changed,omitempty"`
}

type LNDManagerCredentialState struct {
	Status         string `json:"status"`
	Changed        bool   `json:"changed,omitempty"`
	ConfiguredPath string `json:"configured_path,omitempty"`
	AdminProtected bool   `json:"admin_protected,omitempty"`
}

type LNDUpgradeStartParams struct {
	Version       string `json:"version"`
	HelperContent string `json:"helper_content"`
	VerifyOnly    bool   `json:"verify_only,omitempty"`
}

type LNDUpgradeState struct {
	Status     string `json:"status"`
	Unit       string `json:"unit"`
	Version    string `json:"version"`
	VerifyOnly bool   `json:"verify_only,omitempty"`
}

type TorUpgradeStartParams struct {
	HelperContent string `json:"helper_content"`
	VerifyOnly    bool   `json:"verify_only,omitempty"`
}

type TorUpgradeState struct {
	Status     string `json:"status"`
	Unit       string `json:"unit,omitempty"`
	VerifyOnly bool   `json:"verify_only,omitempty"`
}

type LightningOSUpgradeStartParams struct {
	Version       string `json:"version"`
	Tag           string `json:"tag"`
	Commit        string `json:"commit"`
	HelperContent string `json:"helper_content"`
	VerifyOnly    bool   `json:"verify_only,omitempty"`
}

type LightningOSUpgradeState struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Unit       string `json:"unit"`
	VerifyOnly bool   `json:"verify_only,omitempty"`
}

type AppLifecycleAction string

const (
	AppLifecycleStart   AppLifecycleAction = "start"
	AppLifecycleStop    AppLifecycleAction = "stop"
	AppLifecycleRestart AppLifecycleAction = "restart"
)

type AppLifecycleParams struct {
	AppID  string             `json:"app_id"`
	Action AppLifecycleAction `json:"action"`
}

type AppInspectParams struct {
	AppID string `json:"app_id"`
}

type AppLogsParams struct {
	AppID string `json:"app_id"`
	Lines int    `json:"lines"`
	Since string `json:"since,omitempty"`
}

type AppLogsState struct {
	Lines  []string `json:"lines"`
	Source string   `json:"source"`
}

type AppSnapshotParams struct {
	AppID string `json:"app_id"`
}

type CatalogStorageParams struct {
	AppID    string `json:"app_id"`
	DataDir  string `json:"data_dir"`
	Finalize bool   `json:"finalize,omitempty"`
}

type CatalogStorageState struct {
	Status string `json:"status"`
}

type AppRemoveParams struct {
	AppID string `json:"app_id"`
}

type AppAdminResetParams struct {
	AppID string `json:"app_id"`
}

type AppImageParams struct {
	AppID   string                      `json:"app_id"`
	Variant appmanifest.AppImageVariant `json:"variant"`
}

type AppImageState struct {
	Status string `json:"status"`
}

type AppImageProbe struct {
	Runnable bool `json:"runnable"`
}

type AppFirewallParams struct {
	AppID string `json:"app_id"`
}

type AppFirewallState struct {
	Status string `json:"status"`
}

type AppLNDHostAccessParams struct {
	AppID string `json:"app_id"`
}

type BitcoinCoreStorageParams struct {
	DataDir string `json:"data_dir"`
}

type BitcoinCoreStorageState struct {
	Status string `json:"status"`
}

type BitcoinCoreConfigTargetParams struct {
	DataDir string `json:"data_dir"`
}

type BitcoinCoreConfigWriteParams struct {
	DataDir         string `json:"data_dir"`
	Content         string `json:"content"`
	GenerateRPCAuth bool   `json:"generate_rpcauth,omitempty"`
}

type BitcoinCoreConfigState struct {
	Status  string `json:"status"`
	Content string `json:"content,omitempty"`
}

type BitcoinCoreCredentialsState struct {
	Status   string `json:"status"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type BitcoinCoreCredentialsEnsureState struct {
	Status        string `json:"status"`
	User          string `json:"user,omitempty"`
	Password      string `json:"password,omitempty"`
	ConfigChanged bool   `json:"config_changed,omitempty"`
}

type BitcoinCoreElectrsCredentialsState struct {
	Status        string `json:"status"`
	User          string `json:"user,omitempty"`
	Password      string `json:"password,omitempty"`
	ConfigChanged bool   `json:"config_changed,omitempty"`
}

type BitcoinCoreStatusState struct {
	Chain                 string                     `json:"chain"`
	Blocks                int64                      `json:"blocks"`
	Headers               int64                      `json:"headers"`
	BestBlockTime         int64                      `json:"best_block_time,omitempty"`
	BlockCadenceWindowSec int64                      `json:"block_cadence_window_sec,omitempty"`
	BlockCadence          []BitcoinCoreCadenceBucket `json:"block_cadence,omitempty"`
	VerificationProgress  float64                    `json:"verification_progress"`
	InitialBlockDownload  bool                       `json:"initial_block_download"`
	BestBlockHash         string                     `json:"best_block_hash"`
	Pruned                bool                       `json:"pruned"`
	PruneHeight           int64                      `json:"prune_height,omitempty"`
	PruneTargetSize       int64                      `json:"prune_target_size,omitempty"`
	SizeOnDisk            int64                      `json:"size_on_disk,omitempty"`
	NetworkOK             bool                       `json:"network_ok"`
	Version               int                        `json:"version,omitempty"`
	Subversion            string                     `json:"subversion,omitempty"`
	Connections           int                        `json:"connections,omitempty"`
}

type BitcoinCoreCadenceBucket struct {
	StartTime int64 `json:"start_time"`
	EndTime   int64 `json:"end_time"`
	Count     int   `json:"count"`
}

type BitcoinConsumerNetworkState struct {
	Status string `json:"status"`
}

type DockerRuntimeState struct {
	Status string `json:"status"`
}

type PackageFeature string

const (
	PackageFeatureDockerRuntime PackageFeature = "docker_runtime"
	PackageFeatureMDNS          PackageFeature = "mdns"
)

type PackageFeatureParams struct {
	Feature PackageFeature `json:"feature"`
}

type PackageFeatureState struct {
	Status string `json:"status"`
}

type AppInspection struct {
	Status        string  `json:"status"`
	CPUPercentRaw float64 `json:"cpu_percent_raw"`
}

type LoopEnsureParams struct {
	LNDTLSCertificate []byte `json:"lnd_tls_certificate"`
	LNDMacaroon       []byte `json:"lnd_macaroon,omitempty"`
}

type LoopLifecycleParams struct {
	Action AppLifecycleAction `json:"action"`
}

type LoopState struct {
	Installed          bool   `json:"installed"`
	Status             string `json:"status"`
	HasLNDMacaroon     bool   `json:"has_lnd_macaroon"`
	HasPersistentState bool   `json:"has_persistent_state"`
}

type ElementsTargetParams struct {
	DataDir string `json:"data_dir"`
}

type ElementsEnsureParams struct {
	DataDir string `json:"data_dir"`
	Content string `json:"content"`
}

type ElementsLifecycleParams struct {
	DataDir string             `json:"data_dir"`
	Action  AppLifecycleAction `json:"action"`
}

type ElementsConfigState struct {
	Status  string `json:"status"`
	Content string `json:"content,omitempty"`
}

type ElementsState struct {
	Installed            bool    `json:"installed"`
	Status               string  `json:"status"`
	DataDir              string  `json:"data_dir"`
	RPCOK                bool    `json:"rpc_ok"`
	Chain                string  `json:"chain,omitempty"`
	Blocks               int64   `json:"blocks,omitempty"`
	Headers              int64   `json:"headers,omitempty"`
	VerificationProgress float64 `json:"verification_progress,omitempty"`
	InitialBlockDownload bool    `json:"initial_block_download,omitempty"`
	Peers                int     `json:"peers,omitempty"`
	Version              int     `json:"version,omitempty"`
	Subversion           string  `json:"subversion,omitempty"`
	SizeOnDisk           int64   `json:"size_on_disk,omitempty"`
}

type PeerSwapSource struct {
	Mode     string `json:"mode"`
	URL      string `json:"url,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Wallet   string `json:"wallet,omitempty"`
}

type PeerSwapSourceState struct {
	Configured bool           `json:"configured"`
	Source     PeerSwapSource `json:"source"`
}

type PeerSwapEnsureParams struct {
	ElementsMode      string `json:"elements_mode"`
	Config            string `json:"config"`
	WebConfig         string `json:"web_config"`
	LNDTLSCertificate []byte `json:"lnd_tls_certificate"`
	LNDMacaroon       []byte `json:"lnd_macaroon,omitempty"`
}

type PeerSwapLifecycleParams struct {
	Action AppLifecycleAction `json:"action"`
}

type PeerSwapState struct {
	Installed      bool   `json:"installed"`
	Status         string `json:"status"`
	HasLNDMacaroon bool   `json:"has_lnd_macaroon"`
	ElementsMode   string `json:"elements_mode,omitempty"`
}

type TapdEnsureParams struct {
	DatabasePassword  string `json:"database_password"`
	LNDTLSCertificate []byte `json:"lnd_tls_certificate"`
	LNDMacaroon       []byte `json:"lnd_macaroon,omitempty"`
}

type TapdLifecycleParams struct {
	Action AppLifecycleAction `json:"action"`
}

type TapdState struct {
	Installed           bool   `json:"installed"`
	Status              string `json:"status"`
	HasLNDMacaroon      bool   `json:"has_lnd_macaroon"`
	InterceptorConflict bool   `json:"interceptor_conflict"`
}

type TapdCLIResult struct {
	Output string `json:"output"`
}

type PublicPoolEnsureParams struct {
	Runtime appmanifest.PublicPoolRuntime `json:"runtime"`
}

type PublicPoolLifecycleParams struct {
	Action AppLifecycleAction `json:"action"`
}

type PublicPoolState struct {
	Installed bool   `json:"installed"`
	Status    string `json:"status"`
	UFWActive bool   `json:"ufw_active,omitempty"`
}

type BarkWalletLifecycleParams struct {
	Action AppLifecycleAction `json:"action"`
}

type BarkWalletState struct {
	Installed         bool   `json:"installed"`
	Status            string `json:"status"`
	UFWActive         bool   `json:"ufw_active,omitempty"`
	PasswordAvailable bool   `json:"password_available,omitempty"`
}

type BarkWalletPasswordResult struct {
	Password string `json:"password"`
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var (
	lndUpgradeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z\.-]*)?$`)
	gitCommitPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	smartDevicePattern       = regexp.MustCompile(`^/dev/[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

var allowedServiceUnits = map[string]struct{}{
	"autofee":                      {},
	"lightningos-app-upgrade":      {},
	"lightningos-elements":         {},
	"lightningos-lnd-upgrade":      {},
	"lightningos-manager":          {},
	"lightningos-peerswapd":        {},
	"lightningos-psweb":            {},
	"lightningos-terminal":         {},
	"lightningos-terminal.service": {},
	"lightningos-tor-upgrade":      {},
	"lnd":                          {},
	"lnd@default":                  {},
	"postgresql":                   {},
}

func DecodeRequest(reader io.Reader) (Request, error) {
	var request Request
	data, err := readBounded(reader)
	if err != nil {
		return request, err
	}
	if err := decodeStrict(data, &request); err != nil {
		return request, fmt.Errorf("invalid request: %w", err)
	}
	if err := ValidateRequest(request); err != nil {
		return request, err
	}
	return request, nil
}

func DecodeResponse(reader io.Reader) (Response, error) {
	var response Response
	data, err := readBounded(reader)
	if err != nil {
		return response, err
	}
	if err := decodeStrict(data, &response); err != nil {
		return response, fmt.Errorf("invalid response: %w", err)
	}
	if response.Version != ProtocolVersion {
		return response, fmt.Errorf("unsupported response version")
	}
	if response.OK && response.Error != nil {
		return response, errors.New("invalid successful response")
	}
	if !response.OK && response.Error == nil {
		return response, errors.New("invalid error response")
	}
	return response, nil
}

func ValidateRequest(request Request) error {
	if request.Version != ProtocolVersion {
		return errors.New("unsupported protocol version")
	}
	if !requestIDPattern.MatchString(request.RequestID) {
		return errors.New("invalid request_id")
	}
	if len(request.Params) == 0 {
		return errors.New("params required")
	}
	if bytes.Equal(bytes.TrimSpace(request.Params), []byte("null")) {
		return errors.New("params must be an object")
	}

	switch request.Operation {
	case OperationSelfTest:
		if request.DryRun {
			return errors.New("dry_run is not valid for self_test")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid self_test params: %w", err)
		}
	case OperationServiceStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for service.status")
		}
		var params ServiceStatusParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid service.status params: %w", err)
		}
		if err := ValidateServiceUnit(params.Unit); err != nil {
			return err
		}
	case OperationServiceRestart:
		var params ServiceRestartParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid service.restart params: %w", err)
		}
		if err := ValidateServiceUnit(params.Unit); err != nil {
			return err
		}
		if params.Unit == "lightningos-manager" && !params.NoBlock {
			return errors.New("manager restart must be non-blocking")
		}
	case OperationHostPower:
		var params HostPowerParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid host.power params: %w", err)
		}
		if params.Action != "reboot" && params.Action != "poweroff" {
			return errors.New("host power action is not allowed")
		}
	case OperationFilesEnableLogin:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid files.enable_login params: %w", err)
		}
	case OperationSystemIntegrationAssetInstall:
		var params SystemIntegrationAssetInstallParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid files.system-integration.install params: %w", err)
		}
		if !validSystemIntegrationAsset(params.Asset) {
			return errors.New("system integration asset is not allowed")
		}
		if len(params.Content) == 0 || len(params.Content) > 16*1024 {
			return errors.New("system integration asset content is invalid")
		}
	case OperationSystemIntegrationsStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for system.integrations.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid system.integrations.status params: %w", err)
		}
	case OperationSystemIntegrationsApply, OperationSystemIntegrationsFinalize:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationTerminalCredentialRotate:
		var params TerminalCredentialRotateParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid terminal.credential.rotate params: %w", err)
		}
		if !systemIntegrationIdentityPattern.MatchString(params.OperatorUser) {
			return errors.New("terminal operator user is invalid")
		}
		if !terminalCredentialPasswordPattern.MatchString(params.Password) {
			return errors.New("terminal credential password is invalid")
		}
	case OperationTerminalControl:
		var params TerminalControlParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid terminal.control params: %w", err)
		}
		if params.Action != TerminalControlEnable && params.Action != TerminalControlDisable {
			return errors.New("terminal control action is not allowed")
		}
	case OperationManagerFirewallStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for manager.firewall.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid manager.firewall.status params: %w", err)
		}
	case OperationLNDUpgradeStart:
		var params LNDUpgradeStartParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid upgrade.lnd.start params: %w", err)
		}
		if !lndUpgradeVersionPattern.MatchString(params.Version) {
			return errors.New("LND upgrade version is invalid")
		}
		if len(params.HelperContent) == 0 || len(params.HelperContent) > 48*1024 {
			return errors.New("LND upgrade helper content is invalid")
		}
	case OperationTorMetadataRefresh:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid packages.tor.refresh params: %w", err)
		}
	case OperationTorUpgradeStart:
		var params TorUpgradeStartParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid upgrade.tor.start params: %w", err)
		}
		if len(params.HelperContent) == 0 || len(params.HelperContent) > 48*1024 {
			return errors.New("Tor upgrade helper content is invalid")
		}
	case OperationLightningOSUpgradeStart:
		var params LightningOSUpgradeStartParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid upgrade.lightningos.start params: %w", err)
		}
		if !lndUpgradeVersionPattern.MatchString(params.Version) {
			return errors.New("LightningOS upgrade version is invalid")
		}
		tagVersion := strings.TrimPrefix(strings.TrimPrefix(params.Tag, "v"), "V")
		if !strings.EqualFold(tagVersion, params.Version) || !lndUpgradeVersionPattern.MatchString(strings.ToLower(tagVersion)) {
			return errors.New("LightningOS upgrade tag is invalid")
		}
		if !gitCommitPattern.MatchString(params.Commit) {
			return errors.New("LightningOS upgrade commit is invalid")
		}
		if len(params.HelperContent) == 0 || len(params.HelperContent) > 48*1024 {
			return errors.New("LightningOS upgrade helper content is invalid")
		}
	case OperationAppLifecycle:
		var params AppLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.lifecycle params: %w", err)
		}
		if _, err := appmanifest.ComposeManifestForApp(params.AppID); err != nil {
			return errors.New("app manifest is not allowed")
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop && params.Action != AppLifecycleRestart {
			return errors.New("app lifecycle action is not allowed")
		}
		if params.Action == AppLifecycleRestart && params.AppID != appmanifest.BitcoinCoreID {
			return errors.New("app lifecycle action is not allowed")
		}
	case OperationAppSnapshot:
		var params AppSnapshotParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.snapshot params: %w", err)
		}
		if params.AppID != appmanifest.BTCPayID {
			return errors.New("app snapshot manifest is not allowed")
		}
	case OperationAppInspect:
		if request.DryRun {
			return errors.New("dry_run is not valid for app.compose.inspect")
		}
		var params AppInspectParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.inspect params: %w", err)
		}
		if _, err := appmanifest.ComposeManifestForApp(params.AppID); err != nil {
			return errors.New("app manifest is not allowed")
		}
	case OperationAppLogs:
		if request.DryRun {
			return errors.New("dry_run is not valid for app.compose.logs")
		}
		var params AppLogsParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.logs params: %w", err)
		}
		if params.AppID != appmanifest.BitcoinCoreID && params.AppID != appmanifest.FedimintGuardianID && params.AppID != appmanifest.FedimintGatewayID {
			return errors.New("app log manifest is not allowed")
		}
		if params.Lines < 1 || params.Lines > 500 || !validComposeLogSince(params.Since) {
			return errors.New("app log query is invalid")
		}
	case OperationAppRemove:
		var params AppRemoveParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.remove params: %w", err)
		}
		if _, err := appmanifest.ComposeManifestForApp(params.AppID); err != nil {
			return errors.New("app manifest is not allowed")
		}
	case OperationAppAdminReset:
		var params AppAdminResetParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.admin.reset params: %w", err)
		}
		if params.AppID != appmanifest.LNDgID {
			return errors.New("app admin reset manifest is not allowed")
		}
	case OperationDockerEnsure:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid docker.runtime.ensure params: %w", err)
		}
	case OperationDockerStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for docker.runtime.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid docker.runtime.status params: %w", err)
		}
	case OperationPackageEnsure:
		var params PackageFeatureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid packages.feature.ensure params: %w", err)
		}
		if !validPackageFeature(params.Feature) {
			return errors.New("package feature is not allowed")
		}
	case OperationPackageStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for packages.feature.status")
		}
		var params PackageFeatureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid packages.feature.status params: %w", err)
		}
		if !validPackageFeature(params.Feature) {
			return errors.New("package feature is not allowed")
		}
	case OperationAppStorageEnsure:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid storage.apps.ensure params: %w", err)
		}
	case OperationCatalogStorageEnsure:
		var params CatalogStorageParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.catalog.storage.ensure params: %w", err)
		}
		normalized, err := appmanifest.NormalizeCatalogDataDir(params.AppID, params.DataDir)
		if err != nil || normalized != params.DataDir {
			return errors.New("catalog storage target is not allowed")
		}
	case OperationSMARTRead:
		if request.DryRun {
			return errors.New("dry_run is not valid for storage.smart.read")
		}
		var params SMARTReadParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid storage.smart.read params: %w", err)
		}
		if !smartDevicePattern.MatchString(params.Device) {
			return errors.New("SMART device is invalid")
		}
	case OperationLNDPermissionsRepair:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid storage.lnd.permissions.repair params: %w", err)
		}
	case OperationLNDManagerCredentialEnsure, OperationLNDManagerCredentialRollback:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationAppImagePrepare:
		var params AppImageParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
		if err := validateCatalogImageParams(params); err != nil {
			return err
		}
	case OperationAppImageProbe:
		var params AppImageParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.image.probe params: %w", err)
		}
		if err := validateProbedImageParams(params); err != nil {
			return err
		}
	case OperationAppImageStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for app.image.status")
		}
		var params AppImageParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.image.status params: %w", err)
		}
		if err := validateCatalogImageParams(params); err != nil {
			return err
		}
	case OperationAppFirewallEnsure:
		var params AppFirewallParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.firewall.ensure params: %w", err)
		}
		if _, err := appmanifest.CatalogExternalTCPPort(params.AppID); err != nil {
			return errors.New("app external access manifest is not allowed")
		}
	case OperationAppLNDHostAccessEnsure:
		var params AppLNDHostAccessParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.lnd-host-access.ensure params: %w", err)
		}
		if params.AppID != appmanifest.BTCPayID {
			return errors.New("app LND host access manifest is not allowed")
		}
	case OperationBitcoinStorageEnsure:
		var params BitcoinCoreStorageParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.bitcoincore.storage.ensure params: %w", err)
		}
		normalized, err := appmanifest.NormalizeBitcoinCoreDataDir(params.DataDir)
		if err != nil || normalized != params.DataDir {
			return errors.New("bitcoin storage target is not allowed")
		}
	case OperationBitcoinConfigRead, OperationBitcoinCredentialsRead:
		if request.DryRun {
			return fmt.Errorf("dry_run is not valid for %s", request.Operation)
		}
		var params BitcoinCoreConfigTargetParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.bitcoincore.config.read params: %w", err)
		}
		if err := validateBitcoinCoreConfigDataDir(params.DataDir); err != nil {
			return err
		}
	case OperationBitcoinCredentialsEnsure, OperationBitcoinElectrsCredentialsEnsure:
		var params BitcoinCoreConfigTargetParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
		if err := validateBitcoinCoreConfigDataDir(params.DataDir); err != nil {
			return err
		}
	case OperationBitcoinStatus:
		if request.DryRun {
			return errors.New("dry_run is not valid for app.bitcoincore.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.bitcoincore.status params: %w", err)
		}
	case OperationBitcoinConfigEnsure, OperationBitcoinConfigWrite:
		var params BitcoinCoreConfigWriteParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
		if err := validateBitcoinCoreConfigDataDir(params.DataDir); err != nil {
			return err
		}
		if err := validateBitcoinCoreConfigContent(params.Content); err != nil {
			return err
		}
		if request.Operation == OperationBitcoinConfigWrite && params.GenerateRPCAuth {
			return errors.New("generate_rpcauth is valid only for app.bitcoincore.config.ensure")
		}
	case OperationBitcoinConsumerNetworkEnsure:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid bitcoin.consumer-network.ensure params: %w", err)
		}
	case OperationLoopStatus, OperationLoopRemove, OperationLoopPermissionsEnsure, OperationLoopClientMaterialEnsure:
		if request.DryRun && request.Operation == OperationLoopStatus {
			return errors.New("dry_run is not valid for app.loop.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationLoopEnsure:
		var params LoopEnsureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.loop.ensure params: %w", err)
		}
		if err := appmanifest.ValidateLoopMaterial(params.LNDTLSCertificate, params.LNDMacaroon); err != nil {
			return err
		}
	case OperationLoopLifecycle:
		var params LoopLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.loop.lifecycle params: %w", err)
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("Loop lifecycle action is not allowed")
		}
	case OperationElementsStatus, OperationElementsConfigRead, OperationElementsRemove:
		if request.DryRun && (request.Operation == OperationElementsStatus || request.Operation == OperationElementsConfigRead) {
			return fmt.Errorf("dry_run is not valid for %s", request.Operation)
		}
		var params ElementsTargetParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
		if normalized, err := appmanifest.NormalizeElementsDataDir(params.DataDir); err != nil || normalized != params.DataDir {
			return errors.New("Elements data directory is not allowed")
		}
	case OperationElementsEnsure:
		var params ElementsEnsureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.elements.ensure params: %w", err)
		}
		if normalized, err := appmanifest.NormalizeElementsDataDir(params.DataDir); err != nil || normalized != params.DataDir {
			return errors.New("Elements data directory is not allowed")
		}
		if err := appmanifest.ValidateElementsConfig(params.Content); err != nil {
			return err
		}
	case OperationElementsLifecycle:
		var params ElementsLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.elements.lifecycle params: %w", err)
		}
		if normalized, err := appmanifest.NormalizeElementsDataDir(params.DataDir); err != nil || normalized != params.DataDir {
			return errors.New("Elements data directory is not allowed")
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("Elements lifecycle action is not allowed")
		}
	case OperationPeerSwapStatus, OperationPeerSwapSourceRead, OperationPeerSwapRemove:
		if request.DryRun && (request.Operation == OperationPeerSwapStatus || request.Operation == OperationPeerSwapSourceRead) {
			return fmt.Errorf("dry_run is not valid for %s", request.Operation)
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationPeerSwapSourceWrite:
		var params PeerSwapSource
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.peerswap.source.write params: %w", err)
		}
		if err := appmanifest.ValidatePeerSwapSource(params.Mode, params.URL, params.User, params.Password, params.Wallet); err != nil {
			return err
		}
	case OperationPeerSwapEnsure:
		var params PeerSwapEnsureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.peerswap.ensure params: %w", err)
		}
		paths := appmanifest.DefaultPeerSwapPaths()
		if err := appmanifest.ValidatePeerSwapMaterial(params.LNDTLSCertificate, params.LNDMacaroon); err != nil {
			return err
		}
		if err := appmanifest.ValidatePeerSwapConfig(params.Config, params.ElementsMode, paths); err != nil {
			return err
		}
		if err := appmanifest.ValidatePeerSwapWebConfig(params.WebConfig, appmanifest.DefaultPeerSwapPaths()); err != nil {
			return err
		}
	case OperationPeerSwapLifecycle:
		var params PeerSwapLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.peerswap.lifecycle params: %w", err)
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop && params.Action != AppLifecycleRestart {
			return errors.New("PeerSwap lifecycle action is not allowed")
		}
	case OperationTapdStatus, OperationTapdRemove:
		if request.DryRun && request.Operation == OperationTapdStatus {
			return errors.New("dry_run is not valid for app.tapd.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationTapdEnsure:
		var params TapdEnsureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.tapd.ensure params: %w", err)
		}
		if _, err := appmanifest.TapdConfig(params.DatabasePassword); err != nil {
			return err
		}
		if len(params.LNDTLSCertificate) == 0 || len(params.LNDTLSCertificate) > 64*1024 || len(params.LNDMacaroon) > 64*1024 {
			return errors.New("Tapd LND material is invalid")
		}
	case OperationTapdLifecycle:
		var params TapdLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.tapd.lifecycle params: %w", err)
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("Tapd lifecycle action is not allowed")
		}
	case OperationTapdCLI:
		if request.DryRun {
			return errors.New("dry_run is not valid for app.tapd.cli")
		}
		var params appmanifest.TapdCLIRequest
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.tapd.cli params: %w", err)
		}
		if err := appmanifest.ValidateTapdCLIRequest(params); err != nil {
			return err
		}
	case OperationPublicPoolStatus, OperationPublicPoolRemove, OperationPublicPoolFirewall:
		if request.DryRun && request.Operation == OperationPublicPoolStatus {
			return errors.New("dry_run is not valid for app.publicpool.status")
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationPublicPoolEnsure:
		var params PublicPoolEnsureParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.publicpool.ensure params: %w", err)
		}
		if err := appmanifest.ValidatePublicPoolRuntime(params.Runtime); err != nil {
			return err
		}
	case OperationPublicPoolLifecycle:
		var params PublicPoolLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.publicpool.lifecycle params: %w", err)
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("Public Pool lifecycle action is not allowed")
		}
	case OperationBarkWalletStatus, OperationBarkWalletEnsure, OperationBarkWalletRemove,
		OperationBarkWalletFirewall, OperationBarkWalletPasswordRead, OperationBarkWalletPasswordReset:
		if request.DryRun && (request.Operation == OperationBarkWalletStatus || request.Operation == OperationBarkWalletPasswordRead) {
			return fmt.Errorf("dry_run is not valid for %s", request.Operation)
		}
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid %s params: %w", request.Operation, err)
		}
	case OperationBarkWalletLifecycle:
		var params BarkWalletLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.bark.lifecycle params: %w", err)
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("Bark Wallet lifecycle action is not allowed")
		}
	default:
		return errors.New("unknown operation")
	}
	return nil
}

func validSystemIntegrationAsset(asset SystemIntegrationAsset) bool {
	switch asset {
	case SystemIntegrationAssetTerminal,
		SystemIntegrationAssetManagerFirewall,
		SystemIntegrationAssetManagerTLSMDNS:
		return true
	default:
		return false
	}
}

func validateBitcoinCoreConfigDataDir(dataDir string) error {
	normalized, err := appmanifest.NormalizeBitcoinCoreDataDir(dataDir)
	if err != nil || normalized != dataDir {
		return errors.New("bitcoin config target is not allowed")
	}
	return nil
}

func validateCatalogImageParams(params AppImageParams) error {
	if _, err := appmanifest.CatalogImageForVariant(params.AppID, params.Variant); err != nil {
		return errors.New("app image variant is not allowed")
	}
	return nil
}

func validateProbedImageParams(params AppImageParams) error {
	if params.AppID != appmanifest.CPUMinerID && params.AppID != appmanifest.TapdID && params.AppID != appmanifest.PublicPoolID && params.AppID != appmanifest.BarkWalletID && params.AppID != appmanifest.MempoolID && params.AppID != appmanifest.FedimintGuardianID && params.AppID != appmanifest.FedimintGatewayID {
		return errors.New("app manifest is not allowed")
	}
	if _, err := appmanifest.CatalogImageForVariant(params.AppID, params.Variant); err != nil {
		return errors.New("app image variant is not allowed")
	}
	return nil
}

func validComposeLogSince(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 64 || strings.HasPrefix(value, "-") {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune(".:+_-", char) {
			continue
		}
		return false
	}
	return true
}

func ValidateServiceUnit(unit string) error {
	if strings.TrimSpace(unit) != unit || unit == "" {
		return errors.New("invalid service unit")
	}
	if _, ok := allowedServiceUnits[unit]; !ok {
		return errors.New("service unit is not allowed")
	}
	return nil
}

func validPackageFeature(feature PackageFeature) bool {
	switch feature {
	case PackageFeatureDockerRuntime, PackageFeatureMDNS:
		return true
	default:
		return false
	}
}

func MarshalParams(params any) (json.RawMessage, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func SuccessResponse(request Request, result any) Response {
	raw, err := json.Marshal(result)
	if err != nil {
		return ErrorResponse(request.RequestID, "internal_error", "failed to encode response")
	}
	return Response{
		Version:   ProtocolVersion,
		RequestID: request.RequestID,
		OK:        true,
		Result:    raw,
	}
}

func ErrorResponse(requestID string, code string, message string) Response {
	if !requestIDPattern.MatchString(requestID) {
		requestID = ""
	}
	return Response{
		Version:   ProtocolVersion,
		RequestID: requestID,
		OK:        false,
		Error: &ResponseError{
			Code:    code,
			Message: message,
		},
	}
}

func readBounded(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxMessageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxMessageBytes {
		return nil, errors.New("message too large")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty message")
	}
	return data, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
