package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

const (
	systemctlPath                     = "/usr/bin/systemctl"
	systemdRunPath                    = "/usr/bin/systemd-run"
	privilegedLongOperationTimeout    = 2 * time.Minute
	privilegedBitcoinStatusTimeout    = 45 * time.Second
	privilegedImageProbeTimeout       = 30 * time.Second
	privilegedStorageMigrationTimeout = 90 * time.Minute
)

type CommandRunner interface {
	Run(ctx context.Context, path string, args ...string) (string, error)
}

type Locker interface {
	Lock(ctx context.Context) (unlock func(), err error)
}

type AuditSink interface {
	Write(event AuditEvent) error
}

type ConfigFileManager interface {
	EnableLogin(ctx context.Context, dryRun bool) (changed bool, err error)
}

type ManagerFirewallManager interface {
	Status(ctx context.Context) (ManagerFirewallState, error)
}

type LNDUpgradeManager interface {
	Start(ctx context.Context, params LNDUpgradeStartParams, dryRun bool) (LNDUpgradeState, error)
}

type TorUpgradeManager interface {
	Refresh(ctx context.Context, dryRun bool) (TorUpgradeState, error)
	Start(ctx context.Context, params TorUpgradeStartParams, dryRun bool) (TorUpgradeState, error)
}

type LightningOSUpgradeManager interface {
	Start(ctx context.Context, params LightningOSUpgradeStartParams, dryRun bool) (LightningOSUpgradeState, error)
}

type AppManager interface {
	EnsureDockerRuntime(ctx context.Context, dryRun bool) (DockerRuntimeState, error)
	DockerRuntimeStatus(ctx context.Context) (DockerRuntimeState, error)
	Lifecycle(ctx context.Context, appID string, action AppLifecycleAction, dryRun bool) error
	Snapshot(ctx context.Context, appID string, dryRun bool) error
	Inspect(ctx context.Context, appID string) (AppInspection, error)
	Remove(ctx context.Context, appID string, dryRun bool) error
	PrepareImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageState, error)
	ImageStatus(ctx context.Context, appID string, variant appmanifest.AppImageVariant) (AppImageState, error)
	ProbeImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageProbe, error)
	EnsureFirewallAccess(ctx context.Context, appID string, dryRun bool) (AppFirewallState, error)
	EnsureBitcoinConsumerNetwork(ctx context.Context, dryRun bool) (BitcoinConsumerNetworkState, error)
	BitcoinCoreStatus(ctx context.Context) (BitcoinCoreStatusState, error)
}

type AppAdminManager interface {
	ResetAdmin(ctx context.Context, appID string, dryRun bool) error
}

type AppLogManager interface {
	Logs(ctx context.Context, appID string, lines int, since string) (AppLogsState, error)
}

type AppLNDHostAccessManager interface {
	EnsureLNDHostAccess(ctx context.Context, appID string, dryRun bool) error
}

type PackageManager interface {
	EnsureFeature(ctx context.Context, feature PackageFeature, dryRun bool) (PackageFeatureState, error)
	FeatureStatus(ctx context.Context, feature PackageFeature) (PackageFeatureState, error)
}

type AppStorageManager interface {
	Ensure(ctx context.Context, dryRun bool) (AppStorageState, error)
}

type CatalogStorageManager interface {
	Ensure(ctx context.Context, appID string, dataDir string, finalize bool, dryRun bool) (CatalogStorageState, error)
}

type SMARTManager interface {
	Read(ctx context.Context, device string) (SMARTReadState, error)
}

type LNDPermissionsManager interface {
	Repair(ctx context.Context, dryRun bool) (LNDPermissionsState, error)
}

type LNDManagerCredentialManager interface {
	Ensure(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error)
	Rollback(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error)
}

type BitcoinStorageManager interface {
	Ensure(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreStorageState, error)
}

type BitcoinConfigManager interface {
	Ensure(ctx context.Context, dataDir string, content string, generateRPCAuth bool, dryRun bool) (BitcoinCoreConfigState, error)
	Read(ctx context.Context, dataDir string) (BitcoinCoreConfigState, error)
	Write(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error)
	Credentials(ctx context.Context, dataDir string) (BitcoinCoreCredentialsState, error)
	EnsureCredentials(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreCredentialsEnsureState, error)
	EnsureElectrsCredentials(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreElectrsCredentialsState, error)
}

type LoopManager interface {
	Status(ctx context.Context) (LoopState, error)
	Ensure(ctx context.Context, params LoopEnsureParams, dryRun bool) (LoopState, error)
	Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (LoopState, error)
	Remove(ctx context.Context, dryRun bool) error
	EnsurePermissions(ctx context.Context, dryRun bool) error
	EnsureClientMaterial(ctx context.Context, dryRun bool) error
}

type ElementsManager interface {
	Status(ctx context.Context, dataDir string) (ElementsState, error)
	Config(ctx context.Context, dataDir string) (ElementsConfigState, error)
	Ensure(ctx context.Context, params ElementsEnsureParams, dryRun bool) (ElementsState, error)
	Lifecycle(ctx context.Context, dataDir string, action AppLifecycleAction, dryRun bool) (ElementsState, error)
	Remove(ctx context.Context, dataDir string, dryRun bool) error
}

type PeerSwapManager interface {
	Status(ctx context.Context) (PeerSwapState, error)
	Source(ctx context.Context) (PeerSwapSourceState, error)
	WriteSource(ctx context.Context, source PeerSwapSource, dryRun bool) error
	Ensure(ctx context.Context, params PeerSwapEnsureParams, dryRun bool) (PeerSwapState, error)
	Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (PeerSwapState, error)
	Remove(ctx context.Context, dryRun bool) error
}

type TapdManager interface {
	Status(ctx context.Context) (TapdState, error)
	Ensure(ctx context.Context, params TapdEnsureParams, dryRun bool) (TapdState, error)
	Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (TapdState, error)
	Remove(ctx context.Context, dryRun bool) error
	CLI(ctx context.Context, request appmanifest.TapdCLIRequest) (string, error)
	InterceptorConflict(ctx context.Context) (bool, error)
}

type PublicPoolManager interface {
	Status(ctx context.Context) (PublicPoolState, error)
	Ensure(ctx context.Context, params PublicPoolEnsureParams, dryRun bool) (PublicPoolState, error)
	Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (PublicPoolState, error)
	Remove(ctx context.Context, dryRun bool) error
	EnsureFirewall(ctx context.Context, dryRun bool) (PublicPoolState, error)
}

type BarkWalletManager interface {
	Status(ctx context.Context) (BarkWalletState, error)
	Ensure(ctx context.Context, dryRun bool) (BarkWalletState, error)
	Lifecycle(ctx context.Context, action AppLifecycleAction, dryRun bool) (BarkWalletState, error)
	Remove(ctx context.Context, dryRun bool) error
	EnsureFirewall(ctx context.Context, dryRun bool) (BarkWalletState, error)
	ReadPassword() (string, error)
	ResetPassword(dryRun bool) (BarkWalletState, error)
}

type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	Phase      string    `json:"phase"`
	Caller     string    `json:"caller"`
	RequestID  string    `json:"request_id,omitempty"`
	Operation  Operation `json:"operation"`
	DryRun     bool      `json:"dry_run,omitempty"`
	Success    bool      `json:"success"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
}

type Broker struct {
	Runner               CommandRunner
	Locker               Locker
	Audit                AuditSink
	Files                ConfigFileManager
	SystemIntegrations   SystemIntegrationsManager
	TerminalCredential   TerminalCredentialManager
	TerminalControl      TerminalControlManager
	ManagerFirewall      ManagerFirewallManager
	LNDUpgrade           LNDUpgradeManager
	TorUpgrade           TorUpgradeManager
	LightningOSUpgrade   LightningOSUpgradeManager
	Apps                 AppManager
	Packages             PackageManager
	AppStorage           AppStorageManager
	CatalogStorage       CatalogStorageManager
	SMART                SMARTManager
	LNDPermissions       LNDPermissionsManager
	LNDManagerCredential LNDManagerCredentialManager
	BitcoinStorage       BitcoinStorageManager
	BitcoinConfig        BitcoinConfigManager
	Loop                 LoopManager
	Elements             ElementsManager
	PeerSwap             PeerSwapManager
	Tapd                 TapdManager
	PublicPool           PublicPoolManager
	BarkWallet           BarkWalletManager
	Caller               string
	Timeout              time.Duration
	Now                  func() time.Time
}

func (broker *Broker) Handle(ctx context.Context, request Request) Response {
	startedAt := broker.now()
	if err := ValidateRequest(request); err != nil {
		broker.writeCompletionAudit(startedAt, request, false, "invalid_request")
		return ErrorResponse(request.RequestID, "invalid_request", err.Error())
	}
	if broker.Runner == nil || broker.Audit == nil {
		return ErrorResponse(request.RequestID, "broker_unavailable", "privileged broker is unavailable")
	}

	if err := broker.Audit.Write(AuditEvent{
		Timestamp: startedAt,
		Phase:     "start",
		Caller:    broker.Caller,
		RequestID: request.RequestID,
		Operation: request.Operation,
		DryRun:    request.DryRun,
	}); err != nil {
		return ErrorResponse(request.RequestID, "audit_unavailable", "privileged audit is unavailable")
	}

	timeout := broker.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 15 * time.Second
	}
	timeout = operationTimeout(timeout, request.Operation, request.DryRun)
	operationCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if mutatingOperation(request.Operation) && !request.DryRun {
		if broker.Locker == nil {
			broker.writeCompletionAudit(startedAt, request, false, "lock_unavailable")
			return ErrorResponse(request.RequestID, "lock_unavailable", "privileged operation lock is unavailable")
		}
		unlock, err := broker.Locker.Lock(operationCtx)
		if err != nil {
			broker.writeCompletionAudit(startedAt, request, false, "lock_timeout")
			return ErrorResponse(request.RequestID, "lock_timeout", "privileged operation lock timed out")
		}
		defer unlock()
	}

	result, code, err := broker.execute(operationCtx, request)
	if err != nil {
		if errors.Is(operationCtx.Err(), context.DeadlineExceeded) {
			code = "timeout"
			err = errors.New("privileged operation timed out")
		}
		broker.writeCompletionAudit(startedAt, request, false, code)
		return ErrorResponse(request.RequestID, code, err.Error())
	}
	if err := broker.writeCompletionAudit(startedAt, request, true, ""); err != nil {
		return ErrorResponse(request.RequestID, "audit_unavailable", "privileged audit completion failed")
	}
	return SuccessResponse(request, result)
}

// operationTimeout keeps the normal broker transport short while allowing
// bounded package and Compose mutations to outlive their external tools.
// A BTCPay upgrade can legitimately spend more than 30 seconds replacing its
// database container and waiting for PostgreSQL before the rest of the stack
// starts. Dry-run validation remains on the short configured timeout.
func operationTimeout(configured time.Duration, operation Operation, dryRun bool) time.Duration {
	if dryRun {
		return configured
	}
	switch operation {
	case OperationCatalogStorageEnsure:
		if configured < privilegedStorageMigrationTimeout {
			return privilegedStorageMigrationTimeout
		}
	case OperationTorMetadataRefresh:
		if configured < privilegedLongOperationTimeout {
			return privilegedLongOperationTimeout
		}
	case OperationAppLNDHostAccessEnsure:
		if configured < privilegedLongOperationTimeout {
			return privilegedLongOperationTimeout
		}
	case OperationTerminalControl:
		if configured < 15*time.Second {
			return 15 * time.Second
		}
	case OperationAppImageProbe:
		if configured < privilegedImageProbeTimeout {
			return privilegedImageProbeTimeout
		}
	case OperationSystemIntegrationsApply, OperationAppLifecycle, OperationAppRemove, OperationAppAdminReset, OperationBitcoinConsumerNetworkEnsure, OperationLoopEnsure, OperationLoopLifecycle, OperationLoopRemove, OperationElementsEnsure, OperationElementsLifecycle, OperationElementsRemove, OperationPeerSwapEnsure, OperationPeerSwapLifecycle, OperationPeerSwapRemove, OperationTapdEnsure, OperationTapdLifecycle, OperationTapdRemove, OperationTapdCLI, OperationPublicPoolEnsure, OperationPublicPoolLifecycle, OperationPublicPoolRemove, OperationBarkWalletEnsure, OperationBarkWalletLifecycle, OperationBarkWalletRemove:
		if configured < privilegedLongOperationTimeout {
			return privilegedLongOperationTimeout
		}
	case OperationBitcoinStatus:
		if configured < privilegedBitcoinStatusTimeout {
			return privilegedBitcoinStatusTimeout
		}
	}
	return configured
}

func (broker *Broker) execute(ctx context.Context, request Request) (any, string, error) {
	switch request.Operation {
	case OperationSelfTest:
		return map[string]any{
			"protocol_version": ProtocolVersion,
			"ready":            true,
		}, "", nil
	case OperationServiceStatus:
		var params ServiceStatusParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid service.status params")
		}
		output, runErr := broker.Runner.Run(ctx, systemctlPath, "is-active", params.Unit)
		status := normalizeServiceStatus(output)
		if status == "unknown" && runErr != nil {
			return nil, "execution_failed", errors.New("service status failed")
		}
		return map[string]string{"status": status}, "", nil
	case OperationServiceRestart:
		var params ServiceRestartParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid service.restart params")
		}
		if request.DryRun {
			return map[string]any{"validated": true}, "", nil
		}
		if params.Unit == "lightningos-manager" {
			transientUnit := "lightningos-manager-restart-" + request.RequestID
			args := []string{
				"--quiet",
				"--collect",
				"--unit=" + transientUnit,
				"--on-active=1s",
				systemctlPath,
				"restart",
				params.Unit,
			}
			if _, err := broker.Runner.Run(ctx, systemdRunPath, args...); err != nil {
				return nil, "execution_failed", errors.New("service restart failed")
			}
			return map[string]any{"scheduled": true}, "", nil
		}
		args := []string{"restart"}
		if params.NoBlock {
			args = append(args, "--no-block")
		}
		args = append(args, params.Unit)
		if _, err := broker.Runner.Run(ctx, systemctlPath, args...); err != nil {
			return nil, "execution_failed", errors.New("service restart failed")
		}
		return map[string]any{"started": true}, "", nil
	case OperationHostPower:
		var params HostPowerParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid host.power params")
		}
		if request.DryRun {
			return HostPowerState{Validated: true}, "", nil
		}
		transientUnit := "lightningos-host-" + params.Action + "-" + request.RequestID
		args := []string{
			"--quiet",
			"--collect",
			"--unit=" + transientUnit,
			"--on-active=2s",
			systemctlPath,
			params.Action,
		}
		if _, err := broker.Runner.Run(ctx, systemdRunPath, args...); err != nil {
			return nil, "execution_failed", errors.New("host power action failed")
		}
		return HostPowerState{Validated: true, Scheduled: true}, "", nil
	case OperationFilesEnableLogin:
		if broker.Files == nil {
			return nil, "broker_unavailable", errors.New("privileged config file manager is unavailable")
		}
		changed, err := broker.Files.EnableLogin(ctx, request.DryRun)
		if err != nil {
			return nil, "file_update_failed", errors.New("enable login config update failed")
		}
		return map[string]any{"validated": true, "changed": changed}, "", nil
	case OperationSystemIntegrationAssetInstall:
		if broker.SystemIntegrations == nil {
			return nil, "broker_unavailable", errors.New("system integrations manager is unavailable")
		}
		var params SystemIntegrationAssetInstallParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid files.system-integration.install params")
		}
		state, err := broker.SystemIntegrations.InstallAsset(ctx, params, request.DryRun)
		if err != nil {
			return nil, "system_integration_failed", errors.New("system integration asset installation failed")
		}
		return state, "", nil
	case OperationSystemIntegrationsStatus:
		if broker.SystemIntegrations == nil {
			return nil, "broker_unavailable", errors.New("system integrations manager is unavailable")
		}
		state, err := broker.SystemIntegrations.Status(ctx)
		if err != nil {
			return nil, "system_integration_failed", errors.New("system integrations status failed")
		}
		return state, "", nil
	case OperationSystemIntegrationsApply:
		if broker.SystemIntegrations == nil {
			return nil, "broker_unavailable", errors.New("system integrations manager is unavailable")
		}
		state, err := broker.SystemIntegrations.Apply(ctx, request.DryRun)
		if err != nil {
			return nil, "system_integration_failed", errors.New("system integrations reconciliation failed")
		}
		return state, "", nil
	case OperationSystemIntegrationsFinalize:
		if broker.SystemIntegrations == nil {
			return nil, "broker_unavailable", errors.New("system integrations manager is unavailable")
		}
		state, err := broker.SystemIntegrations.Finalize(ctx, request.DryRun)
		if err != nil {
			return nil, "system_integration_failed", errors.New("system integrations finalization failed")
		}
		return state, "", nil
	case OperationTerminalCredentialRotate:
		if broker.TerminalCredential == nil {
			return nil, "broker_unavailable", errors.New("terminal credential manager is unavailable")
		}
		var params TerminalCredentialRotateParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid terminal.credential.rotate params")
		}
		state, err := broker.TerminalCredential.Rotate(ctx, params, request.DryRun)
		if err != nil {
			return nil, "terminal_credential_failed", errors.New("terminal credential rotation failed")
		}
		return state, "", nil
	case OperationTerminalControl:
		if broker.TerminalControl == nil {
			return nil, "broker_unavailable", errors.New("terminal control manager is unavailable")
		}
		var params TerminalControlParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid terminal.control params")
		}
		state, err := broker.TerminalControl.SetEnabled(ctx, params, request.DryRun)
		if err != nil {
			return nil, "terminal_control_failed", errors.New("terminal control failed")
		}
		return state, "", nil
	case OperationManagerFirewallStatus:
		if broker.ManagerFirewall == nil {
			return nil, "broker_unavailable", errors.New("manager firewall inspector is unavailable")
		}
		state, err := broker.ManagerFirewall.Status(ctx)
		if err != nil {
			return nil, "firewall_status_failed", errors.New("manager firewall status failed")
		}
		return state, "", nil
	case OperationLNDUpgradeStart:
		if broker.LNDUpgrade == nil {
			return nil, "broker_unavailable", errors.New("privileged LND upgrade manager is unavailable")
		}
		var params LNDUpgradeStartParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid upgrade.lnd.start params")
		}
		state, err := broker.LNDUpgrade.Start(ctx, params, request.DryRun)
		if err != nil {
			return nil, "lnd_upgrade_failed", errors.New("LND upgrade start failed")
		}
		return state, "", nil
	case OperationTorMetadataRefresh:
		if broker.TorUpgrade == nil {
			return nil, "broker_unavailable", errors.New("privileged Tor upgrade manager is unavailable")
		}
		state, err := broker.TorUpgrade.Refresh(ctx, request.DryRun)
		if err != nil {
			return nil, "tor_refresh_failed", errors.New("Tor package metadata refresh failed")
		}
		return state, "", nil
	case OperationTorUpgradeStart:
		if broker.TorUpgrade == nil {
			return nil, "broker_unavailable", errors.New("privileged Tor upgrade manager is unavailable")
		}
		var params TorUpgradeStartParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid upgrade.tor.start params")
		}
		state, err := broker.TorUpgrade.Start(ctx, params, request.DryRun)
		if err != nil {
			return nil, "tor_upgrade_failed", errors.New("Tor upgrade start failed")
		}
		return state, "", nil
	case OperationLightningOSUpgradeStart:
		if broker.LightningOSUpgrade == nil {
			return nil, "broker_unavailable", errors.New("privileged LightningOS upgrade manager is unavailable")
		}
		var params LightningOSUpgradeStartParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid upgrade.lightningos.start params")
		}
		state, err := broker.LightningOSUpgrade.Start(ctx, params, request.DryRun)
		if err != nil {
			return nil, "lightningos_upgrade_failed", errors.New("LightningOS upgrade start failed")
		}
		return state, "", nil
	case OperationDockerEnsure:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		state, err := broker.Apps.EnsureDockerRuntime(ctx, request.DryRun)
		if err != nil {
			return nil, "docker_runtime_unavailable", errors.New("docker runtime is unavailable")
		}
		return state, "", nil
	case OperationDockerStatus:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		state, err := broker.Apps.DockerRuntimeStatus(ctx)
		if err != nil {
			return nil, "docker_runtime_status_failed", errors.New("docker runtime status failed")
		}
		return state, "", nil
	case OperationPackageEnsure:
		if broker.Packages == nil {
			return nil, "broker_unavailable", errors.New("privileged package manager is unavailable")
		}
		var params PackageFeatureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid packages.feature.ensure params")
		}
		state, err := broker.Packages.EnsureFeature(ctx, params.Feature, request.DryRun)
		if err != nil {
			return nil, "package_feature_failed", errors.New("package feature preparation failed")
		}
		return state, "", nil
	case OperationPackageStatus:
		if broker.Packages == nil {
			return nil, "broker_unavailable", errors.New("privileged package manager is unavailable")
		}
		var params PackageFeatureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid packages.feature.status params")
		}
		state, err := broker.Packages.FeatureStatus(ctx, params.Feature)
		if err != nil {
			return nil, "package_feature_status_failed", errors.New("package feature status failed")
		}
		return state, "", nil
	case OperationAppStorageEnsure:
		if broker.AppStorage == nil {
			return nil, "broker_unavailable", errors.New("privileged app storage manager is unavailable")
		}
		state, err := broker.AppStorage.Ensure(ctx, request.DryRun)
		if err != nil {
			return nil, "app_storage_failed", errors.New("app storage preparation failed")
		}
		return state, "", nil
	case OperationCatalogStorageEnsure:
		if broker.CatalogStorage == nil {
			return nil, "broker_unavailable", errors.New("catalog storage manager is unavailable")
		}
		var params CatalogStorageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid catalog storage params")
		}
		state, err := broker.CatalogStorage.Ensure(ctx, params.AppID, params.DataDir, params.Finalize, request.DryRun)
		if err != nil {
			return nil, "catalog_storage_failed", errors.New("catalog storage enrollment failed")
		}
		return state, "", nil
	case OperationSMARTRead:
		if broker.SMART == nil {
			return nil, "broker_unavailable", errors.New("privileged SMART manager is unavailable")
		}
		var params SMARTReadParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid storage.smart.read params")
		}
		state, err := broker.SMART.Read(ctx, params.Device)
		if err != nil {
			return nil, "smart_read_failed", errors.New("SMART read failed")
		}
		return state, "", nil
	case OperationLNDPermissionsRepair:
		if broker.LNDPermissions == nil {
			return nil, "broker_unavailable", errors.New("privileged LND permissions manager is unavailable")
		}
		state, err := broker.LNDPermissions.Repair(ctx, request.DryRun)
		if err != nil {
			return nil, "lnd_permissions_failed", errors.New("LND permissions repair failed")
		}
		return state, "", nil
	case OperationLNDManagerCredentialEnsure, OperationLNDManagerCredentialRollback:
		if broker.LNDManagerCredential == nil {
			return nil, "broker_unavailable", errors.New("privileged LND manager credential service is unavailable")
		}
		var state LNDManagerCredentialState
		var err error
		if request.Operation == OperationLNDManagerCredentialEnsure {
			state, err = broker.LNDManagerCredential.Ensure(ctx, request.DryRun)
		} else {
			state, err = broker.LNDManagerCredential.Rollback(ctx, request.DryRun)
		}
		if err != nil {
			return nil, "lnd_manager_credential_failed", errors.New("LND manager credential operation failed")
		}
		return state, "", nil
	case OperationAppLifecycle:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.compose.lifecycle params")
		}
		if err := broker.Apps.Lifecycle(ctx, params.AppID, params.Action, request.DryRun); err != nil {
			return nil, "app_lifecycle_failed", errors.New("app lifecycle operation failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationAppSnapshot:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppSnapshotParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.compose.snapshot params")
		}
		if err := broker.Apps.Snapshot(ctx, params.AppID, request.DryRun); err != nil {
			return nil, "app_snapshot_failed", errors.New("app snapshot operation failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationAppInspect:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppInspectParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.compose.inspect params")
		}
		inspection, err := broker.Apps.Inspect(ctx, params.AppID)
		if err != nil {
			return nil, "app_inspection_failed", errors.New("app inspection failed")
		}
		return inspection, "", nil
	case OperationAppLogs:
		logManager, ok := broker.Apps.(AppLogManager)
		if !ok {
			return nil, "broker_unavailable", errors.New("privileged app log manager is unavailable")
		}
		var params AppLogsParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.compose.logs params")
		}
		state, err := logManager.Logs(ctx, params.AppID, params.Lines, params.Since)
		if err != nil {
			return nil, "app_logs_failed", errors.New("app log read failed")
		}
		return state, "", nil
	case OperationAppRemove:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppRemoveParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.compose.remove params")
		}
		if err := broker.Apps.Remove(ctx, params.AppID, request.DryRun); err != nil {
			return nil, "app_remove_failed", errors.New("app remove operation failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationAppAdminReset:
		adminManager, ok := broker.Apps.(AppAdminManager)
		if !ok {
			return nil, "broker_unavailable", errors.New("privileged app admin manager is unavailable")
		}
		var params AppAdminResetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.admin.reset params")
		}
		if err := adminManager.ResetAdmin(ctx, params.AppID, request.DryRun); err != nil {
			return nil, "app_admin_reset_failed", errors.New("app admin reset failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationAppImagePrepare:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppImageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.image.prepare params")
		}
		state, err := broker.Apps.PrepareImage(ctx, params.AppID, params.Variant, request.DryRun)
		if err != nil {
			return nil, "app_image_prepare_failed", errors.New("app image preparation failed")
		}
		return state, "", nil
	case OperationAppImageStatus:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppImageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.image.status params")
		}
		state, err := broker.Apps.ImageStatus(ctx, params.AppID, params.Variant)
		if err != nil {
			return nil, "app_image_status_failed", errors.New("app image status failed")
		}
		return state, "", nil
	case OperationAppImageProbe:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppImageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.image.probe params")
		}
		probe, err := broker.Apps.ProbeImage(ctx, params.AppID, params.Variant, request.DryRun)
		if err != nil {
			return nil, "app_image_probe_failed", errors.New("app image probe failed")
		}
		return probe, "", nil
	case OperationAppFirewallEnsure:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("privileged app manager is unavailable")
		}
		var params AppFirewallParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.firewall.ensure params")
		}
		state, err := broker.Apps.EnsureFirewallAccess(ctx, params.AppID, request.DryRun)
		if err != nil {
			return nil, "app_firewall_failed", errors.New("app firewall operation failed")
		}
		return state, "", nil
	case OperationAppLNDHostAccessEnsure:
		manager, ok := broker.Apps.(AppLNDHostAccessManager)
		if !ok {
			return nil, "broker_unavailable", errors.New("privileged app LND host access manager is unavailable")
		}
		var params AppLNDHostAccessParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.lnd-host-access.ensure params")
		}
		if err := manager.EnsureLNDHostAccess(ctx, params.AppID, request.DryRun); err != nil {
			return nil, "app_lnd_host_access_failed", errors.New("app LND host access operation failed")
		}
		return map[string]any{"ready": true, "changed": !request.DryRun}, "", nil
	case OperationBitcoinStorageEnsure:
		if broker.BitcoinStorage == nil {
			return nil, "broker_unavailable", errors.New("bitcoin storage manager is unavailable")
		}
		var params BitcoinCoreStorageParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.bitcoincore.storage.ensure params")
		}
		state, err := broker.BitcoinStorage.Ensure(ctx, params.DataDir, request.DryRun)
		if err != nil {
			return nil, "bitcoin_storage_failed", errors.New("bitcoin storage enrollment failed")
		}
		return state, "", nil
	case OperationBitcoinConfigEnsure, OperationBitcoinConfigWrite:
		if broker.BitcoinConfig == nil {
			return nil, "broker_unavailable", errors.New("bitcoin config manager is unavailable")
		}
		var params BitcoinCoreConfigWriteParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid bitcoin config params")
		}
		var state BitcoinCoreConfigState
		var err error
		if request.Operation == OperationBitcoinConfigEnsure {
			state, err = broker.BitcoinConfig.Ensure(ctx, params.DataDir, params.Content, params.GenerateRPCAuth, request.DryRun)
		} else {
			state, err = broker.BitcoinConfig.Write(ctx, params.DataDir, params.Content, request.DryRun)
		}
		if err != nil {
			return nil, "bitcoin_config_failed", errors.New("bitcoin config update failed")
		}
		return state, "", nil
	case OperationBitcoinCredentialsRead:
		if broker.BitcoinConfig == nil {
			return nil, "broker_unavailable", errors.New("bitcoin config manager is unavailable")
		}
		var params BitcoinCoreConfigTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid bitcoin credentials params")
		}
		state, err := broker.BitcoinConfig.Credentials(ctx, params.DataDir)
		if err != nil {
			return nil, "bitcoin_credentials_failed", errors.New("bitcoin credentials read failed")
		}
		return state, "", nil
	case OperationBitcoinCredentialsEnsure:
		if broker.BitcoinConfig == nil {
			return nil, "broker_unavailable", errors.New("bitcoin config manager is unavailable")
		}
		var params BitcoinCoreConfigTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid bitcoin credentials params")
		}
		state, err := broker.BitcoinConfig.EnsureCredentials(ctx, params.DataDir, request.DryRun)
		if err != nil {
			return nil, "bitcoin_credentials_failed", errors.New("bitcoin credentials ensure failed")
		}
		return state, "", nil
	case OperationBitcoinElectrsCredentialsEnsure:
		if broker.BitcoinConfig == nil {
			return nil, "broker_unavailable", errors.New("bitcoin config manager is unavailable")
		}
		var params BitcoinCoreConfigTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid Electrs bitcoin credentials params")
		}
		state, err := broker.BitcoinConfig.EnsureElectrsCredentials(ctx, params.DataDir, request.DryRun)
		if err != nil {
			return nil, "bitcoin_electrs_credentials_failed", errors.New("Electrs bitcoin credentials ensure failed")
		}
		return state, "", nil
	case OperationBitcoinConfigRead:
		if broker.BitcoinConfig == nil {
			return nil, "broker_unavailable", errors.New("bitcoin config manager is unavailable")
		}
		var params BitcoinCoreConfigTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid bitcoin config params")
		}
		state, err := broker.BitcoinConfig.Read(ctx, params.DataDir)
		if err != nil {
			return nil, "bitcoin_config_failed", errors.New("bitcoin config read failed")
		}
		return state, "", nil
	case OperationBitcoinStatus:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("app manager is unavailable")
		}
		state, err := broker.Apps.BitcoinCoreStatus(ctx)
		if err != nil {
			return nil, "bitcoin_status_failed", errors.New("bitcoin core status failed")
		}
		return state, "", nil
	case OperationBitcoinConsumerNetworkEnsure:
		if broker.Apps == nil {
			return nil, "broker_unavailable", errors.New("app manager is unavailable")
		}
		state, err := broker.Apps.EnsureBitcoinConsumerNetwork(ctx, request.DryRun)
		if err != nil {
			return nil, "bitcoin_consumer_network_failed", errors.New("bitcoin consumer network ensure failed")
		}
		return state, "", nil
	case OperationLoopStatus:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		state, err := broker.Loop.Status(ctx)
		if err != nil {
			return nil, "loop_status_failed", errors.New("Lightning Loop status failed")
		}
		return state, "", nil
	case OperationLoopEnsure:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		var params LoopEnsureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.loop.ensure params")
		}
		state, err := broker.Loop.Ensure(ctx, params, request.DryRun)
		if err != nil {
			return nil, "loop_ensure_failed", errors.New("Lightning Loop preparation failed")
		}
		return state, "", nil
	case OperationLoopLifecycle:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		var params LoopLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.loop.lifecycle params")
		}
		state, err := broker.Loop.Lifecycle(ctx, params.Action, request.DryRun)
		if err != nil {
			return nil, "loop_lifecycle_failed", errors.New("Lightning Loop lifecycle failed")
		}
		return state, "", nil
	case OperationLoopRemove:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		if err := broker.Loop.Remove(ctx, request.DryRun); err != nil {
			return nil, "loop_remove_failed", errors.New("Lightning Loop removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationLoopPermissionsEnsure:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		if err := broker.Loop.EnsurePermissions(ctx, request.DryRun); err != nil {
			return nil, "loop_permissions_failed", errors.New("Lightning Loop permissions repair failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationLoopClientMaterialEnsure:
		if broker.Loop == nil {
			return nil, "broker_unavailable", errors.New("Lightning Loop manager is unavailable")
		}
		if err := broker.Loop.EnsureClientMaterial(ctx, request.DryRun); err != nil {
			return nil, "loop_client_material_failed", errors.New("Lightning Loop client material preparation failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationElementsStatus:
		if broker.Elements == nil {
			return nil, "broker_unavailable", errors.New("Elements manager is unavailable")
		}
		var params ElementsTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.elements.status params")
		}
		state, err := broker.Elements.Status(ctx, params.DataDir)
		if err != nil {
			return nil, "elements_status_failed", errors.New("Elements status failed")
		}
		return state, "", nil
	case OperationElementsConfigRead:
		if broker.Elements == nil {
			return nil, "broker_unavailable", errors.New("Elements manager is unavailable")
		}
		var params ElementsTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.elements.config.read params")
		}
		state, err := broker.Elements.Config(ctx, params.DataDir)
		if err != nil {
			return nil, "elements_config_failed", errors.New("Elements configuration read failed")
		}
		return state, "", nil
	case OperationElementsEnsure:
		if broker.Elements == nil {
			return nil, "broker_unavailable", errors.New("Elements manager is unavailable")
		}
		var params ElementsEnsureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.elements.ensure params")
		}
		state, err := broker.Elements.Ensure(ctx, params, request.DryRun)
		if err != nil {
			return nil, "elements_ensure_failed", errors.New("Elements preparation failed")
		}
		return state, "", nil
	case OperationElementsLifecycle:
		if broker.Elements == nil {
			return nil, "broker_unavailable", errors.New("Elements manager is unavailable")
		}
		var params ElementsLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.elements.lifecycle params")
		}
		state, err := broker.Elements.Lifecycle(ctx, params.DataDir, params.Action, request.DryRun)
		if err != nil {
			return nil, "elements_lifecycle_failed", errors.New("Elements lifecycle failed")
		}
		return state, "", nil
	case OperationElementsRemove:
		if broker.Elements == nil {
			return nil, "broker_unavailable", errors.New("Elements manager is unavailable")
		}
		var params ElementsTargetParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.elements.remove params")
		}
		if err := broker.Elements.Remove(ctx, params.DataDir, request.DryRun); err != nil {
			return nil, "elements_remove_failed", errors.New("Elements removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationPeerSwapStatus:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		state, err := broker.PeerSwap.Status(ctx)
		if err != nil {
			return nil, "peerswap_status_failed", errors.New("PeerSwap status failed")
		}
		return state, "", nil
	case OperationPeerSwapSourceRead:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		state, err := broker.PeerSwap.Source(ctx)
		if err != nil {
			return nil, "peerswap_source_failed", errors.New("PeerSwap Elements source read failed")
		}
		return state, "", nil
	case OperationPeerSwapSourceWrite:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		var params PeerSwapSource
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.peerswap.source.write params")
		}
		if err := broker.PeerSwap.WriteSource(ctx, params, request.DryRun); err != nil {
			return nil, "peerswap_source_failed", errors.New("PeerSwap Elements source write failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationPeerSwapEnsure:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		var params PeerSwapEnsureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.peerswap.ensure params")
		}
		state, err := broker.PeerSwap.Ensure(ctx, params, request.DryRun)
		if err != nil {
			return nil, "peerswap_ensure_failed", errors.New("PeerSwap preparation failed")
		}
		return state, "", nil
	case OperationPeerSwapLifecycle:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		var params PeerSwapLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.peerswap.lifecycle params")
		}
		state, err := broker.PeerSwap.Lifecycle(ctx, params.Action, request.DryRun)
		if err != nil {
			return nil, "peerswap_lifecycle_failed", errors.New("PeerSwap lifecycle failed")
		}
		return state, "", nil
	case OperationPeerSwapRemove:
		if broker.PeerSwap == nil {
			return nil, "broker_unavailable", errors.New("PeerSwap manager is unavailable")
		}
		if err := broker.PeerSwap.Remove(ctx, request.DryRun); err != nil {
			return nil, "peerswap_remove_failed", errors.New("PeerSwap removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationTapdStatus:
		if broker.Tapd == nil {
			return nil, "broker_unavailable", errors.New("Tapd manager is unavailable")
		}
		state, err := broker.Tapd.Status(ctx)
		if err != nil {
			return nil, "tapd_status_failed", errors.New("Tapd status failed")
		}
		return state, "", nil
	case OperationTapdEnsure:
		if broker.Tapd == nil {
			return nil, "broker_unavailable", errors.New("Tapd manager is unavailable")
		}
		var params TapdEnsureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.tapd.ensure params")
		}
		state, err := broker.Tapd.Ensure(ctx, params, request.DryRun)
		if err != nil {
			return nil, "tapd_ensure_failed", errors.New("Tapd preparation failed")
		}
		return state, "", nil
	case OperationTapdLifecycle:
		if broker.Tapd == nil {
			return nil, "broker_unavailable", errors.New("Tapd manager is unavailable")
		}
		var params TapdLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.tapd.lifecycle params")
		}
		state, err := broker.Tapd.Lifecycle(ctx, params.Action, request.DryRun)
		if err != nil {
			return nil, "tapd_lifecycle_failed", errors.New("Tapd lifecycle failed")
		}
		return state, "", nil
	case OperationTapdRemove:
		if broker.Tapd == nil {
			return nil, "broker_unavailable", errors.New("Tapd manager is unavailable")
		}
		if err := broker.Tapd.Remove(ctx, request.DryRun); err != nil {
			return nil, "tapd_remove_failed", errors.New("Tapd removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationTapdCLI:
		if broker.Tapd == nil {
			return nil, "broker_unavailable", errors.New("Tapd manager is unavailable")
		}
		var params appmanifest.TapdCLIRequest
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.tapd.cli params")
		}
		output, err := broker.Tapd.CLI(ctx, params)
		if err != nil {
			return nil, "tapd_cli_failed", errors.New("Tapd command failed")
		}
		return TapdCLIResult{Output: output}, "", nil
	case OperationPublicPoolStatus:
		if broker.PublicPool == nil {
			return nil, "broker_unavailable", errors.New("Public Pool manager is unavailable")
		}
		state, err := broker.PublicPool.Status(ctx)
		if err != nil {
			return nil, "publicpool_status_failed", errors.New("Public Pool status failed")
		}
		return state, "", nil
	case OperationPublicPoolEnsure:
		if broker.PublicPool == nil {
			return nil, "broker_unavailable", errors.New("Public Pool manager is unavailable")
		}
		var params PublicPoolEnsureParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.publicpool.ensure params")
		}
		state, err := broker.PublicPool.Ensure(ctx, params, request.DryRun)
		if err != nil {
			return nil, "publicpool_ensure_failed", errors.New("Public Pool preparation failed")
		}
		return state, "", nil
	case OperationPublicPoolLifecycle:
		if broker.PublicPool == nil {
			return nil, "broker_unavailable", errors.New("Public Pool manager is unavailable")
		}
		var params PublicPoolLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.publicpool.lifecycle params")
		}
		state, err := broker.PublicPool.Lifecycle(ctx, params.Action, request.DryRun)
		if err != nil {
			return nil, "publicpool_lifecycle_failed", errors.New("Public Pool lifecycle failed")
		}
		return state, "", nil
	case OperationPublicPoolRemove:
		if broker.PublicPool == nil {
			return nil, "broker_unavailable", errors.New("Public Pool manager is unavailable")
		}
		if err := broker.PublicPool.Remove(ctx, request.DryRun); err != nil {
			return nil, "publicpool_remove_failed", errors.New("Public Pool removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationPublicPoolFirewall:
		if broker.PublicPool == nil {
			return nil, "broker_unavailable", errors.New("Public Pool manager is unavailable")
		}
		state, err := broker.PublicPool.EnsureFirewall(ctx, request.DryRun)
		if err != nil {
			return nil, "publicpool_firewall_failed", errors.New("Public Pool firewall preparation failed")
		}
		return state, "", nil
	case OperationBarkWalletStatus:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		state, err := broker.BarkWallet.Status(ctx)
		if err != nil {
			return nil, "bark_status_failed", errors.New("Bark Wallet status failed")
		}
		return state, "", nil
	case OperationBarkWalletEnsure:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		state, err := broker.BarkWallet.Ensure(ctx, request.DryRun)
		if err != nil {
			return nil, "bark_ensure_failed", errors.New("Bark Wallet preparation failed")
		}
		return state, "", nil
	case OperationBarkWalletLifecycle:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		var params BarkWalletLifecycleParams
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return nil, "invalid_request", errors.New("invalid app.bark.lifecycle params")
		}
		state, err := broker.BarkWallet.Lifecycle(ctx, params.Action, request.DryRun)
		if err != nil {
			return nil, "bark_lifecycle_failed", errors.New("Bark Wallet lifecycle failed")
		}
		return state, "", nil
	case OperationBarkWalletRemove:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		if err := broker.BarkWallet.Remove(ctx, request.DryRun); err != nil {
			return nil, "bark_remove_failed", errors.New("Bark Wallet removal failed")
		}
		return map[string]any{"validated": true, "changed": !request.DryRun}, "", nil
	case OperationBarkWalletFirewall:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		state, err := broker.BarkWallet.EnsureFirewall(ctx, request.DryRun)
		if err != nil {
			return nil, "bark_firewall_failed", errors.New("Bark Wallet firewall preparation failed")
		}
		return state, "", nil
	case OperationBarkWalletPasswordRead:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		password, err := broker.BarkWallet.ReadPassword()
		if err != nil {
			return nil, "bark_password_unavailable", errors.New("Bark Wallet password is unavailable")
		}
		return BarkWalletPasswordResult{Password: password}, "", nil
	case OperationBarkWalletPasswordReset:
		if broker.BarkWallet == nil {
			return nil, "broker_unavailable", errors.New("Bark Wallet manager is unavailable")
		}
		state, err := broker.BarkWallet.ResetPassword(request.DryRun)
		if err != nil {
			return nil, "bark_password_reset_failed", errors.New("Bark Wallet password reset failed")
		}
		return state, "", nil
	default:
		return nil, "unknown_operation", errors.New("unknown operation")
	}
}

func (broker *Broker) writeCompletionAudit(startedAt time.Time, request Request, success bool, errorCode string) error {
	if broker.Audit == nil {
		return errors.New("audit unavailable")
	}
	operation := request.Operation
	if !knownOperation(operation) {
		operation = "request.invalid"
	}
	completedAt := broker.now()
	return broker.Audit.Write(AuditEvent{
		Timestamp:  completedAt,
		Phase:      "complete",
		Caller:     broker.Caller,
		RequestID:  request.RequestID,
		Operation:  operation,
		DryRun:     request.DryRun,
		Success:    success,
		DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		ErrorCode:  errorCode,
	})
}

func (broker *Broker) now() time.Time {
	if broker.Now != nil {
		return broker.Now().UTC()
	}
	return time.Now().UTC()
}

func knownOperation(operation Operation) bool {
	switch operation {
	case OperationAppLNDHostAccessEnsure:
		return true
	case OperationTerminalControl:
		return true
	case OperationCatalogStorageEnsure:
		return true
	case OperationSelfTest, OperationServiceStatus, OperationServiceRestart, OperationHostPower, OperationFilesEnableLogin, OperationSystemIntegrationAssetInstall, OperationSystemIntegrationsStatus, OperationSystemIntegrationsApply, OperationSystemIntegrationsFinalize, OperationTerminalCredentialRotate, OperationManagerFirewallStatus, OperationLNDUpgradeStart, OperationTorMetadataRefresh, OperationTorUpgradeStart, OperationLightningOSUpgradeStart, OperationAppLifecycle, OperationAppSnapshot, OperationAppInspect, OperationAppLogs, OperationAppRemove, OperationAppAdminReset, OperationDockerEnsure, OperationDockerStatus, OperationPackageEnsure, OperationPackageStatus, OperationAppStorageEnsure, OperationSMARTRead, OperationLNDPermissionsRepair, OperationLNDManagerCredentialEnsure, OperationLNDManagerCredentialRollback, OperationAppImagePrepare, OperationAppImageStatus, OperationAppImageProbe, OperationAppFirewallEnsure, OperationBitcoinStorageEnsure, OperationBitcoinConfigEnsure, OperationBitcoinConfigRead, OperationBitcoinConfigWrite, OperationBitcoinCredentialsRead, OperationBitcoinCredentialsEnsure, OperationBitcoinElectrsCredentialsEnsure, OperationBitcoinStatus, OperationBitcoinConsumerNetworkEnsure, OperationLoopStatus, OperationLoopEnsure, OperationLoopLifecycle, OperationLoopRemove, OperationLoopPermissionsEnsure, OperationLoopClientMaterialEnsure, OperationElementsStatus, OperationElementsConfigRead, OperationElementsEnsure, OperationElementsLifecycle, OperationElementsRemove, OperationPeerSwapStatus, OperationPeerSwapSourceRead, OperationPeerSwapSourceWrite, OperationPeerSwapEnsure, OperationPeerSwapLifecycle, OperationPeerSwapRemove, OperationTapdStatus, OperationTapdEnsure, OperationTapdLifecycle, OperationTapdRemove, OperationTapdCLI, OperationPublicPoolStatus, OperationPublicPoolEnsure, OperationPublicPoolLifecycle, OperationPublicPoolRemove, OperationPublicPoolFirewall, OperationBarkWalletStatus, OperationBarkWalletEnsure, OperationBarkWalletLifecycle, OperationBarkWalletRemove, OperationBarkWalletFirewall, OperationBarkWalletPasswordRead, OperationBarkWalletPasswordReset:
		return true
	default:
		return false
	}
}

func mutatingOperation(operation Operation) bool {
	switch operation {
	case OperationAppLNDHostAccessEnsure:
		return true
	case OperationTerminalControl:
		return true
	case OperationCatalogStorageEnsure:
		return true
	case OperationServiceRestart, OperationHostPower, OperationFilesEnableLogin, OperationSystemIntegrationAssetInstall, OperationSystemIntegrationsApply, OperationSystemIntegrationsFinalize, OperationTerminalCredentialRotate, OperationLNDUpgradeStart, OperationTorMetadataRefresh, OperationTorUpgradeStart, OperationLightningOSUpgradeStart, OperationAppLifecycle, OperationAppSnapshot, OperationAppRemove, OperationAppAdminReset, OperationDockerEnsure, OperationPackageEnsure, OperationAppStorageEnsure, OperationLNDPermissionsRepair, OperationLNDManagerCredentialEnsure, OperationLNDManagerCredentialRollback, OperationAppImagePrepare, OperationAppImageProbe, OperationAppFirewallEnsure, OperationBitcoinStorageEnsure, OperationBitcoinConfigEnsure, OperationBitcoinConfigWrite, OperationBitcoinElectrsCredentialsEnsure, OperationBitcoinConsumerNetworkEnsure, OperationLoopEnsure, OperationLoopLifecycle, OperationLoopRemove, OperationLoopPermissionsEnsure, OperationLoopClientMaterialEnsure, OperationElementsEnsure, OperationElementsLifecycle, OperationElementsRemove, OperationPeerSwapSourceWrite, OperationPeerSwapEnsure, OperationPeerSwapLifecycle, OperationPeerSwapRemove, OperationTapdEnsure, OperationTapdLifecycle, OperationTapdRemove, OperationTapdCLI, OperationPublicPoolEnsure, OperationPublicPoolLifecycle, OperationPublicPoolRemove, OperationBarkWalletEnsure, OperationBarkWalletLifecycle, OperationBarkWalletRemove, OperationBarkWalletFirewall, OperationBarkWalletPasswordReset:
		return true
	default:
		return false
	}
}

func normalizeServiceStatus(output string) string {
	status := strings.ToLower(strings.TrimSpace(output))
	switch status {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading", "maintenance":
		return status
	default:
		return "unknown"
	}
}

func EncodeResponse(response Response) ([]byte, error) {
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	if len(data) > MaxMessageBytes {
		return nil, errors.New("response too large")
	}
	return append(data, '\n'), nil
}
