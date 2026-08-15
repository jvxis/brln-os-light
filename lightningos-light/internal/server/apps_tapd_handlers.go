package server

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"
)

// HTTP handlers for the Taproot Assets (tapd) app. They are thin wrappers that
// shell out to tapcli inside the container and return its JSON output. The tapd
// JSON shapes are rendered tolerantly on the frontend, so we pass the raw
// output through instead of binding brittle Go structs (validate shapes against
// tapd v0.8 as it stabilizes).

// writeTapcli forwards tapcli output to the client. Valid JSON is passed through
// untouched; anything else is wrapped so the response is always valid JSON.
func writeTapcli(w http.ResponseWriter, out string, err error) {
	trimmed := strings.TrimSpace(out)
	if err != nil {
		msg := trimmed
		if msg == "" {
			msg = err.Error()
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	// The docker CLI may prepend a warning line to stdout (e.g. "WARNING: Error
	// loading config file: ... .docker/config.json: permission denied") on nodes
	// with stale permissions. Strip anything before the first JSON delimiter so
	// the real tapcli JSON still parses.
	clean := extractJSON(trimmed)
	if json.Valid([]byte(clean)) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, clean)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": trimmed})
}

// extractJSON returns the substring starting at the first '{' or '[' so leading
// non-JSON noise (docker CLI warnings) does not break parsing. Returns the input
// unchanged when there is no such delimiter or nothing precedes it.
func extractJSON(s string) string {
	i := strings.IndexAny(s, "{[")
	if i <= 0 {
		return s
	}
	return s[i:]
}

func (s *Server) handleTapdInfo(w http.ResponseWriter, r *http.Request) {
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIGetInfo})
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdAssets(w http.ResponseWriter, r *http.Request) {
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIAssetsBalance})
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AssetID  string `json:"asset_id"`
		GroupKey string `json:"group_key"`
		Amount   uint64 `json:"amount"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	assetID := strings.TrimSpace(req.AssetID)
	groupKey := strings.TrimSpace(req.GroupKey)
	if assetID == "" && groupKey == "" {
		writeError(w, http.StatusBadRequest, "asset_id or group_key is required")
		return
	}
	if req.Amount == 0 {
		writeError(w, http.StatusBadRequest, "amount must be greater than zero")
		return
	}
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{
		Command: appmanifest.TapdCLIAddressNew, AssetID: assetID, GroupKey: groupKey, Amount: req.Amount,
	})
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdUniverseSync(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UniverseHost string `json:"universe_host"`
		GroupKey     string `json:"group_key"`
		AssetID      string `json:"asset_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	host := strings.TrimSpace(req.UniverseHost)
	if host == "" {
		writeError(w, http.StatusBadRequest, "universe_host is required")
		return
	}
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{
		Command: appmanifest.TapdCLIUniverseSync, UniverseHost: host,
		GroupKey: strings.TrimSpace(req.GroupKey), AssetID: strings.TrimSpace(req.AssetID),
	})
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdMint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string `json:"name"`
		Supply         uint64 `json:"supply"`
		DecimalDisplay uint32 `json:"decimal_display"`
		Grouped        bool   `json:"grouped"`
		GroupKey       string `json:"group_key"`
		Meta           string `json:"meta"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Supply == 0 {
		writeError(w, http.StatusBadRequest, "supply must be greater than zero")
		return
	}
	// Reissue into an existing group when group_key is given. tapd requires
	// --grouped_asset alongside --group_key ("must set grouped asset to mint into
	// a specific group"). Otherwise start a new reissuable group if requested.
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{
		Command: appmanifest.TapdCLIMint, Name: name, Supply: req.Supply,
		DecimalDisplay: req.DecimalDisplay, Grouped: req.Grouped,
		GroupKey: strings.TrimSpace(req.GroupKey), Metadata: strings.TrimSpace(req.Meta),
	})
	writeTapcli(w, out, err)
}

// handleTapdMintFinalize broadcasts the staged mint batch on-chain. Kept
// separate from staging so the user reviews before the irreversible broadcast.
// Optional fee_rate (sat/vByte) overrides LND's fee estimate.
func (s *Server) handleTapdMintFinalize(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FeeRate         uint32 `json:"fee_rate"`
		ConfirmPassword string `json:"confirm_password"`
	}
	_ = readJSON(r, &req) // body optional
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIMintFinalize, FeeRate: req.FeeRate})
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr            string `json:"addr"`
		FeeRate         uint32 `json:"fee_rate"`
		ConfirmPassword string `json:"confirm_password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		writeError(w, http.StatusBadRequest, "addr is required")
		return
	}
	if !s.requireLightningFundsReauth(w, r, req.ConfirmPassword) {
		return
	}
	// A Taproot Assets address already encodes the amount, so `--addr` is enough.
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLISend, Address: addr, FeeRate: req.FeeRate})
	writeTapcli(w, out, err)
}

// handleTapdDecodeAddr decodes a Taproot Assets address (asset_id, group_key,
// amount, …) without sending, for a pre-send preview.
func (s *Server) handleTapdDecodeAddr(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	addr := strings.TrimSpace(req.Addr)
	if addr == "" {
		writeError(w, http.StatusBadRequest, "addr is required")
		return
	}
	out, err := s.tapcli(r.Context(), appmanifest.TapdCLIRequest{Command: appmanifest.TapdCLIDecodeAddress, Address: addr})
	writeTapcli(w, out, err)
}

// handleTapdRedeem is the Fase 2 integration point: redeeming asset points for
// sats over Lightning requires the community edge node (litd), which does not
// exist yet. Return 501 so the UI can present it as "coming soon".
func (s *Server) handleTapdRedeem(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Redeem to sats requires the community edge node (Fase 2) and is not available yet")
}

// tapdDiscoverAsset is one entry from a universe's public REST roots catalog.
type tapdDiscoverAsset struct {
	Name      string `json:"name"`
	AssetID   string `json:"asset_id"`
	GroupKey  string `json:"group_key"`
	ProofType string `json:"proof_type"`
	Supply    string `json:"supply"`
}

type universeRootsResp struct {
	UniverseRoots map[string]struct {
		ID struct {
			AssetID   string `json:"asset_id"`
			GroupKey  string `json:"group_key"`
			ProofType string `json:"proof_type"`
		} `json:"id"`
		AssetName string `json:"asset_name"`
		MssmtRoot struct {
			RootSum string `json:"root_sum"`
		} `json:"mssmt_root"`
	} `json:"universe_roots"`
}

const (
	tapdDefaultDiscoveryUniverseHost = "universe.lightning.finance"
	tapdDefaultDiscoveryUniverseURL  = "https://universe.lightning.finance/v1/taproot-assets/universe/roots"
)

func isApprovedTapdDiscoveryUniverse(host string) bool {
	return strings.EqualFold(strings.TrimSpace(host), tapdDefaultDiscoveryUniverseHost)
}

// handleTapdDiscover fetches the public REST catalog from a server-approved
// universe and returns a compact asset list so users can browse and then
// targeted-sync an asset. Arbitrary hosts remain available to the separate
// tapcli/gRPC sync action, which does not make an HTTP request from the manager.
func (s *Server) handleTapdDiscover(w http.ResponseWriter, r *http.Request) {
	requestedHost := strings.TrimSpace(r.URL.Query().Get("host"))
	if requestedHost == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	if !isApprovedTapdDiscoveryUniverse(requestedHost) {
		writeError(w, http.StatusBadRequest, "universe discovery host is not approved")
		return
	}

	// Both the network policy and request URL are server-owned constants. The
	// user-provided value only selects this closed entry and never reaches the
	// resolver, dialer, TLS authority, or HTTP request URL.
	client, err := publicUniverseHTTPClient(r.Context(), tapdDefaultDiscoveryUniverseHost)
	if err != nil {
		writeError(w, http.StatusBadRequest, "universe host is not publicly routable")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tapdDefaultDiscoveryUniverseURL, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "universe request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("universe returned status %d", resp.StatusCode))
		return
	}
	const maxUniverseResponse = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUniverseResponse+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "universe response read failed")
		return
	}
	if len(body) > maxUniverseResponse {
		writeError(w, http.StatusBadGateway, "universe response is too large")
		return
	}
	var parsed universeRootsResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse universe response")
		return
	}
	assets := make([]tapdDiscoverAsset, 0, len(parsed.UniverseRoots))
	for _, root := range parsed.UniverseRoots {
		assets = append(assets, tapdDiscoverAsset{
			Name:      root.AssetName,
			AssetID:   normalizeUniverseID(root.ID.AssetID),
			GroupKey:  normalizeUniverseID(root.ID.GroupKey),
			ProofType: root.ID.ProofType,
			Supply:    root.MssmtRoot.RootSum,
		})
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	total := len(assets)
	const maxAssets = 400
	if len(assets) > maxAssets {
		assets = assets[:maxAssets]
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": assets, "total": total})
}

func publicUniverseHTTPClient(ctx context.Context, universeHost string) (*http.Client, error) {
	host := universeHost
	port := "443"
	if strings.HasPrefix(universeHost, "[") || strings.Count(universeHost, ":") == 1 {
		var err error
		host, port, err = net.SplitHostPort(universeHost)
		if err != nil {
			return nil, err
		}
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("universe host resolution failed")
	}
	public := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		ip := address.IP
		if !isPublicUniverseIP(ip) {
			continue
		}
		public = append(public, ip)
	}
	if len(public) == 0 {
		return nil, errors.New("universe host is not publicly routable")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			requestedHost, requestedPort, err := net.SplitHostPort(address)
			if err != nil || !strings.EqualFold(requestedHost, host) || requestedPort != port {
				return nil, errors.New("universe redirect target is not allowed")
			}
			var lastErr error
			for _, ip := range public {
				connection, dialErr := dialer.DialContext(dialCtx, network, net.JoinHostPort(ip.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				lastErr = dialErr
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   25 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("universe redirects are not allowed")
		},
	}, nil
}

func isPublicUniverseIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	blocked := []string{
		"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"100::/64", "2001:db8::/32",
	}
	for _, rawPrefix := range blocked {
		if netip.MustParsePrefix(rawPrefix).Contains(address) {
			return false
		}
	}
	return true
}

// normalizeUniverseID returns a lowercase hex string for a universe id field,
// which the REST gateway may encode as hex or base64.
func normalizeUniverseID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s)%2 == 0 && isHexString(s) {
		return strings.ToLower(s)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && (len(b) == 32 || len(b) == 33) {
			return hex.EncodeToString(b)
		}
	}
	return s
}

func isHexString(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
