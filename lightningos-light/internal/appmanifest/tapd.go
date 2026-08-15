package appmanifest

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	TapdID             = "tapd"
	TapdProject        = "tapd"
	TapdComposeFile    = "docker-compose.yaml"
	TapdConfigFile     = "tapd.conf"
	TapdPrimaryService = "tapd"
	TapdLNDDir         = "lnd"
	TapdTLSCertFile    = "tls.cert"
	TapdMacaroonFile   = "tapd.macaroon"
	TapdStopTimeout    = 60
	TapdRPCPort        = 10029
	TapdRESTPort       = 8089
	TapdNetwork        = "mainnet"

	TapdRelease                             = "0.8.0"
	TapdManifestSHA256                      = "868e8dec4174798eaff056336eb9b3ba1bd387590c0f29201781f98702cc567d"
	TapdImage                               = "lightninglabs/taproot-assets:v" + TapdRelease + "@sha256:" + TapdManifestSHA256
	TapdImageApp            AppImageVariant = "app"
	TapdDaemonVersionOutput                 = "tapd version 0.8.0-alpha commit=v0.8.0"
	TapdCLIVersionOutput                    = "tapcli version 0.8.0-alpha commit=v0.8.0"

	TapdDataDirInContainer  = "/root/.tapd"
	TapdConfigInContainer   = "/etc/tapd/tapd.conf"
	TapdTLSCertInContainer  = "/etc/lnd/tls.cert"
	TapdMacaroonInContainer = "/etc/lnd/tapd.macaroon"
	// Keep the complete typed broker request comfortably below its 64 KiB
	// transport ceiling after JSON escaping and protocol framing.
	TapdMaxMetadataBytes = 8 * 1024
)

type TapdComposePaths struct {
	DataDir      string
	ConfigPath   string
	TLSCertPath  string
	MacaroonPath string
}

type TapdCLICommand string

const (
	TapdCLIGetInfo       TapdCLICommand = "get_info"
	TapdCLIAssetsBalance TapdCLICommand = "assets_balance"
	TapdCLIAddressNew    TapdCLICommand = "address_new"
	TapdCLIUniverseSync  TapdCLICommand = "universe_sync"
	TapdCLIMint          TapdCLICommand = "mint"
	TapdCLIMintFinalize  TapdCLICommand = "mint_finalize"
	TapdCLISend          TapdCLICommand = "send"
	TapdCLIDecodeAddress TapdCLICommand = "decode_address"
)

// TapdCLIRequest is the entire manager-to-broker command surface for tapcli.
// The broker converts it into fixed argv; callers can never submit raw flags.
type TapdCLIRequest struct {
	Command        TapdCLICommand `json:"command"`
	AssetID        string         `json:"asset_id,omitempty"`
	GroupKey       string         `json:"group_key,omitempty"`
	Amount         uint64         `json:"amount,omitempty"`
	UniverseHost   string         `json:"universe_host,omitempty"`
	Name           string         `json:"name,omitempty"`
	Supply         uint64         `json:"supply,omitempty"`
	DecimalDisplay uint32         `json:"decimal_display,omitempty"`
	Grouped        bool           `json:"grouped,omitempty"`
	Metadata       string         `json:"metadata,omitempty"`
	Address        string         `json:"address,omitempty"`
	FeeRate        uint32         `json:"fee_rate,omitempty"`
}

func TapdImageForVariant(variant AppImageVariant) (string, error) {
	if variant != TapdImageApp {
		return "", errors.New("tapd image variant is not allowed")
	}
	return TapdImage, nil
}

func TapdImageVariants() []AppImageVariant {
	return []AppImageVariant{TapdImageApp}
}

// TapdConfig returns the only daemon configuration accepted by the broker.
// RPC and REST remain loopback-only even though host networking is required to
// reach native LND and PostgreSQL.
func TapdConfig(databasePassword string) (string, error) {
	if !validTapdDatabasePassword(databasePassword) {
		return "", errors.New("tapd database credential is invalid")
	}
	return fmt.Sprintf(`network=%s
debuglevel=info
rpclisten=127.0.0.1:%d
restlisten=127.0.0.1:%d
tlsextradomain=localhost

lnd.host=127.0.0.1:10009
lnd.macaroonpath=%s
lnd.tlspath=%s

databasebackend=postgres
postgres.host=127.0.0.1
postgres.port=5432
postgres.user=tapd
postgres.password=%s
postgres.dbname=tapd
postgres.requiressl=false
`, TapdNetwork, TapdRPCPort, TapdRESTPort, TapdMacaroonInContainer,
		TapdTLSCertInContainer, databasePassword), nil
}

func ParseTapdConfig(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 64*1024 || raw[len(raw)-1] != '\n' || strings.ContainsAny(string(raw), "\r\x00") {
		return "", errors.New("invalid tapd configuration encoding")
	}
	const marker = "postgres.password="
	start := strings.Index(string(raw), marker)
	if start < 0 {
		return "", errors.New("tapd database credential is missing")
	}
	start += len(marker)
	end := strings.IndexByte(string(raw[start:]), '\n')
	if end < 0 {
		return "", errors.New("tapd database credential is invalid")
	}
	password := string(raw[start : start+end])
	expected, err := TapdConfig(password)
	if err != nil || string(raw) != expected {
		return "", errors.New("tapd configuration does not match the catalog")
	}
	return password, nil
}

func validTapdDatabasePassword(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// TapdCompose contains no Docker socket, host LND tree, published port, or
// mutable image. Root inside the container has no Linux capabilities and can
// write only the persistent tapd data mount and bounded tmpfs.
func TapdCompose(paths TapdComposePaths) string {
	return fmt.Sprintf(`services:
  tapd:
    image: %s
    container_name: tapd
    restart: unless-stopped
    stop_grace_period: %ds
    init: true
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    network_mode: host
    environment:
      HOME: /root
    command:
      - --tapddir=%s
      - --configfile=%s
    tmpfs:
      - /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777
    volumes:
      - %s:%s:rw
      - %s:%s:ro
      - %s:%s:ro
      - %s:%s:ro
`, TapdImage, TapdStopTimeout, TapdDataDirInContainer, TapdConfigInContainer,
		paths.DataDir, TapdDataDirInContainer, paths.ConfigPath,
		TapdConfigInContainer, paths.TLSCertPath, TapdTLSCertInContainer,
		paths.MacaroonPath, TapdMacaroonInContainer)
}

func ValidateTapdCLIRequest(request TapdCLIRequest) error {
	if request.FeeRate > 10_000 || request.DecimalDisplay > 18 {
		return errors.New("tapd numeric parameter is outside the allowed range")
	}
	if request.AssetID != "" && !validTapdHex(request.AssetID, 32) {
		return errors.New("tapd asset ID is invalid")
	}
	if request.GroupKey != "" && !validTapdHex(request.GroupKey, 33) {
		return errors.New("tapd group key is invalid")
	}
	if request.Metadata != "" && (len(request.Metadata) > TapdMaxMetadataBytes ||
		!utf8.ValidString(request.Metadata) || !json.Valid([]byte(request.Metadata))) {
		return errors.New("tapd metadata is invalid")
	}

	empty := func(values ...string) bool {
		for _, value := range values {
			if value != "" {
				return false
			}
		}
		return true
	}
	zeroCommon := func() bool {
		return request.Amount == 0 && request.Supply == 0 && request.DecimalDisplay == 0 &&
			!request.Grouped && request.FeeRate == 0
	}

	switch request.Command {
	case TapdCLIGetInfo, TapdCLIAssetsBalance:
		if !empty(request.AssetID, request.GroupKey, request.UniverseHost, request.Name, request.Metadata, request.Address) || !zeroCommon() {
			return errors.New("tapd command contains unexpected parameters")
		}
	case TapdCLIAddressNew:
		if (request.AssetID == "") == (request.GroupKey == "") || request.Amount == 0 ||
			!empty(request.UniverseHost, request.Name, request.Metadata, request.Address) ||
			request.Supply != 0 || request.DecimalDisplay != 0 || request.Grouped || request.FeeRate != 0 {
			return errors.New("tapd address parameters are invalid")
		}
	case TapdCLIUniverseSync:
		if !validTapdUniverseHost(request.UniverseHost) || (request.AssetID != "" && request.GroupKey != "") ||
			!empty(request.Name, request.Metadata, request.Address) || request.Amount != 0 ||
			request.Supply != 0 || request.DecimalDisplay != 0 || request.Grouped || request.FeeRate != 0 {
			return errors.New("tapd universe parameters are invalid")
		}
	case TapdCLIMint:
		if !validTapdAssetName(request.Name) || request.Supply == 0 || request.AssetID != "" ||
			request.Amount != 0 || request.UniverseHost != "" || request.Address != "" || request.FeeRate != 0 {
			return errors.New("tapd mint parameters are invalid")
		}
		if request.GroupKey != "" && request.Grouped {
			return errors.New("tapd mint group selection is ambiguous")
		}
	case TapdCLIMintFinalize:
		if !empty(request.AssetID, request.GroupKey, request.UniverseHost, request.Name, request.Metadata, request.Address) ||
			request.Amount != 0 || request.Supply != 0 || request.DecimalDisplay != 0 || request.Grouped {
			return errors.New("tapd mint finalize parameters are invalid")
		}
	case TapdCLISend:
		if !validTapdAddress(request.Address) || !empty(request.AssetID, request.GroupKey, request.UniverseHost, request.Name, request.Metadata) ||
			request.Amount != 0 || request.Supply != 0 || request.DecimalDisplay != 0 || request.Grouped {
			return errors.New("tapd send parameters are invalid")
		}
	case TapdCLIDecodeAddress:
		if !validTapdAddress(request.Address) || !empty(request.AssetID, request.GroupKey, request.UniverseHost, request.Name, request.Metadata) ||
			!zeroCommon() {
			return errors.New("tapd decode parameters are invalid")
		}
	default:
		return errors.New("tapd command is not allowed")
	}
	return nil
}

func TapdCLIArgs(request TapdCLIRequest) ([]string, error) {
	if err := ValidateTapdCLIRequest(request); err != nil {
		return nil, err
	}
	var args []string
	switch request.Command {
	case TapdCLIGetInfo:
		args = []string{"getinfo"}
	case TapdCLIAssetsBalance:
		args = []string{"assets", "balance"}
	case TapdCLIAddressNew:
		args = []string{"addrs", "new"}
		if request.AssetID != "" {
			args = append(args, "--asset_id", request.AssetID)
		} else {
			args = append(args, "--group_key", request.GroupKey)
		}
		args = append(args, "--amt", strconv.FormatUint(request.Amount, 10))
	case TapdCLIUniverseSync:
		args = []string{"universe", "sync", "--universe_host", request.UniverseHost}
		if request.GroupKey != "" {
			args = append(args, "--group_key", request.GroupKey)
		} else if request.AssetID != "" {
			args = append(args, "--asset_id", request.AssetID)
		}
	case TapdCLIMint:
		args = []string{"assets", "mint", "--type", "normal", "--name", request.Name, "--supply", strconv.FormatUint(request.Supply, 10)}
		if request.DecimalDisplay > 0 {
			args = append(args, "--decimal_display", strconv.FormatUint(uint64(request.DecimalDisplay), 10))
		}
		if request.GroupKey != "" {
			args = append(args, "--grouped_asset", "--group_key", request.GroupKey)
		} else if request.Grouped {
			args = append(args, "--new_grouped_asset")
		}
		if request.Metadata != "" {
			args = append(args, "--meta_bytes", request.Metadata, "--meta_type", "json")
		}
	case TapdCLIMintFinalize:
		args = []string{"assets", "mint", "finalize"}
		if request.FeeRate > 0 {
			args = append(args, "--sat_per_vbyte", strconv.FormatUint(uint64(request.FeeRate), 10))
		}
	case TapdCLISend:
		args = []string{"assets", "send", "--addr", request.Address}
		if request.FeeRate > 0 {
			args = append(args, "--sat_per_vbyte", strconv.FormatUint(uint64(request.FeeRate), 10))
		}
	case TapdCLIDecodeAddress:
		args = []string{"addrs", "decode", "--addr", request.Address}
	}
	return args, nil
}

func validTapdHex(value string, bytes int) bool {
	if len(value) != bytes*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == bytes
}

func validTapdAssetName(value string) bool {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validTapdAddress(value string) bool {
	if len(value) < 16 || len(value) > 2048 || !strings.HasPrefix(value, "tapbc1") || strings.ToLower(value) != value {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validTapdUniverseHost(value string) bool {
	if value == "" || len(value) > 320 || strings.ContainsAny(value, " /\\?#@") || strings.Contains(value, "://") {
		return false
	}
	host := value
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		parsedHost, port, err := net.SplitHostPort(value)
		if err != nil {
			return false
		}
		parsedPort, err := strconv.Atoi(port)
		if err != nil || parsedPort < 1 || parsedPort > 65535 {
			return false
		}
		host = parsedHost
	} else if strings.Contains(value, ":") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range strings.ToLower(label) {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}
