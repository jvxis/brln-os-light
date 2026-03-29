package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	LND           LNDConfig           `yaml:"lnd"`
	BitcoinRemote BitcoinRemoteConfig `yaml:"bitcoin_remote"`
	Postgres      PostgresConfig      `yaml:"postgres"`
	UI            UIConfig            `yaml:"ui"`
	Features      FeaturesConfig      `yaml:"features"`
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

	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8443
	}
	if cfg.LND.GRPCHost == "" {
		cfg.LND.GRPCHost = "127.0.0.1:10009"
	}
	if cfg.UI.StaticDir == "" {
		cfg.UI.StaticDir = "/opt/lightningos/ui"
	}

	if cfg.Server.TLSCert == "" || cfg.Server.TLSKey == "" {
		return nil, fmt.Errorf("server TLS cert/key required")
	}

	return &cfg, nil
}
