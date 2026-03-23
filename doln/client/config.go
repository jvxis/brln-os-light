package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type clientConfig struct {
	mu              sync.RWMutex
	ServerPubkeys   []string `json:"server_pubkeys"`
	DNSTimeoutSec   int      `json:"dns_timeout_seconds"`
	KeysendAmtSat   int64    `json:"keysend_amount_sat"`
	dataDir         string
}

func newClientConfig(pubkeys []string, timeoutSec int, keysendAmt int64, dataDir string) *clientConfig {
	cfg := &clientConfig{
		ServerPubkeys: pubkeys,
		DNSTimeoutSec: timeoutSec,
		KeysendAmtSat: keysendAmt,
		dataDir:       dataDir,
	}
	cfg.loadFromFile()
	return cfg
}

func (c *clientConfig) configPath() string {
	return filepath.Join(c.dataDir, "config.json")
}

func (c *clientConfig) loadFromFile() {
	data, err := os.ReadFile(c.configPath())
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = json.Unmarshal(data, c)
}

func (c *clientConfig) save() error {
	c.mu.RLock()
	data, err := json.MarshalIndent(c, "", "  ")
	c.mu.RUnlock()
	if err != nil {
		return err
	}
	_ = os.MkdirAll(c.dataDir, 0750)
	return os.WriteFile(c.configPath(), data, 0600)
}

func (c *clientConfig) getPubkeys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.ServerPubkeys))
	copy(out, c.ServerPubkeys)
	return out
}

func (c *clientConfig) getTimeout() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DNSTimeoutSec
}

func (c *clientConfig) getKeysendAmt() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.KeysendAmtSat
}

func (c *clientConfig) update(pubkeys []string, timeoutSec int, amt int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ServerPubkeys = pubkeys
	c.DNSTimeoutSec = timeoutSec
	c.KeysendAmtSat = amt
}
