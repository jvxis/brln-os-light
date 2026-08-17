package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

type cpuMinerStatus struct {
	Installed      bool    `json:"installed"`
	Running        bool    `json:"running"`
	Address        string  `json:"address"`
	Worker         string  `json:"worker"`
	PoolMode       string  `json:"pool_mode"`
	PoolLabel      string  `json:"pool_label"`
	Threads        int     `json:"threads"`
	MaxThreads     int     `json:"max_threads"`
	HostCPUCount   int     `json:"host_cpu_count"`
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

func (s *Server) handleCpuMinerThreads(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Threads int `json:"threads"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.setCpuMinerThreads(r.Context(), req.Threads); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCpuMinerConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PoolMode       string `json:"pool_mode"`
		Address        string `json:"address"`
		Worker         string `json:"worker"`
		UseNodeAddress bool   `json:"use_node_address"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := s.setCpuMinerConfig(r.Context(), req.PoolMode, req.Address, req.Worker, req.UseNodeAddress); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) fetchCpuMinerStatus(ctx context.Context) cpuMinerStatus {
	status := cpuMinerStatus{HostCPUCount: runtime.NumCPU()}
	paths := cpuMinerAppPaths()
	if !fileExists(paths.ComposePath) {
		return status
	}
	status.Installed = true
	status.MaxThreads = cpuMinerMaxThreads()
	status.Address = strings.TrimSpace(readEnvValue(paths.EnvPath, "MINING_ADDRESS"))
	status.Worker = strings.TrimSpace(readEnvValue(paths.EnvPath, "WORKER_NAME"))
	poolMode := strings.TrimSpace(readEnvValue(paths.EnvPath, "POOL_MODE"))
	if poolMode == "" {
		poolMode = cpuMinerPoolLocal
	}
	status.PoolMode = poolMode
	status.PoolLabel = cpuMinerPoolLabel(poolMode)
	pool := cpuMinerPoolPreset(poolMode)
	if threads, err := strconv.Atoi(strings.TrimSpace(readEnvValue(paths.EnvPath, "THREADS"))); err == nil && threads > 0 {
		status.Threads = threads
	}

	handled, composeStatus, cpuPercentRaw, _, err := system.InspectAppWithBroker(ctx, cpuMinerAppID)
	if !handled {
		return status
	}
	if err != nil || composeStatus != "running" {
		return status
	}
	status.Running = true

	if hashrate, accepted, rejected, ok := queryCpuMinerSummary("127.0.0.1:" + strconv.Itoa(cpuMinerAPIPort)); ok {
		status.HashrateHs = hashrate
		status.SharesAccepted = accepted
		status.SharesRejected = rejected
	}

	status.CPUPercent = normalizeHostCPUPercent(cpuPercentRaw, runtime.NumCPU())

	if pool.StatsBase != "" {
		if status.Address != "" {
			if bestDiff, ok := fetchPoolBestDifficulty(ctx, pool.StatsBase, status.Address); ok {
				status.BestDifficulty = bestDiff
			}
		}
		status.PoolHashrateHs = fetchPoolHashrate(ctx, pool.StatsBase)
	}

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

// normalizeHostCPUPercent converts Docker's per-core percentage into the
// fraction of the node's total logical CPU capacity. For example, two fully
// busy mining threads on a 16-CPU node are reported as 12.5%, not 200%.
func normalizeHostCPUPercent(perCorePercent float64, hostCPUCount int) float64 {
	if perCorePercent <= 0 || hostCPUCount <= 0 {
		return 0
	}
	value := perCorePercent / float64(hostCPUCount)
	if value > 100 {
		return 100
	}
	return value
}

var dockerCPUPercentPattern = regexp.MustCompile(`([0-9]+(?:[.,][0-9]+)?)[[:space:]]*%`)

func parseDockerCPUPercent(out string) (float64, bool) {
	matches := dockerCPUPercentPattern.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return 0, false
	}
	raw := strings.ReplaceAll(matches[len(matches)-1][1], ",", ".")
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

type containerCPUCounter struct {
	path string
	unit time.Duration
}

var containerCPUCounterCandidates = []containerCPUCounter{
	{path: "/sys/fs/cgroup/cpu.stat", unit: time.Microsecond},
	{path: "/sys/fs/cgroup/cpuacct/cpuacct.usage", unit: time.Nanosecond},
}

// sampleContainerCgroupCPUPercent is a fallback for hosts where docker stats
// returns 0 or an output format the CLI parser cannot consume. This function
// returns Docker's raw per-core percentage; cpuMinerCPUPercent normalizes it to
// the node's total logical CPU capacity before exposing it through the API.
func parseContainerCPUCounter(out string, counter containerCPUCounter) (uint64, bool) {
	if strings.HasSuffix(counter.path, "cpu.stat") {
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "usage_usec" {
				value, err := strconv.ParseUint(fields[1], 10, 64)
				return value, err == nil
			}
		}
		return 0, false
	}
	value, err := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
	return value, err == nil
}

// fetchPoolBestDifficulty reads the best share difficulty Public Pool has seen
// for our payout address — the playful "lottery ticket" metric.
func fetchPoolBestDifficulty(ctx context.Context, base string, address string) (float64, bool) {
	body, ok := poolAPIGet(ctx, base+"/api/client/"+address)
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
func fetchPoolHashrate(ctx context.Context, base string) float64 {
	for _, path := range []string{"/api/pool", "/api/info", "/api/network/info"} {
		body, ok := poolAPIGet(ctx, base+path)
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
