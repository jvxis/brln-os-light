package server

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
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
	if json.Valid([]byte(trimmed)) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, trimmed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": trimmed})
}

func (s *Server) handleTapdInfo(w http.ResponseWriter, r *http.Request) {
	out, err := s.tapcli(r.Context(), "getinfo")
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdAssets(w http.ResponseWriter, r *http.Request) {
	out, err := s.tapcli(r.Context(), "assets", "balance")
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
	args := []string{"addrs", "new"}
	if assetID != "" {
		args = append(args, "--asset_id", assetID)
	} else {
		args = append(args, "--group_key", groupKey)
	}
	args = append(args, "--amt", strconv.FormatUint(req.Amount, 10))
	out, err := s.tapcli(r.Context(), args...)
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
	args := []string{"universe", "sync", "--universe_host", host}
	if gk := strings.TrimSpace(req.GroupKey); gk != "" {
		args = append(args, "--group_key", gk)
	} else if id := strings.TrimSpace(req.AssetID); id != "" {
		args = append(args, "--asset_id", id)
	}
	out, err := s.tapcli(r.Context(), args...)
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
	args := []string{
		"assets", "mint",
		"--type", "normal",
		"--name", name,
		"--supply", strconv.FormatUint(req.Supply, 10),
	}
	if req.DecimalDisplay > 0 {
		args = append(args, "--decimal_display", strconv.FormatUint(uint64(req.DecimalDisplay), 10))
	}
	// Reissue into an existing group when group_key is given. tapd requires
	// --grouped_asset alongside --group_key ("must set grouped asset to mint into
	// a specific group"). Otherwise start a new reissuable group if requested.
	if gk := strings.TrimSpace(req.GroupKey); gk != "" {
		args = append(args, "--grouped_asset", "--group_key", gk)
	} else if req.Grouped {
		args = append(args, "--new_grouped_asset")
	}
	if meta := strings.TrimSpace(req.Meta); meta != "" {
		args = append(args, "--meta_bytes", meta, "--meta_type", "json")
	}
	out, err := s.tapcli(r.Context(), args...)
	writeTapcli(w, out, err)
}

// handleTapdMintFinalize broadcasts the staged mint batch on-chain. Kept
// separate from staging so the user reviews before the irreversible broadcast.
func (s *Server) handleTapdMintFinalize(w http.ResponseWriter, r *http.Request) {
	out, err := s.tapcli(r.Context(), "assets", "mint", "finalize")
	writeTapcli(w, out, err)
}

func (s *Server) handleTapdSend(w http.ResponseWriter, r *http.Request) {
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
	// A Taproot Assets address already encodes the amount, so `--addr` is enough.
	out, err := s.tapcli(r.Context(), "assets", "send", "--addr", addr)
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

// handleTapdDiscover fetches a universe's public REST catalog
// (https://<host>/v1/taproot-assets/universe/roots) and returns a compact asset
// list so users can browse and then targeted-sync an asset. The tapcli/gRPC
// full-sync does NOT return the whole catalog, but this read endpoint does.
func (s *Server) handleTapdDiscover(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	// Accept a bare hostname[:port] and build the fixed roots path ourselves, to
	// keep the request surface limited.
	if strings.ContainsAny(host, " /\\?#") || strings.Contains(host, "://") {
		writeError(w, http.StatusBadRequest, "invalid host")
		return
	}
	endpoint := "https://" + host + "/v1/taproot-assets/universe/roots"

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("universe returned status %d", resp.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
