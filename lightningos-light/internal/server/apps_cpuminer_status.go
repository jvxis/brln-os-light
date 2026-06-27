package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

type cpuMinerStatus struct {
	Installed      bool    `json:"installed"`
	Running        bool    `json:"running"`
	Address        string  `json:"address"`
	Threads        int     `json:"threads"`
	HashrateHs     float64 `json:"hashrate_hs"`
	SharesAccepted int64   `json:"shares_accepted"`
	SharesRejected int64   `json:"shares_rejected"`
	BestDifficulty float64 `json:"best_difficulty"`
	PoolHashrateHs float64 `json:"pool_hashrate_hs"`
	CPUPercent     float64 `json:"cpu_percent"`
}

func (s *Server) handleCpuMinerStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.fetchCpuMinerStatus(r.Context()))
}

func (s *Server) fetchCpuMinerStatus(ctx context.Context) cpuMinerStatus {
	status := cpuMinerStatus{}
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return status
	}
	status.Installed = true
	status.Address = strings.TrimSpace(readEnvValue(paths.EnvPath, "MINING_ADDRESS"))
	if threads, err := strconv.Atoi(strings.TrimSpace(readEnvValue(paths.EnvPath, "THREADS"))); err == nil && threads > 0 {
		status.Threads = threads
	}

	composeStatus, err := getComposeStatus(ctx, paths.Root, paths.ComposePath, cpuMinerAppID)
	if err != nil || composeStatus != "running" {
		return status
	}
	status.Running = true

	if hashrate, accepted, rejected, ok := queryCpuMinerSummary("127.0.0.1:" + strconv.Itoa(cpuMinerAPIPort)); ok {
		status.HashrateHs = hashrate
		status.SharesAccepted = accepted
		status.SharesRejected = rejected
	}

	status.CPUPercent = cpuMinerCPUPercent(ctx, paths)

	if status.Address != "" {
		if bestDiff, ok := fetchPoolBestDifficulty(ctx, status.Address); ok {
			status.BestDifficulty = bestDiff
		}
	}
	status.PoolHashrateHs = fetchPoolHashrate(ctx)

	return status
}

// queryCpuMinerSummary talks to the cpuminer API (cgminer-style: a single
// command, response is ';'-separated key=value pairs) and extracts hashrate and
// share counts. It degrades silently — the card never depends on it.
func queryCpuMinerSummary(addr string) (hashrateHs float64, accepted int64, rejected int64, ok bool) {
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return 0, 0, 0, false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("summary")); err != nil {
		return 0, 0, 0, false
	}
	raw, err := io.ReadAll(conn)
	if err != nil && len(raw) == 0 {
		return 0, 0, 0, false
	}
	fields := parseCpuMinerSummary(string(raw))
	if len(fields) == 0 {
		return 0, 0, 0, false
	}
	if khs, err := strconv.ParseFloat(fields["KHS"], 64); err == nil {
		hashrateHs = khs * 1000
	}
	if acc, err := strconv.ParseInt(fields["ACC"], 10, 64); err == nil {
		accepted = acc
	}
	if rej, err := strconv.ParseInt(fields["REJ"], 10, 64); err == nil {
		rejected = rej
	}
	return hashrateHs, accepted, rejected, true
}

func parseCpuMinerSummary(raw string) map[string]string {
	fields := map[string]string{}
	normalized := strings.NewReplacer("|", ";", "\x00", "").Replace(raw)
	for _, pair := range strings.Split(normalized, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(kv[0]))
		fields[key] = strings.TrimSpace(kv[1])
	}
	return fields
}

func cpuMinerCPUPercent(ctx context.Context, paths cpuMinerPaths) float64 {
	id, err := composeContainerID(ctx, paths.Root, paths.ComposePath, cpuMinerAppID)
	if err != nil || strings.TrimSpace(id) == "" {
		return 0
	}
	out, err := system.RunCommandWithSudo(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}", id)
	if err != nil {
		return 0
	}
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(out), "%"))
	value, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0
	}
	return value
}

// fetchPoolBestDifficulty reads the best share difficulty Public Pool has seen
// for our payout address — the playful "lottery ticket" metric.
func fetchPoolBestDifficulty(ctx context.Context, address string) (float64, bool) {
	body, ok := poolAPIGet(ctx, "http://127.0.0.1:"+strconv.Itoa(publicPoolAPIPort)+"/api/client/"+address)
	if !ok {
		return 0, false
	}
	var payload struct {
		BestDifficulty json.RawMessage `json:"bestDifficulty"`
		Workers        []struct {
			BestDifficulty json.RawMessage `json:"bestDifficulty"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, false
	}
	best := jsonNumber(payload.BestDifficulty)
	for _, wkr := range payload.Workers {
		if v := jsonNumber(wkr.BestDifficulty); v > best {
			best = v
		}
	}
	if best <= 0 {
		return 0, false
	}
	return best, true
}

// fetchPoolHashrate attempts to read the aggregate pool hashrate. The Public
// Pool fork exposes this loosely, so we parse tolerantly and return 0 when the
// endpoint or field is absent (the UI simply hides it then).
func fetchPoolHashrate(ctx context.Context) float64 {
	for _, path := range []string{"/api/pool", "/api/info", "/api/network/info"} {
		body, ok := poolAPIGet(ctx, "http://127.0.0.1:"+strconv.Itoa(publicPoolAPIPort)+path)
		if !ok {
			continue
		}
		generic := map[string]any{}
		if err := json.Unmarshal(body, &generic); err != nil {
			continue
		}
		for _, key := range []string{"hashRate", "hashrate", "poolHashRate", "totalHashRate"} {
			if v, found := generic[key]; found {
				if f := anyToFloat(v); f > 0 {
					return f
				}
			}
		}
	}
	return 0
}

func poolAPIGet(ctx context.Context, url string) ([]byte, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	return body, true
}

func jsonNumber(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return num
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if v, err := strconv.ParseFloat(strings.TrimSpace(str), 64); err == nil {
			return v
		}
	}
	return 0
}

func anyToFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
			return f
		}
	}
	return 0
}
