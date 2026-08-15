package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	bip110PublicMonitorURL       = "https://bip110monitor.com/api"
	bip110NonEnforcingTipURL     = "https://mempool.space/api/blocks/tip/height"
	bip110EnforcingTipURL        = "https://mempool.guide/api/blocks/tip/height"
	bip110PeriodLength           = int64(2016)
	bip110ThresholdCount         = 1109
	bip110ThresholdPct           = 55.0
	bip110SignalBit              = uint(4)
	bip110MandatoryStartHeight   = int64(961632)
	bip110ForcedLockInHeight     = int64(963648)
	bip110ForcedActivationHeight = int64(965664)
	bip110ActiveDuration         = int64(52416)
	bip110CacheTTL               = 2 * time.Minute
	bip110PartialCacheTTL        = 30 * time.Second
	bip110RequestTimeout         = 18 * time.Second
	bip110PublicRequestTimeout   = 5 * time.Second
	bip110HTTPResponseLimit      = 16 << 20
)

type bip110Period struct {
	PeriodNum      int64   `json:"period_num"`
	StartBlock     int64   `json:"start_block"`
	EndBlock       int64   `json:"end_block"`
	SignalingCount int     `json:"signaling_count"`
	TotalBlocks    int     `json:"total_blocks"`
	Pct            float64 `json:"pct"`
}

type bip110PublicAPIResponse struct {
	BIP            string            `json:"bip"`
	Tip            int64             `json:"tip"`
	ChainTip       int64             `json:"chainTip"`
	PeriodNum      int64             `json:"periodNum"`
	PeriodStart    int64             `json:"periodStart"`
	PeriodEnd      int64             `json:"periodEnd"`
	TotalBlocks    int               `json:"totalBlocks"`
	SignalingCount int               `json:"signalingCount"`
	Pct            float64           `json:"pct"`
	Synced         bool              `json:"synced"`
	UpdatedAt      string            `json:"updatedAt"`
	Periods        []bip110APIPeriod `json:"periods"`
}

type bip110APIPeriod struct {
	PeriodNum      int64   `json:"periodNum"`
	StartBlock     int64   `json:"startBlock"`
	EndBlock       int64   `json:"endBlock"`
	SignalingCount int     `json:"signalingCount"`
	TotalBlocks    int     `json:"totalBlocks"`
	Pct            float64 `json:"pct"`
}

type bip110SourceStatus struct {
	Available      bool           `json:"available"`
	Source         string         `json:"source"`
	Tip            int64          `json:"tip,omitempty"`
	SampledTip     int64          `json:"sampled_tip,omitempty"`
	BestBlockHash  string         `json:"best_block_hash,omitempty"`
	Chainwork      string         `json:"chainwork,omitempty"`
	Subversion     string         `json:"subversion,omitempty"`
	EnforcesBIP110 *bool          `json:"enforces_bip110,omitempty"`
	PeriodNum      int64          `json:"period_num,omitempty"`
	PeriodStart    int64          `json:"period_start,omitempty"`
	PeriodEnd      int64          `json:"period_end,omitempty"`
	TotalBlocks    int            `json:"total_blocks"`
	SignalingCount int            `json:"signaling_count"`
	Pct            float64        `json:"pct"`
	Synced         bool           `json:"synced,omitempty"`
	UpdatedAt      string         `json:"updated_at,omitempty"`
	Error          string         `json:"error,omitempty"`
	RecentPeriods  []bip110Period `json:"recent_periods,omitempty"`
}

type bip110Comparison struct {
	Comparable          bool    `json:"comparable"`
	Matches             bool    `json:"matches"`
	Status              string  `json:"status"`
	SamePeriod          bool    `json:"same_period"`
	TipDelta            int64   `json:"tip_delta"`
	SignalingCountDelta int     `json:"signaling_count_delta"`
	PctDelta            float64 `json:"pct_delta"`
}

type bip110ForkScore struct {
	Available          bool   `json:"available"`
	SplitHeight        int64  `json:"split_height"`
	NonEnforcingTip    int64  `json:"non_enforcing_tip,omitempty"`
	EnforcingTip       int64  `json:"enforcing_tip,omitempty"`
	NonEnforcingBlocks int64  `json:"non_enforcing_blocks"`
	EnforcingBlocks    int64  `json:"enforcing_blocks"`
	NonEnforcingSource string `json:"non_enforcing_source"`
	EnforcingSource    string `json:"enforcing_source"`
	Error              string `json:"error,omitempty"`
}

type bip110MonitorStatus struct {
	InformationalOnly      bool               `json:"informational_only"`
	RiskLevel              string             `json:"risk_level"`
	Phase                  string             `json:"phase"`
	CheckedAt              string             `json:"checked_at"`
	SignalBit              uint               `json:"signal_bit"`
	ThresholdCount         int                `json:"threshold_count"`
	ThresholdPct           float64            `json:"threshold_pct"`
	MandatoryStartHeight   int64              `json:"mandatory_start_height"`
	LockInHeight           int64              `json:"lock_in_height"`
	ActivationHeight       int64              `json:"activation_height"`
	ForcedLockInHeight     int64              `json:"forced_lock_in_height"`
	ForcedActivationHeight int64              `json:"forced_activation_height"`
	BlocksToMandatory      int64              `json:"blocks_to_mandatory"`
	Internal               bip110SourceStatus `json:"internal"`
	Public                 bip110SourceStatus `json:"public"`
	Comparison             bip110Comparison   `json:"comparison"`
	ForkScore              bip110ForkScore    `json:"fork_score"`
}

type cachedBIP110MonitorStatus struct {
	value     bip110MonitorStatus
	expiresAt time.Time
}

type bip110MonitorService struct {
	server          *Server
	client          *http.Client
	publicURL       string
	nonEnforcingURL string
	enforcingURL    string
	now             func() time.Time
	loadRPCConfig   func(context.Context) (bitcoinRPCConfig, string, error)

	mu    sync.Mutex
	cache cachedBIP110MonitorStatus
	group singleflight.Group

	sampleMu  sync.Mutex
	sampleKey string
	samples   map[int64]bip110BlockSample
}

type bip110BlockSample struct {
	Hash    string
	Version int64
}

func newBIP110MonitorService(server *Server) *bip110MonitorService {
	service := &bip110MonitorService{
		server:          server,
		client:          &http.Client{Timeout: 12 * time.Second},
		publicURL:       bip110PublicMonitorURL,
		nonEnforcingURL: bip110NonEnforcingTipURL,
		enforcingURL:    bip110EnforcingTipURL,
		now:             time.Now,
	}
	service.loadRPCConfig = service.activeRPCConfig
	return service
}

func (s *bip110MonitorService) status(ctx context.Context) bip110MonitorStatus {
	now := s.now()
	s.mu.Lock()
	if now.Before(s.cache.expiresAt) {
		value := s.cache.value
		s.mu.Unlock()
		return value
	}
	s.mu.Unlock()

	resultCh := s.group.DoChan("status", func() (any, error) {
		now := s.now()
		s.mu.Lock()
		if now.Before(s.cache.expiresAt) {
			value := s.cache.value
			s.mu.Unlock()
			return value, nil
		}
		s.mu.Unlock()

		fetchCtx, cancel := context.WithTimeout(context.Background(), bip110RequestTimeout-time.Second)
		defer cancel()
		value := s.fetchStatus(fetchCtx, now)
		ttl := bip110CacheTTL
		if !value.Internal.Available || !value.Public.Available {
			ttl = bip110PartialCacheTTL
		}
		s.mu.Lock()
		s.cache = cachedBIP110MonitorStatus{value: value, expiresAt: now.Add(ttl)}
		s.mu.Unlock()
		return value, nil
	})

	select {
	case <-ctx.Done():
		s.mu.Lock()
		value := s.cache.value
		s.mu.Unlock()
		if value.CheckedAt != "" {
			return value
		}
		return unavailableBIP110Status(now, ctx.Err())
	case result := <-resultCh:
		value, _ := result.Val.(bip110MonitorStatus)
		return value
	}
}

func unavailableBIP110Status(now time.Time, err error) bip110MonitorStatus {
	message := "monitor unavailable"
	if err != nil {
		message = err.Error()
	}
	return bip110MonitorStatus{
		InformationalOnly:      true,
		RiskLevel:              "unknown",
		Phase:                  "unknown",
		CheckedAt:              now.UTC().Format(time.RFC3339),
		SignalBit:              bip110SignalBit,
		ThresholdCount:         bip110ThresholdCount,
		ThresholdPct:           bip110ThresholdPct,
		MandatoryStartHeight:   bip110MandatoryStartHeight,
		LockInHeight:           bip110ForcedLockInHeight,
		ActivationHeight:       bip110ForcedActivationHeight,
		ForcedLockInHeight:     bip110ForcedLockInHeight,
		ForcedActivationHeight: bip110ForcedActivationHeight,
		Internal:               bip110SourceStatus{Source: "active_bitcoind", Error: message},
		Public:                 bip110SourceStatus{Source: bip110PublicMonitorURL, Error: message},
		Comparison:             bip110Comparison{Status: "unavailable"},
	}
}

func (s *bip110MonitorService) fetchStatus(ctx context.Context, now time.Time) bip110MonitorStatus {
	publicCh := make(chan bip110SourceStatus, 1)
	type tipResult struct {
		tip int64
		err error
	}
	nonEnforcingCh := make(chan tipResult, 1)
	enforcingCh := make(chan tipResult, 1)
	go func() {
		publicCtx, cancel := context.WithTimeout(ctx, bip110PublicRequestTimeout)
		defer cancel()
		publicCh <- s.fetchPublic(publicCtx)
	}()
	go func() {
		nonEnforcingCtx, cancel := context.WithTimeout(ctx, bip110PublicRequestTimeout)
		defer cancel()
		tip, err := s.fetchChainTip(nonEnforcingCtx, s.nonEnforcingURL, "non-enforcing")
		nonEnforcingCh <- tipResult{tip: tip, err: err}
	}()
	go func() {
		enforcingCtx, cancel := context.WithTimeout(ctx, bip110PublicRequestTimeout)
		defer cancel()
		tip, err := s.fetchChainTip(enforcingCtx, s.enforcingURL, "enforcing")
		enforcingCh <- tipResult{tip: tip, err: err}
	}()
	public := <-publicCh
	nonEnforcing := <-nonEnforcingCh
	enforcing := <-enforcingCh
	internal := s.fetchInternal(ctx, public)
	comparison := compareBIP110Sources(internal, public)
	forkScore := buildBIP110ForkScore(
		nonEnforcing.tip, nonEnforcing.err, s.nonEnforcingURL,
		enforcing.tip, enforcing.err, s.enforcingURL,
	)
	tip := internal.Tip
	if tip == 0 {
		tip = public.Tip
	}
	lockInHeight, activationHeight := bip110EffectiveMilestones(tip, internal, public)
	blocksToMandatory := bip110MandatoryStartHeight - tip
	if blocksToMandatory < 0 {
		blocksToMandatory = 0
	}
	if lockInHeight < bip110ForcedLockInHeight && tip >= lockInHeight {
		blocksToMandatory = 0
	}
	phase := bip110Phase(tip, lockInHeight, activationHeight)
	risk := bip110RiskLevel(tip, internal, public, comparison, phase)

	return bip110MonitorStatus{
		InformationalOnly:      true,
		RiskLevel:              risk,
		Phase:                  phase,
		CheckedAt:              now.UTC().Format(time.RFC3339),
		SignalBit:              bip110SignalBit,
		ThresholdCount:         bip110ThresholdCount,
		ThresholdPct:           bip110ThresholdPct,
		MandatoryStartHeight:   bip110MandatoryStartHeight,
		LockInHeight:           lockInHeight,
		ActivationHeight:       activationHeight,
		ForcedLockInHeight:     bip110ForcedLockInHeight,
		ForcedActivationHeight: bip110ForcedActivationHeight,
		BlocksToMandatory:      blocksToMandatory,
		Internal:               internal,
		Public:                 public,
		Comparison:             comparison,
		ForkScore:              forkScore,
	}
}

func (s *bip110MonitorService) fetchChainTip(ctx context.Context, sourceURL, chainLabel string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("User-Agent", "lightningos-light")
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s chain source status %d", chainLabel, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		return 0, err
	}
	tip, err := strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
	if err != nil || tip <= 0 {
		return 0, fmt.Errorf("invalid %s chain tip", chainLabel)
	}
	return tip, nil
}

func buildBIP110ForkScore(
	nonEnforcingTip int64,
	nonEnforcingErr error,
	nonEnforcingSource string,
	enforcingTip int64,
	enforcingErr error,
	enforcingSource string,
) bip110ForkScore {
	splitHeight := bip110MandatoryStartHeight - 1
	status := bip110ForkScore{
		SplitHeight:        splitHeight,
		NonEnforcingSource: nonEnforcingSource,
		EnforcingSource:    enforcingSource,
		NonEnforcingTip:    nonEnforcingTip,
		EnforcingTip:       enforcingTip,
	}
	if nonEnforcingErr != nil {
		status.Error = nonEnforcingErr.Error()
		return status
	}
	if enforcingErr != nil {
		status.Error = enforcingErr.Error()
		return status
	}
	if nonEnforcingTip < splitHeight || enforcingTip < splitHeight {
		status.Error = "fork score sources are behind the split height"
		return status
	}
	status.Available = true
	status.NonEnforcingBlocks = nonEnforcingTip - splitHeight
	status.EnforcingBlocks = enforcingTip - splitHeight
	return status
}

func (s *bip110MonitorService) fetchPublic(ctx context.Context) bip110SourceStatus {
	status := bip110SourceStatus{Source: s.publicURL}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.publicURL, nil)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "lightningos-light")
	resp, err := s.client.Do(req)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		status.Error = fmt.Sprintf("public monitor status %d", resp.StatusCode)
		return status
	}
	var payload bip110PublicAPIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, bip110HTTPResponseLimit)).Decode(&payload); err != nil {
		status.Error = err.Error()
		return status
	}
	if payload.BIP != "110" || payload.Tip <= 0 || payload.PeriodStart < 0 || payload.PeriodEnd < payload.PeriodStart {
		status.Error = "invalid public monitor payload"
		return status
	}
	status.Available = true
	status.Tip = payload.Tip
	status.SampledTip = payload.Tip
	status.PeriodNum = payload.PeriodNum
	status.PeriodStart = payload.PeriodStart
	status.PeriodEnd = payload.PeriodEnd
	status.TotalBlocks = payload.TotalBlocks
	status.SignalingCount = payload.SignalingCount
	status.Pct = roundBIP110Pct(payload.Pct)
	status.Synced = payload.Synced
	status.UpdatedAt = payload.UpdatedAt
	status.RecentPeriods = make([]bip110Period, 0, len(payload.Periods))
	for _, period := range payload.Periods {
		status.RecentPeriods = append(status.RecentPeriods, bip110Period{
			PeriodNum:      period.PeriodNum,
			StartBlock:     period.StartBlock,
			EndBlock:       period.EndBlock,
			SignalingCount: period.SignalingCount,
			TotalBlocks:    period.TotalBlocks,
			Pct:            roundBIP110Pct(period.Pct),
		})
	}
	return status
}

type bip110BlockchainInfo struct {
	Chain         string `json:"chain"`
	Blocks        int64  `json:"blocks"`
	BestBlockHash string `json:"bestblockhash"`
	Chainwork     string `json:"chainwork"`
}

type bip110NetworkInfo struct {
	Subversion string `json:"subversion"`
}

type bip110DeploymentInfo struct {
	Deployments map[string]json.RawMessage `json:"deployments"`
}

type bip110RPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type bip110RPCResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcErrorDetail `json:"error"`
}

type bip110BlockHeader struct {
	Version int64 `json:"version"`
}

func (s *bip110MonitorService) fetchInternal(ctx context.Context, public bip110SourceStatus) bip110SourceStatus {
	status := bip110SourceStatus{Source: "active_bitcoind"}
	cfg, source, err := s.loadRPCConfig(ctx)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Source = source

	var chainInfo bip110BlockchainInfo
	if err := s.rpcCall(ctx, cfg, "getblockchaininfo", nil, &chainInfo); err != nil {
		status.Error = err.Error()
		return status
	}
	if chainInfo.Chain != "main" || chainInfo.Blocks <= 0 {
		status.Error = "active bitcoind is not on a usable mainnet tip"
		return status
	}
	status.Tip = chainInfo.Blocks
	status.BestBlockHash = chainInfo.BestBlockHash
	status.Chainwork = chainInfo.Chainwork
	var networkInfo bip110NetworkInfo
	if err := s.rpcCall(ctx, cfg, "getnetworkinfo", nil, &networkInfo); err == nil {
		status.Subversion = networkInfo.Subversion
	}
	var deploymentInfo bip110DeploymentInfo
	if err := s.rpcCall(ctx, cfg, "getdeploymentinfo", nil, &deploymentInfo); err == nil {
		_, configured := deploymentInfo.Deployments["reduced_data"]
		status.EnforcesBIP110 = &configured
	} else if strings.HasPrefix(status.Subversion, "/Satoshi:") {
		// Unmodified Bitcoin Core does not include the BIP 110 deployment. Keep
		// this fallback for older Core releases that lack getdeploymentinfo.
		configured := false
		status.EnforcesBIP110 = &configured
	}

	periodStart := (chainInfo.Blocks / bip110PeriodLength) * bip110PeriodLength
	periodEnd := periodStart + bip110PeriodLength - 1
	periodNum := chainInfo.Blocks / bip110PeriodLength
	sampledTip := chainInfo.Blocks
	if public.Available && public.PeriodStart <= chainInfo.Blocks && public.PeriodEnd >= public.PeriodStart {
		periodStart = public.PeriodStart
		periodEnd = public.PeriodEnd
		periodNum = public.PeriodNum
		if public.Tip <= chainInfo.Blocks {
			sampledTip = public.Tip
		}
	}
	if sampledTip < periodStart {
		status.Error = "active bitcoind tip is behind the comparison period"
		return status
	}

	heights := make([]int64, 0, sampledTip-periodStart+1)
	for height := periodStart; height <= sampledTip; height++ {
		heights = append(heights, height)
	}
	signaling, err := s.signalingCount(ctx, cfg, source, periodStart, periodEnd, heights)
	if err != nil {
		status.Error = err.Error()
		return status
	}

	status.Available = true
	status.SampledTip = sampledTip
	status.PeriodNum = periodNum
	status.PeriodStart = periodStart
	status.PeriodEnd = periodEnd
	status.TotalBlocks = len(heights)
	status.SignalingCount = signaling
	status.Pct = roundBIP110Pct(float64(signaling) * 100 / float64(len(heights)))
	status.Synced = true
	status.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	return status
}

func (s *bip110MonitorService) signalingCount(
	ctx context.Context,
	cfg bitcoinRPCConfig,
	source string,
	periodStart, periodEnd int64,
	heights []int64,
) (int, error) {
	key := fmt.Sprintf("%s|%s|%d|%d", source, cfg.Host, periodStart, periodEnd)
	s.sampleMu.Lock()
	samples := make(map[int64]bip110BlockSample, len(s.samples))
	if s.sampleKey == key {
		for height, sample := range s.samples {
			samples[height] = sample
		}
	}
	s.sampleMu.Unlock()

	var verifyHeight int64 = -1
	for _, height := range heights {
		if _, ok := samples[height]; ok && height > verifyHeight {
			verifyHeight = height
		}
	}
	if verifyHeight >= 0 {
		hashes, err := s.blockHashes(ctx, cfg, []int64{verifyHeight})
		if err != nil {
			return 0, err
		}
		if samples[verifyHeight].Hash != hashes[verifyHeight] {
			samples = make(map[int64]bip110BlockSample)
		}
	}

	missing := make([]int64, 0, len(heights))
	for _, height := range heights {
		if _, ok := samples[height]; !ok {
			missing = append(missing, height)
		}
	}
	if len(missing) > 0 {
		hashes, err := s.blockHashes(ctx, cfg, missing)
		if err != nil {
			return 0, err
		}
		versions, err := s.blockVersions(ctx, cfg, missing, hashes)
		if err != nil {
			return 0, err
		}
		for _, height := range missing {
			samples[height] = bip110BlockSample{Hash: hashes[height], Version: versions[height]}
		}
	}

	signaling := 0
	for _, height := range heights {
		sample, ok := samples[height]
		if !ok {
			return 0, fmt.Errorf("missing cached block sample at height %d", height)
		}
		if bip110VersionSignals(sample.Version) {
			signaling++
		}
	}
	s.sampleMu.Lock()
	s.sampleKey = key
	s.samples = samples
	s.sampleMu.Unlock()
	return signaling, nil
}

func (s *bip110MonitorService) activeRPCConfig(ctx context.Context) (bitcoinRPCConfig, string, error) {
	if s.server == nil {
		return bitcoinRPCConfig{}, "active_bitcoind", errors.New("server unavailable")
	}
	if readBitcoinSource() == "local" {
		cfg, err := readBitcoinLocalRPCConfig(ctx)
		return cfg, "local_bitcoind", err
	}
	remoteCfg := resolveBitcoinRemoteRPCConfig(s.server.cfg)
	if remoteCfg.User == "" || remoteCfg.Pass == "" {
		return bitcoinRPCConfig{}, "remote_bitcoind", errors.New("remote RPC credentials missing")
	}
	return remoteCfg, "remote_bitcoind", nil
}

func (s *bip110MonitorService) rpcCall(ctx context.Context, cfg bitcoinRPCConfig, method string, params []any, out any) error {
	request := bip110RPCRequest{JSONRPC: "1.0", ID: 1, Method: method, Params: params}
	var responses []bip110RPCResponse
	if err := s.rpcPost(ctx, cfg, request, &responses, false); err != nil {
		return err
	}
	if len(responses) != 1 {
		return fmt.Errorf("unexpected %s RPC response", method)
	}
	if responses[0].Error != nil {
		return fmt.Errorf("%s RPC error: %s", method, responses[0].Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(responses[0].Result, out)
}

func (s *bip110MonitorService) rpcBatch(ctx context.Context, cfg bitcoinRPCConfig, requests []bip110RPCRequest) ([]bip110RPCResponse, error) {
	var responses []bip110RPCResponse
	if err := s.rpcPost(ctx, cfg, requests, &responses, true); err != nil {
		return nil, err
	}
	return responses, nil
}

func (s *bip110MonitorService) rpcPost(ctx context.Context, cfg bitcoinRPCConfig, payload any, responses *[]bip110RPCResponse, batch bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	candidates := []string{cfg.Host}
	if !strings.HasPrefix(cfg.Host, "http://") && !strings.HasPrefix(cfg.Host, "https://") {
		candidates = []string{"http://" + cfg.Host, "https://" + cfg.Host}
	}
	var lastErr error
	for idx, endpoint := range candidates {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if reqErr != nil {
			return reqErr
		}
		req.SetBasicAuth(cfg.User, cfg.Pass)
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := s.client.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, bip110HTTPResponseLimit))
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = rpcStatusError{statusCode: resp.StatusCode, message: parseRPCError(responseBody)}
			if idx == 0 && len(candidates) > 1 && resp.StatusCode >= 500 {
				continue
			}
			return lastErr
		}
		if batch {
			return json.Unmarshal(responseBody, responses)
		}
		var single bip110RPCResponse
		if err := json.Unmarshal(responseBody, &single); err != nil {
			return err
		}
		*responses = []bip110RPCResponse{single}
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("bitcoin RPC unavailable")
	}
	return lastErr
}

func (s *bip110MonitorService) blockHashes(ctx context.Context, cfg bitcoinRPCConfig, heights []int64) (map[int64]string, error) {
	requests := make([]bip110RPCRequest, 0, len(heights))
	for _, height := range heights {
		requests = append(requests, bip110RPCRequest{JSONRPC: "1.0", ID: height, Method: "getblockhash", Params: []any{height}})
	}
	responses, err := s.rpcBatch(ctx, cfg, requests)
	if err != nil {
		return nil, err
	}
	hashes := make(map[int64]string, len(responses))
	for _, response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("getblockhash %d: %s", response.ID, response.Error.Message)
		}
		var hash string
		if err := json.Unmarshal(response.Result, &hash); err != nil || hash == "" {
			return nil, fmt.Errorf("invalid block hash at height %d", response.ID)
		}
		hashes[response.ID] = hash
	}
	if len(hashes) != len(heights) {
		return nil, errors.New("incomplete getblockhash batch response")
	}
	return hashes, nil
}

func (s *bip110MonitorService) blockVersions(ctx context.Context, cfg bitcoinRPCConfig, heights []int64, hashes map[int64]string) (map[int64]int64, error) {
	requests := make([]bip110RPCRequest, 0, len(heights))
	for _, height := range heights {
		hash := hashes[height]
		requests = append(requests, bip110RPCRequest{JSONRPC: "1.0", ID: height, Method: "getblockheader", Params: []any{hash, true}})
	}
	responses, err := s.rpcBatch(ctx, cfg, requests)
	if err != nil {
		return nil, err
	}
	versions := make(map[int64]int64, len(responses))
	for _, response := range responses {
		if response.Error != nil {
			return nil, fmt.Errorf("getblockheader %d: %s", response.ID, response.Error.Message)
		}
		var header bip110BlockHeader
		if err := json.Unmarshal(response.Result, &header); err != nil {
			return nil, fmt.Errorf("invalid block header at height %d", response.ID)
		}
		versions[response.ID] = header.Version
	}
	if len(versions) != len(heights) {
		return nil, errors.New("incomplete getblockheader batch response")
	}
	return versions, nil
}

func bip110VersionSignals(version int64) bool {
	value := uint32(version)
	return value&0xe0000000 == 0x20000000 && value&(uint32(1)<<bip110SignalBit) != 0
}

func compareBIP110Sources(internal, public bip110SourceStatus) bip110Comparison {
	comparison := bip110Comparison{Status: "unavailable"}
	if !internal.Available || !public.Available {
		return comparison
	}
	comparison.SamePeriod = internal.PeriodStart == public.PeriodStart && internal.PeriodEnd == public.PeriodEnd
	comparison.TipDelta = internal.Tip - public.Tip
	comparison.Comparable = comparison.SamePeriod && internal.SampledTip == public.Tip && internal.TotalBlocks == public.TotalBlocks
	if !comparison.Comparable {
		comparison.Status = "tip_mismatch"
		return comparison
	}
	comparison.SignalingCountDelta = internal.SignalingCount - public.SignalingCount
	comparison.PctDelta = roundBIP110Pct(internal.Pct - public.Pct)
	comparison.Matches = comparison.SignalingCountDelta == 0
	if comparison.Matches {
		comparison.Status = "matched"
	} else {
		comparison.Status = "signal_mismatch"
	}
	return comparison
}

func bip110EffectiveMilestones(tip int64, internal, public bip110SourceStatus) (int64, int64) {
	periods := append([]bip110Period(nil), public.RecentPeriods...)
	periods = append(periods, internal.RecentPeriods...)
	for _, period := range periods {
		if period.SignalingCount >= bip110ThresholdCount && period.EndBlock < tip {
			lockIn := period.EndBlock + 1
			return lockIn, lockIn + bip110PeriodLength
		}
	}
	return bip110ForcedLockInHeight, bip110ForcedActivationHeight
}

func bip110Phase(tip, lockInHeight, activationHeight int64) string {
	switch {
	case tip <= 0:
		return "unknown"
	case tip < lockInHeight && (tip < bip110MandatoryStartHeight || lockInHeight < bip110ForcedLockInHeight):
		return "voluntary_signaling"
	case tip < lockInHeight:
		return "mandatory_signaling"
	case tip < activationHeight:
		return "locked_in"
	case tip < activationHeight+bip110ActiveDuration:
		return "active_window"
	default:
		return "scheduled_window_complete"
	}
}

func bip110RiskLevel(tip int64, internal, public bip110SourceStatus, comparison bip110Comparison, phase string) string {
	if !internal.Available || !public.Available {
		return "unknown"
	}
	if comparison.Status == "signal_mismatch" || comparison.Status == "tip_mismatch" || !public.Synced {
		return "watch"
	}
	// Signaling progress describes the proposal, not automatically the local
	// operator's exposure. A standard Bitcoin Core backend does not enforce the
	// reduced_data deployment, so low miner signaling is not a high-risk state
	// for that backend while the two monitoring sources still agree.
	if internal.EnforcesBIP110 != nil && !*internal.EnforcesBIP110 {
		return "low"
	}
	pct := internal.Pct
	if phase == "mandatory_signaling" && pct < bip110ThresholdPct {
		return "high"
	}
	if phase == "voluntary_signaling" && tip < bip110MandatoryStartHeight && bip110MandatoryStartHeight-tip <= 2*bip110PeriodLength && pct < bip110ThresholdPct {
		return "elevated"
	}
	return "normal"
}

func roundBIP110Pct(value float64) float64 {
	return math.Round(value*100) / 100
}
