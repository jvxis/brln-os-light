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
	systemctlPath  = "/usr/bin/systemctl"
	systemdRunPath = "/usr/bin/systemd-run"
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

type AppManager interface {
	EnsureDockerRuntime(ctx context.Context, dryRun bool) (DockerRuntimeState, error)
	DockerRuntimeStatus(ctx context.Context) (DockerRuntimeState, error)
	Lifecycle(ctx context.Context, appID string, action AppLifecycleAction, dryRun bool) error
	Inspect(ctx context.Context, appID string) (AppInspection, error)
	Remove(ctx context.Context, appID string, dryRun bool) error
	PrepareImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageState, error)
	ImageStatus(ctx context.Context, appID string, variant appmanifest.AppImageVariant) (AppImageState, error)
	ProbeImage(ctx context.Context, appID string, variant appmanifest.AppImageVariant, dryRun bool) (AppImageProbe, error)
	EnsureFirewallAccess(ctx context.Context, appID string, dryRun bool) (AppFirewallState, error)
}

type PackageManager interface {
	EnsureFeature(ctx context.Context, feature PackageFeature, dryRun bool) (PackageFeatureState, error)
	FeatureStatus(ctx context.Context, feature PackageFeature) (PackageFeatureState, error)
}

type BitcoinStorageManager interface {
	Ensure(ctx context.Context, dataDir string, dryRun bool) (BitcoinCoreStorageState, error)
}

type BitcoinConfigManager interface {
	Ensure(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error)
	Read(ctx context.Context, dataDir string) (BitcoinCoreConfigState, error)
	Write(ctx context.Context, dataDir string, content string, dryRun bool) (BitcoinCoreConfigState, error)
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
	Runner         CommandRunner
	Locker         Locker
	Audit          AuditSink
	Files          ConfigFileManager
	Apps           AppManager
	Packages       PackageManager
	BitcoinStorage BitcoinStorageManager
	BitcoinConfig  BitcoinConfigManager
	Caller         string
	Timeout        time.Duration
	Now            func() time.Time
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
	case OperationFilesEnableLogin:
		if broker.Files == nil {
			return nil, "broker_unavailable", errors.New("privileged config file manager is unavailable")
		}
		changed, err := broker.Files.EnableLogin(ctx, request.DryRun)
		if err != nil {
			return nil, "file_update_failed", errors.New("enable login config update failed")
		}
		return map[string]any{"validated": true, "changed": changed}, "", nil
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
			state, err = broker.BitcoinConfig.Ensure(ctx, params.DataDir, params.Content, request.DryRun)
		} else {
			state, err = broker.BitcoinConfig.Write(ctx, params.DataDir, params.Content, request.DryRun)
		}
		if err != nil {
			return nil, "bitcoin_config_failed", errors.New("bitcoin config update failed")
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
	case OperationSelfTest, OperationServiceStatus, OperationServiceRestart, OperationFilesEnableLogin, OperationAppLifecycle, OperationAppInspect, OperationAppRemove, OperationDockerEnsure, OperationDockerStatus, OperationPackageEnsure, OperationPackageStatus, OperationAppImagePrepare, OperationAppImageStatus, OperationAppImageProbe, OperationAppFirewallEnsure, OperationBitcoinStorageEnsure, OperationBitcoinConfigEnsure, OperationBitcoinConfigRead, OperationBitcoinConfigWrite:
		return true
	default:
		return false
	}
}

func mutatingOperation(operation Operation) bool {
	switch operation {
	case OperationServiceRestart, OperationFilesEnableLogin, OperationAppLifecycle, OperationAppRemove, OperationDockerEnsure, OperationPackageEnsure, OperationAppImagePrepare, OperationAppImageProbe, OperationAppFirewallEnsure, OperationBitcoinStorageEnsure, OperationBitcoinConfigEnsure, OperationBitcoinConfigWrite:
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
