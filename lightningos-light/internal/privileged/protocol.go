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
	OperationSelfTest          Operation = "self_test"
	OperationServiceStatus     Operation = "service.status"
	OperationServiceRestart    Operation = "service.restart"
	OperationFilesEnableLogin  Operation = "files.enable_login"
	OperationAppLifecycle      Operation = "app.compose.lifecycle"
	OperationAppInspect        Operation = "app.compose.inspect"
	OperationAppRemove         Operation = "app.compose.remove"
	OperationDockerEnsure      Operation = "docker.runtime.ensure"
	OperationDockerStatus      Operation = "docker.runtime.status"
	OperationPackageEnsure     Operation = "packages.feature.ensure"
	OperationPackageStatus     Operation = "packages.feature.status"
	OperationAppImagePrepare   Operation = "app.image.prepare"
	OperationAppImageStatus    Operation = "app.image.status"
	OperationAppImageProbe     Operation = "app.image.probe"
	OperationAppFirewallEnsure Operation = "app.firewall.ensure"
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

type AppLifecycleAction string

const (
	AppLifecycleStart AppLifecycleAction = "start"
	AppLifecycleStop  AppLifecycleAction = "stop"
)

type AppLifecycleParams struct {
	AppID  string             `json:"app_id"`
	Action AppLifecycleAction `json:"action"`
}

type AppInspectParams struct {
	AppID string `json:"app_id"`
}

type AppRemoveParams struct {
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

type DockerRuntimeState struct {
	Status string `json:"status"`
}

type PackageFeature string

const PackageFeatureDockerRuntime PackageFeature = "docker_runtime"

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

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

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
	case OperationFilesEnableLogin:
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid files.enable_login params: %w", err)
		}
	case OperationAppLifecycle:
		var params AppLifecycleParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.lifecycle params: %w", err)
		}
		if _, err := appmanifest.ComposeManifestForApp(params.AppID); err != nil {
			return errors.New("app manifest is not allowed")
		}
		if params.Action != AppLifecycleStart && params.Action != AppLifecycleStop {
			return errors.New("app lifecycle action is not allowed")
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
	case OperationAppRemove:
		var params AppRemoveParams
		if err := decodeStrict(request.Params, &params); err != nil {
			return fmt.Errorf("invalid app.compose.remove params: %w", err)
		}
		if params.AppID != appmanifest.CPUMinerID {
			return errors.New("app manifest is not allowed")
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
		if params.Feature != PackageFeatureDockerRuntime {
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
		if params.Feature != PackageFeatureDockerRuntime {
			return errors.New("package feature is not allowed")
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
		if err := validateCPUMinerImageParams(params); err != nil {
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
	default:
		return errors.New("unknown operation")
	}
	return nil
}

func validateCatalogImageParams(params AppImageParams) error {
	if _, err := appmanifest.CatalogImageForVariant(params.AppID, params.Variant); err != nil {
		return errors.New("app image variant is not allowed")
	}
	return nil
}

func validateCPUMinerImageParams(params AppImageParams) error {
	if params.AppID != appmanifest.CPUMinerID {
		return errors.New("app manifest is not allowed")
	}
	if _, err := appmanifest.CPUMinerImageForVariant(params.Variant); err != nil {
		return errors.New("app image variant is not allowed")
	}
	return nil
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
