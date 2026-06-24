package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	chanHealEnabledEnv      = "LND_CHAN_HEAL_ENABLED"
	chanHealIntervalEnv     = "LND_CHAN_HEAL_INTERVAL_SEC"
	chanHealDefaultInterval = 300 * time.Second
	chanHealConnectTimeout  = 8 * time.Second
	chanHealMaxReconnects   = 5
)

type chanStatusHealPayload struct {
	Enabled                bool                      `json:"enabled"`
	Status                 string                    `json:"status"`
	IntervalSec            int                       `json:"interval_sec"`
	LastAttemptAt          string                    `json:"last_attempt_at,omitempty"`
	LastOkAt               string                    `json:"last_ok_at,omitempty"`
	LastError              string                    `json:"last_error,omitempty"`
	LastErrorAt            string                    `json:"last_error_at,omitempty"`
	LastUpdated            int                       `json:"last_updated,omitempty"`
	LastReconnectAttempted int                       `json:"last_reconnect_attempted,omitempty"`
	LastReconnected        int                       `json:"last_reconnected,omitempty"`
	LastReconnectFailed    int                       `json:"last_reconnect_failed,omitempty"`
	LastReconnectDetails   []chanHealReconnectDetail `json:"last_reconnect_details,omitempty"`
}

type chanHealReconnectDetail struct {
	Alias          string                           `json:"alias,omitempty"`
	Pubkey         string                           `json:"pubkey,omitempty"`
	PubkeyShort    string                           `json:"pubkey_short,omitempty"`
	ChannelPoints  []string                         `json:"channel_points,omitempty"`
	Status         string                           `json:"status,omitempty"`
	Socket         string                           `json:"socket,omitempty"`
	Sockets        []string                         `json:"sockets,omitempty"`
	SocketAttempts []chanHealReconnectSocketAttempt `json:"socket_attempts,omitempty"`
	ErrorSummary   string                           `json:"error_summary,omitempty"`
	RawError       string                           `json:"raw_error,omitempty"`
}

type chanHealReconnectSocketAttempt struct {
	Socket       string `json:"socket,omitempty"`
	Network      string `json:"network,omitempty"`
	Status       string `json:"status,omitempty"`
	ErrorSummary string `json:"error_summary,omitempty"`
	RawError     string `json:"raw_error,omitempty"`
}

type chanStatusLND interface {
	ListChannels(ctx context.Context) ([]lndclient.ChannelInfo, error)
	ListPeers(ctx context.Context) ([]lndclient.PeerInfo, error)
	GetNodeDetails(ctx context.Context, pubkey string) (lndclient.NodeDetails, error)
	ConnectPeerWithTimeout(ctx context.Context, pubkey string, host string, perm bool, timeoutSec uint64) error
	UpdateChanStatus(ctx context.Context, channelPoint string, enable bool) error
}

type chanHealRunStats struct {
	updated            int
	reconnectAttempted int
	reconnected        int
	reconnectFailed    int
	reconnectDetails   []chanHealReconnectDetail
}

type ChanStatusHealer struct {
	lnd    chanStatusLND
	logger *log.Logger

	mu                     sync.Mutex
	enabled                bool
	interval               time.Duration
	lastAttempt            time.Time
	lastOK                 time.Time
	lastError              string
	lastErrorAt            time.Time
	lastUpdated            int
	lastReconnectAttempted int
	lastReconnected        int
	lastReconnectFailed    int
	lastReconnectDetails   []chanHealReconnectDetail
	inFlight               bool
	started                bool
	stop                   chan struct{}
	wake                   chan struct{}
	intervalUpdated        chan struct{}
}

func NewChanStatusHealer(lnd *lndclient.Client, logger *log.Logger) *ChanStatusHealer {
	enabled := readChanHealEnabled()
	interval := readChanHealInterval()
	return &ChanStatusHealer{
		lnd:      lnd,
		logger:   logger,
		enabled:  enabled,
		interval: interval,
	}
}

func (c *ChanStatusHealer) Start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.stop = make(chan struct{})
	c.wake = make(chan struct{}, 1)
	c.intervalUpdated = make(chan struct{}, 1)
	if c.interval <= 0 {
		c.interval = chanHealDefaultInterval
	}
	enabled := c.enabled
	c.mu.Unlock()

	go c.run()
	if enabled {
		c.trigger()
	}
}

func (c *ChanStatusHealer) Stop() {
	c.mu.Lock()
	if !c.started || c.stop == nil {
		c.mu.Unlock()
		return
	}
	close(c.stop)
	c.stop = nil
	c.started = false
	c.mu.Unlock()
}

func (c *ChanStatusHealer) SetEnabled(enabled bool) error {
	if err := storeChanHealEnabled(enabled); err != nil {
		return err
	}
	c.mu.Lock()
	c.enabled = enabled
	c.mu.Unlock()
	if enabled {
		c.trigger()
	}
	return nil
}

func (c *ChanStatusHealer) SetInterval(seconds int) error {
	if seconds <= 0 {
		return fmt.Errorf("interval_sec must be positive")
	}
	if err := storeChanHealInterval(seconds); err != nil {
		return err
	}
	c.mu.Lock()
	c.interval = time.Duration(seconds) * time.Second
	intervalUpdated := c.intervalUpdated
	c.mu.Unlock()

	if intervalUpdated != nil {
		select {
		case intervalUpdated <- struct{}{}:
		default:
		}
	}
	return nil
}

func (c *ChanStatusHealer) Snapshot() chanStatusHealPayload {
	c.mu.Lock()
	enabled := c.enabled
	interval := c.interval
	lastAttempt := c.lastAttempt
	lastOK := c.lastOK
	lastError := c.lastError
	lastErrorAt := c.lastErrorAt
	lastUpdated := c.lastUpdated
	lastReconnectAttempted := c.lastReconnectAttempted
	lastReconnected := c.lastReconnected
	lastReconnectFailed := c.lastReconnectFailed
	lastReconnectDetails := append([]chanHealReconnectDetail{}, c.lastReconnectDetails...)
	c.mu.Unlock()

	if interval <= 0 {
		interval = chanHealDefaultInterval
	}

	status := "disabled"
	if enabled {
		status = "checking"
		if lastError != "" {
			status = "warn"
		}
		if !lastOK.IsZero() {
			status = "ok"
			if lastError != "" && lastErrorAt.After(lastOK) {
				status = "warn"
			}
			if time.Since(lastOK) > interval*2 {
				status = "warn"
			}
			if lastError == "" && lastReconnectFailed > 0 {
				status = "unreachable"
			}
		}
	}

	payload := chanStatusHealPayload{
		Enabled:                enabled,
		Status:                 status,
		IntervalSec:            int(interval.Seconds()),
		LastUpdated:            lastUpdated,
		LastReconnectAttempted: lastReconnectAttempted,
		LastReconnected:        lastReconnected,
		LastReconnectFailed:    lastReconnectFailed,
		LastReconnectDetails:   lastReconnectDetails,
	}
	if !lastAttempt.IsZero() {
		payload.LastAttemptAt = lastAttempt.UTC().Format(time.RFC3339)
	}
	if !lastOK.IsZero() {
		payload.LastOkAt = lastOK.UTC().Format(time.RFC3339)
	}
	if lastError != "" {
		payload.LastError = lastError
	}
	if !lastErrorAt.IsZero() {
		payload.LastErrorAt = lastErrorAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func (c *ChanStatusHealer) trigger() {
	c.mu.Lock()
	wake := c.wake
	c.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (c *ChanStatusHealer) currentInterval() time.Duration {
	c.mu.Lock()
	interval := c.interval
	c.mu.Unlock()
	if interval <= 0 {
		interval = chanHealDefaultInterval
	}
	return interval
}

func (c *ChanStatusHealer) run() {
	timer := time.NewTimer(c.currentInterval())
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			c.tick()
			timer.Reset(c.currentInterval())
		case <-c.wake:
			c.tick()
		case <-c.intervalUpdated:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.currentInterval())
		case <-c.stop:
			return
		}
	}
}

func (c *ChanStatusHealer) tick() {
	c.mu.Lock()
	if !c.enabled || c.inFlight {
		c.mu.Unlock()
		return
	}
	c.inFlight = true
	c.lastAttempt = time.Now().UTC()
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.inFlight = false
		c.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
	channels, err := c.lnd.ListChannels(ctx)
	cancel()
	if err != nil {
		c.recordFailure(err, chanHealRunStats{})
		return
	}

	if len(channels) == 0 {
		c.recordSuccess(chanHealRunStats{})
		return
	}

	stats := chanHealRunStats{}
	var lastErr error
	if hasInactiveReconnectCandidates(channels) {
		peersCtx, peersCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
		peers, err := c.lnd.ListPeers(peersCtx)
		peersCancel()
		if err != nil {
			lastErr = err
			if c.logger != nil {
				c.logger.Printf("chan-heal: failed to list peers for reconnect: %v", err)
			}
		} else {
			connected := chanHealConnectedPeerSet(peers)
			attempted := map[string]struct{}{}
			for _, ch := range channels {
				if stats.reconnectAttempted >= chanHealMaxReconnects {
					break
				}
				if ch.Active {
					continue
				}
				pubkey := normalizeChanHealPubkey(ch.RemotePubkey)
				if pubkey == "" {
					continue
				}
				if _, ok := connected[pubkey]; ok {
					continue
				}
				if _, ok := attempted[pubkey]; ok {
					continue
				}
				attempted[pubkey] = struct{}{}
				stats.reconnectAttempted++
				detail, err := c.reconnectPeer(pubkey)
				detail = mergeChanHealReconnectChannelDetail(detail, ch, channels)
				if err != nil {
					stats.reconnectFailed++
					stats.reconnectDetails = append(stats.reconnectDetails, detail)
					if c.logger != nil {
						c.logger.Printf("chan-heal: peer %s still unreachable after reconnect attempt: %v", shortIdentifier(pubkey), err)
					}
					continue
				}
				stats.reconnected++
				stats.reconnectDetails = append(stats.reconnectDetails, detail)
				connected[pubkey] = struct{}{}
			}
		}
	}

	if stats.reconnected > 0 {
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
		refreshed, err := c.lnd.ListChannels(refreshCtx)
		refreshCancel()
		if err != nil {
			lastErr = err
			if c.logger != nil {
				c.logger.Printf("chan-heal: failed to refresh channels after reconnect: %v", err)
			}
		} else {
			channels = refreshed
			stats.reconnectDetails = annotateChanHealReconnectDetails(stats.reconnectDetails, channels)
		}
	}

	for _, ch := range channels {
		if !ch.Active {
			continue
		}
		if !isChanLocallyDisabled(ch) {
			continue
		}
		if strings.TrimSpace(ch.ChannelPoint) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), lndRPCTimeout)
		err := c.lnd.UpdateChanStatus(ctx, ch.ChannelPoint, true)
		cancel()
		if err != nil {
			lastErr = err
			if c.logger != nil {
				c.logger.Printf("chan-heal: failed to enable %s: %v", ch.ChannelPoint, err)
			}
			continue
		}
		stats.updated++
	}

	if lastErr != nil {
		c.recordFailure(lastErr, stats)
		return
	}
	c.recordSuccess(stats)
}

func (c *ChanStatusHealer) reconnectPeer(pubkey string) (chanHealReconnectDetail, error) {
	detail := chanHealReconnectDetail{
		Pubkey:      normalizeChanHealPubkey(pubkey),
		PubkeyShort: shortIdentifier(pubkey),
	}
	detailsCtx, detailsCancel := context.WithTimeout(context.Background(), lndRPCTimeout)
	details, err := c.lnd.GetNodeDetails(detailsCtx, pubkey)
	detailsCancel()
	if err != nil {
		detail.Status = "lookup_failed"
		detail.ErrorSummary = chanHealReconnectErrorSummary(err)
		detail.RawError = strings.TrimSpace(err.Error())
		return detail, err
	}
	if alias := strings.TrimSpace(details.Alias); alias != "" {
		detail.Alias = alias
	}

	sockets := chanHealNodeSockets(details.Addresses)
	detail.Sockets = append([]string{}, sockets...)
	if len(sockets) == 0 {
		err := fmt.Errorf("no announced socket for %s", shortIdentifier(pubkey))
		detail.Status = "no_announced_socket"
		detail.ErrorSummary = chanHealReconnectErrorSummary(err)
		detail.RawError = err.Error()
		return detail, err
	}

	var lastErr error
	for _, socket := range sockets {
		detail.Socket = socket
		connectCtx, connectCancel := context.WithTimeout(context.Background(), chanHealConnectTimeout)
		err := c.lnd.ConnectPeerWithTimeout(connectCtx, pubkey, socket, false, uint64(chanHealConnectTimeout/time.Second))
		connectCancel()
		if err == nil {
			detail.SocketAttempts = append(detail.SocketAttempts, chanHealReconnectSocketAttempt{
				Socket:  socket,
				Network: chanHealSocketNetwork(socket),
				Status:  "connected",
			})
			detail.Status = "connected"
			detail.ErrorSummary = ""
			detail.RawError = ""
			return detail, nil
		}
		if isAlreadyConnected(err) {
			detail.SocketAttempts = append(detail.SocketAttempts, chanHealReconnectSocketAttempt{
				Socket:  socket,
				Network: chanHealSocketNetwork(socket),
				Status:  "already_connected",
			})
			detail.Status = "already_connected"
			detail.ErrorSummary = ""
			detail.RawError = ""
			return detail, nil
		}
		lastErr = fmt.Errorf("connect via %s failed: %w", socket, err)
		detail.SocketAttempts = append(detail.SocketAttempts, chanHealReconnectSocketAttempt{
			Socket:       socket,
			Network:      chanHealSocketNetwork(socket),
			Status:       classifyChanHealReconnectError(lastErr),
			ErrorSummary: chanHealReconnectErrorSummary(lastErr),
			RawError:     strings.TrimSpace(lastErr.Error()),
		})
	}
	if lastErr != nil {
		detail.Status = classifyChanHealReconnectError(lastErr)
		detail.ErrorSummary = chanHealReconnectErrorSummary(lastErr)
		detail.RawError = strings.TrimSpace(lastErr.Error())
		return detail, lastErr
	}
	err = fmt.Errorf("connect failed for %s", shortIdentifier(pubkey))
	detail.Status = "connect_failed"
	detail.ErrorSummary = chanHealReconnectErrorSummary(err)
	detail.RawError = err.Error()
	return detail, err
}

func mergeChanHealReconnectChannelDetail(detail chanHealReconnectDetail, ch lndclient.ChannelInfo, channels []lndclient.ChannelInfo) chanHealReconnectDetail {
	pubkey := normalizeChanHealPubkey(detail.Pubkey)
	if pubkey == "" {
		pubkey = normalizeChanHealPubkey(ch.RemotePubkey)
		detail.Pubkey = pubkey
		detail.PubkeyShort = shortIdentifier(pubkey)
	}
	if strings.TrimSpace(detail.Alias) == "" {
		detail.Alias = strings.TrimSpace(ch.PeerAlias)
	}
	seen := map[string]struct{}{}
	for _, item := range channels {
		if normalizeChanHealPubkey(item.RemotePubkey) != pubkey {
			continue
		}
		if alias := strings.TrimSpace(item.PeerAlias); detail.Alias == "" && alias != "" {
			detail.Alias = alias
		}
		point := strings.TrimSpace(item.ChannelPoint)
		if point == "" {
			continue
		}
		if _, ok := seen[point]; ok {
			continue
		}
		seen[point] = struct{}{}
		detail.ChannelPoints = append(detail.ChannelPoints, point)
	}
	return detail
}

func annotateChanHealReconnectDetails(details []chanHealReconnectDetail, channels []lndclient.ChannelInfo) []chanHealReconnectDetail {
	if len(details) == 0 {
		return details
	}
	activeByPubkey := map[string]int{}
	inactiveByPubkey := map[string]int{}
	for _, ch := range channels {
		pubkey := normalizeChanHealPubkey(ch.RemotePubkey)
		if pubkey == "" {
			continue
		}
		if ch.Active {
			activeByPubkey[pubkey]++
			continue
		}
		inactiveByPubkey[pubkey]++
	}
	out := append([]chanHealReconnectDetail{}, details...)
	for i := range out {
		pubkey := normalizeChanHealPubkey(out[i].Pubkey)
		if pubkey == "" {
			continue
		}
		switch out[i].Status {
		case "connected", "already_connected":
			if inactiveByPubkey[pubkey] > 0 && activeByPubkey[pubkey] == 0 {
				out[i].Status = "connected_channel_inactive"
				out[i].ErrorSummary = "peer connected, channel still inactive"
			}
		}
	}
	return out
}

func classifyChanHealReconnectError(err error) string {
	msg := strings.ToLower(strings.TrimSpace(fmt.Sprint(err)))
	switch {
	case msg == "":
		return "connect_failed"
	case strings.Contains(msg, "deadline") || strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
		return "connect_timeout"
	case strings.Contains(msg, "no announced socket"):
		return "no_announced_socket"
	case strings.Contains(msg, "node not found") || strings.Contains(msg, "not found"):
		return "lookup_failed"
	case strings.Contains(msg, "socks") || strings.Contains(msg, "unreachable") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "connect:"):
		return "still_unreachable"
	default:
		return "connect_failed"
	}
}

func chanHealReconnectErrorSummary(err error) string {
	status := classifyChanHealReconnectError(err)
	switch status {
	case "connect_timeout":
		return "connection attempt timed out; peer may be offline or unreachable"
	case "no_announced_socket":
		return "peer has no announced socket to reconnect"
	case "lookup_failed":
		return "node details lookup failed"
	case "still_unreachable":
		return "peer remains unreachable after reconnect attempt"
	default:
		return "peer reconnect attempt failed"
	}
}

func (c *ChanStatusHealer) recordFailure(err error, stats chanHealRunStats) {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "channel status heal failed"
	}

	c.mu.Lock()
	c.lastError = msg
	c.lastErrorAt = time.Now().UTC()
	c.lastUpdated = stats.updated
	c.lastReconnectAttempted = stats.reconnectAttempted
	c.lastReconnected = stats.reconnected
	c.lastReconnectFailed = stats.reconnectFailed
	c.lastReconnectDetails = append([]chanHealReconnectDetail{}, stats.reconnectDetails...)
	c.mu.Unlock()

	if c.logger != nil {
		c.logger.Printf("chan-heal: %s", msg)
	}
}

func (c *ChanStatusHealer) recordSuccess(stats chanHealRunStats) {
	c.mu.Lock()
	hadError := c.lastError != ""
	c.lastOK = time.Now().UTC()
	c.lastError = ""
	c.lastErrorAt = time.Time{}
	c.lastUpdated = stats.updated
	c.lastReconnectAttempted = stats.reconnectAttempted
	c.lastReconnected = stats.reconnected
	c.lastReconnectFailed = stats.reconnectFailed
	c.lastReconnectDetails = append([]chanHealReconnectDetail{}, stats.reconnectDetails...)
	c.mu.Unlock()

	if hadError && c.logger != nil {
		c.logger.Printf("chan-heal: recovered")
	}
	if c.logger != nil && stats.updated > 0 {
		c.logger.Printf("chan-heal: enabled %d channel(s)", stats.updated)
	}
	if c.logger != nil && stats.reconnectAttempted > 0 {
		c.logger.Printf("chan-heal: reconnect attempted=%d connected=%d failed=%d", stats.reconnectAttempted, stats.reconnected, stats.reconnectFailed)
	}
}

func hasInactiveReconnectCandidates(channels []lndclient.ChannelInfo) bool {
	for _, ch := range channels {
		if ch.Active {
			continue
		}
		if normalizeChanHealPubkey(ch.RemotePubkey) != "" {
			return true
		}
	}
	return false
}

func chanHealConnectedPeerSet(peers []lndclient.PeerInfo) map[string]struct{} {
	out := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		pubkey := normalizeChanHealPubkey(peer.PubKey)
		if pubkey == "" {
			continue
		}
		out[pubkey] = struct{}{}
	}
	return out
}

func normalizeChanHealPubkey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func chanHealNodeSockets(addresses []lndclient.NodeAddress) []string {
	clearnet := make([]string, 0, len(addresses))
	onion := make([]string, 0, len(addresses))
	seen := map[string]struct{}{}
	for _, item := range addresses {
		socket := normalizeSocket(item.Addr)
		if socket == "" || !socketHasPort(socket) {
			continue
		}
		key := strings.ToLower(socket)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if isOnionSocket(socket) {
			onion = append(onion, socket)
			continue
		}
		clearnet = append(clearnet, socket)
	}
	return append(clearnet, onion...)
}

func chanHealSocketNetwork(socket string) string {
	if isOnionSocket(socket) {
		return "tor"
	}
	return "clearnet"
}

func isChanLocallyDisabled(ch lndclient.ChannelInfo) bool {
	if ch.LocalDisabled {
		return true
	}
	return isLocalChanDisabled(ch.ChanStatusFlags)
}

func isLocalChanDisabled(flags string) bool {
	trimmed := strings.TrimSpace(flags)
	if trimmed == "" {
		return false
	}
	normalized := strings.ToLower(trimmed)
	split := func(r rune) bool {
		switch r {
		case '|', ',', ';', ' ':
			return true
		default:
			return false
		}
	}
	tokens := strings.FieldsFunc(normalized, split)
	if len(tokens) == 0 {
		tokens = []string{normalized}
	}
	for _, token := range tokens {
		tok := strings.TrimSpace(token)
		if tok == "" {
			continue
		}
		if strings.Contains(tok, "localchandisabled") || strings.Contains(tok, "local_chan_disabled") {
			return true
		}
		if strings.Contains(tok, "disabled") && !strings.Contains(tok, "remote") {
			if strings.Contains(tok, "local") || strings.Contains(tok, "chanstatusdisabled") || tok == "disabled" {
				return true
			}
		}
	}
	return false
}

func readChanHealEnabled() bool {
	if val := strings.TrimSpace(os.Getenv(chanHealEnabledEnv)); val != "" {
		if parsed, ok := parseEnvBool(val); ok {
			return parsed
		}
	}
	if val, err := readEnvFileValue(secretsPath, chanHealEnabledEnv); err == nil {
		if parsed, ok := parseEnvBool(val); ok {
			return parsed
		}
	}
	return false
}

func storeChanHealEnabled(enabled bool) error {
	if err := ensureSecretsDir(); err != nil {
		return err
	}
	value := "0"
	if enabled {
		value = "1"
	}
	if err := writeEnvFileValue(secretsPath, chanHealEnabledEnv, value); err != nil {
		return err
	}
	_ = os.Setenv(chanHealEnabledEnv, value)
	return nil
}

func readChanHealInterval() time.Duration {
	if val := strings.TrimSpace(os.Getenv(chanHealIntervalEnv)); val != "" {
		if parsed := parseEnvSeconds(val); parsed > 0 {
			return parsed
		}
	}
	if val, err := readEnvFileValue(secretsPath, chanHealIntervalEnv); err == nil {
		if parsed := parseEnvSeconds(val); parsed > 0 {
			return parsed
		}
	}
	return chanHealDefaultInterval
}

func storeChanHealInterval(seconds int) error {
	if err := ensureSecretsDir(); err != nil {
		return err
	}
	if err := writeEnvFileValue(secretsPath, chanHealIntervalEnv, fmt.Sprintf("%d", seconds)); err != nil {
		return err
	}
	_ = os.Setenv(chanHealIntervalEnv, fmt.Sprintf("%d", seconds))
	return nil
}

func (s *Server) handleLNChanHealGet(w http.ResponseWriter, r *http.Request) {
	if s.chanHealer == nil {
		writeJSON(w, http.StatusOK, chanStatusHealPayload{
			Enabled:     false,
			Status:      "disabled",
			IntervalSec: int(chanHealDefaultInterval.Seconds()),
		})
		return
	}
	writeJSON(w, http.StatusOK, s.chanHealer.Snapshot())
}

func (s *Server) handleLNChanHealPost(w http.ResponseWriter, r *http.Request) {
	if s.chanHealer == nil {
		writeError(w, http.StatusServiceUnavailable, "channel healer unavailable")
		return
	}
	var req struct {
		Enabled     *bool `json:"enabled"`
		IntervalSec *int  `json:"interval_sec"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Enabled != nil {
		if err := s.chanHealer.SetEnabled(*req.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if req.IntervalSec != nil {
		if err := s.chanHealer.SetInterval(*req.IntervalSec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.chanHealer.Snapshot())
}
