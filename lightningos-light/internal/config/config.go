package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Path          string              `yaml:"-"`
	Server        ServerConfig        `yaml:"server"`
	LND           LNDConfig           `yaml:"lnd"`
	BitcoinRemote BitcoinRemoteConfig `yaml:"bitcoin_remote"`
	Postgres      PostgresConfig      `yaml:"postgres"`
	UI            UIConfig            `yaml:"ui"`
	Features      FeaturesConfig      `yaml:"features"`
	Privileged    PrivilegedConfig    `yaml:"privileged"`
}

type ServerConfig struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	TLSCert string `yaml:"tls_cert"`
	TLSKey  string `yaml:"tls_key"`
}

type LNDConfig struct {
	GRPCHost          string `yaml:"grpc_host"`
	TLSCertPath       string `yaml:"tls_cert_path"`
	AdminMacaroonPath string `yaml:"admin_macaroon_path"`
	SharedGRPC        *bool  `yaml:"shared_grpc"`
}

func (l LNDConfig) SharedGRPCEnabled() bool {
	if l.SharedGRPC == nil {
		return true
	}
	return *l.SharedGRPC
}

type BitcoinRemoteConfig struct {
	RPCHost     string `yaml:"rpchost"`
	ZMQRawBlock string `yaml:"zmq_rawblock"`
	ZMQRawTx    string `yaml:"zmq_rawtx"`
}

type PostgresConfig struct {
	DBName string `yaml:"db_name"`
}

type UIConfig struct {
	StaticDir string `yaml:"static_dir"`
}

type PrivilegedConfig struct {
	Mode           string `yaml:"mode"`
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

type FeaturesConfig struct {
	EnableLogin                   *bool `yaml:"enable_login"`
	EnableBitcoinLocalPlaceholder bool  `yaml:"enable_bitcoin_local_placeholder"`
	EnableAppStorePlaceholder     bool  `yaml:"enable_app_store_placeholder"`
	EnableBalancedOpen            *bool `yaml:"enable_balanced_open"`
}

func (f FeaturesConfig) LoginEnabled() bool {
	if f.EnableLogin == nil {
		return true
	}
	return *f.EnableLogin
}

func (f FeaturesConfig) BalancedOpenEnabled() bool {
	if f.EnableBalancedOpen == nil {
		return true
	}
	return *f.EnableBalancedOpen
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	cfg.Path = path

	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8443
	}
	if cfg.LND.GRPCHost == "" {
		cfg.LND.GRPCHost = "127.0.0.1:10009"
	}
	if raw := strings.TrimSpace(os.Getenv("LND_SHARED_GRPC")); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid LND_SHARED_GRPC value %q", raw)
		}
		cfg.LND.SharedGRPC = &enabled
	}
	if cfg.UI.StaticDir == "" {
		cfg.UI.StaticDir = "/opt/lightningos/ui"
	}
	if raw := strings.TrimSpace(os.Getenv("LIGHTNINGOS_PRIVILEGED_MODE")); raw != "" {
		cfg.Privileged.Mode = raw
	}
	if cfg.Privileged.Mode == "" {
		cfg.Privileged.Mode = "disabled"
	}
	cfg.Privileged.Mode = strings.ToLower(strings.TrimSpace(cfg.Privileged.Mode))
	switch cfg.Privileged.Mode {
	case "disabled", "shadow", "enforce":
	default:
		return nil, fmt.Errorf("invalid privileged.mode value %q", cfg.Privileged.Mode)
	}
	if raw := strings.TrimSpace(os.Getenv("LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid LIGHTNINGOS_PRIVILEGED_TIMEOUT_SECONDS value %q", raw)
		}
		cfg.Privileged.TimeoutSeconds = value
	}
	if cfg.Privileged.TimeoutSeconds == 0 {
		cfg.Privileged.TimeoutSeconds = 5
	}
	if cfg.Privileged.TimeoutSeconds < 1 || cfg.Privileged.TimeoutSeconds > 30 {
		return nil, fmt.Errorf("privileged.timeout_seconds must be between 1 and 30")
	}

	if cfg.Server.TLSCert == "" || cfg.Server.TLSKey == "" {
		return nil, fmt.Errorf("server TLS cert/key required")
	}

	return &cfg, nil
}
