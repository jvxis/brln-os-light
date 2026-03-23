package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type serverConfig struct {
	mu             sync.RWMutex
	AdguardDNSHost string `json:"adguard_dns_host"`
	AdguardDNSPort string `json:"adguard_dns_port"`
	KeysendAmtSat  int64  `json:"keysend_amount_sat"`
	dataDir        string
}

func newServerConfig(adguardHost, adguardPort string, keysendAmt int64, dataDir string) *serverConfig {
	cfg := &serverConfig{
		AdguardDNSHost: adguardHost,
		AdguardDNSPort: adguardPort,
		KeysendAmtSat:  keysendAmt,
		dataDir:        dataDir,
	}
	cfg.loadFromFile()
	return cfg
}

func (c *serverConfig) configPath() string {
	return filepath.Join(c.dataDir, "config.json")
}

func (c *serverConfig) loadFromFile() {
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = json.Unmarshal(data, c)
}

func (c *serverConfig) save() error {
	c.mu.RLock()
	data, err := json.MarshalIndent(c, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(c.dataDir, 0750)
	return os.WriteFile(c.configPath(), data, 0600)
}

func (c *serverConfig) dnsAddr() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AdguardDNSHost + ":" + c.AdguardDNSPort
}

func (c *serverConfig) keysendAmt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.KeysendAmtSat
}

func (c *serverConfig) update(host, port string, amt int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AdguardDNSHost = host
	c.AdguardDNSPort = port
	c.KeysendAmtSat = amt
}
