package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/system"
)

const (
	peerswapElementsModeLocal  = "local"
	peerswapElementsModeRemote = "remote"
	peerswapElementsWallet     = "peerswap"
	peerswapRemoteWalletPrefix = "peerswap_"
	peerswapLNDIdentityTimeout = 6 * time.Second
)

var peerswapElementsHTTPClient = &http.Client{Timeout: 8 * time.Second}

type peerswapInstallOptions struct {
	ElementsMode        string `json:"elements_mode"`
	ElementsRPCURL      string `json:"elements_rpc_url"`
	ElementsRPCUser     string `json:"elements_rpc_user"`
	ElementsRPCPassword string `json:"elements_rpc_password"`
	ElementsRPCWallet   string `json:"elements_rpc_wallet"`
}

type peerswapElementsSource struct {
	Mode     string `json:"mode"`
	URL      string `json:"url,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	Wallet   string `json:"wallet,omitempty"`
}

type peerswapElementsSourceRequest struct {
	Mode     string `json:"mode"`
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"`
	Wallet   string `json:"wallet"`
}

type peerswapElementsSourceResponse struct {
	Configured  bool   `json:"configured"`
	Mode        string `json:"mode"`
	URL         string `json:"url,omitempty"`
	User        string `json:"user,omitempty"`
	Wallet      string `json:"wallet,omitempty"`
	LocalReady  bool   `json:"local_ready"`
	LocalStatus string `json:"local_status"`
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
}

type peerswapElementsSourceTestResponse struct {
	OK    bool   `json:"ok"`
	Mode  string `json:"mode"`
	Chain string `json:"chain,omitempty"`
}

type peerswapRemoteEndpoint struct {
	URL  string
	Host string
	Port int
}

func (opts peerswapInstallOptions) sourceRequest() peerswapElementsSourceRequest {
	return peerswapElementsSourceRequest{
		Mode:     opts.ElementsMode,
		URL:      opts.ElementsRPCURL,
		User:     opts.ElementsRPCUser,
		Password: opts.ElementsRPCPassword,
		Wallet:   opts.ElementsRPCWallet,
	}
}

func (s *Server) handlePeerswapElementsSourceGet(w http.ResponseWriter, r *http.Request) {
	paths := peerswapAppPaths()
	source, configured, err := readPeerswapElementsSource(r.Context(), paths)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	localReady, localStatus := peerswapLocalElementsReady(r.Context())
	if !configured {
		source = peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}
	} else if completed, _, err := s.completePeerswapElementsSource(r.Context(), source); err == nil {
		source = completed
	}
	handled, brokerState, statusErr := system.PeerSwapStatusWithBroker(r.Context())
	if !handled {
		writeError(w, http.StatusServiceUnavailable, "PeerSwap requires the privileged broker in enforce mode")
		return
	}
	if statusErr != nil {
		writeError(w, http.StatusInternalServerError, statusErr.Error())
		return
	}
	resp := peerswapElementsSourceResponse{
		Configured:  configured,
		Mode:        source.Mode,
		URL:         source.URL,
		User:        source.User,
		Wallet:      peerswapSourceWallet(source),
		LocalReady:  localReady,
		LocalStatus: localStatus,
		Installed:   brokerState.Installed,
		Running:     brokerState.Status == "running",
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePeerswapElementsSourceTest(w http.ResponseWriter, r *http.Request) {
	source, err := s.peerswapElementsSourceFromRequestOrStored(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	chain, err := testPeerswapElementsSource(r.Context(), source)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, peerswapElementsSourceTestResponse{
		OK:    true,
		Mode:  source.Mode,
		Chain: chain,
	})
}

func (s *Server) handlePeerswapElementsSourcePost(w http.ResponseWriter, r *http.Request) {
	var req peerswapElementsSourceRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	source, err := normalizePeerswapElementsSourceRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := testPeerswapElementsSource(r.Context(), source); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	source, _, err = s.completePeerswapElementsSource(r.Context(), source)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	paths := peerswapAppPaths()
	previous, previousSet, _ := readPeerswapElementsSource(r.Context(), paths)
	handled, brokerState, statusErr := system.PeerSwapStatusWithBroker(r.Context())
	if !handled || statusErr != nil {
		writeError(w, http.StatusServiceUnavailable, "PeerSwap status is unavailable")
		return
	}
	if err := writePeerswapElementsSource(r.Context(), paths, source); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if brokerState.Installed {
		if err := s.reconfigureInstalledPeerswap(r.Context(), paths, source); err != nil {
			if previousSet {
				_ = writePeerswapElementsSource(r.Context(), paths, previous)
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.handlePeerswapElementsSourceGet(w, r)
}

func (s *Server) peerswapElementsSourceFromRequestOrStored(ctx context.Context, r *http.Request) (peerswapElementsSource, error) {
	if r.ContentLength != 0 {
		var req peerswapElementsSourceRequest
		if err := readJSON(r, &req); err != nil {
			return peerswapElementsSource{}, errors.New("invalid json")
		}
		return normalizePeerswapElementsSourceRequest(req)
	}
	paths := peerswapAppPaths()
	source, configured, err := readPeerswapElementsSource(ctx, paths)
	if err != nil {
		return peerswapElementsSource{}, err
	}
	if configured {
		completed, _, err := s.completePeerswapElementsSource(ctx, source)
		return completed, err
	}
	if ok, _ := peerswapLocalElementsReady(ctx); ok {
		return peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}, nil
	}
	return peerswapElementsSource{}, errors.New("Peerswap Elements source is not configured")
}

func (s *Server) preparePeerswapElementsSourceForInstall(ctx context.Context, paths peerswapPaths, opts peerswapInstallOptions) (peerswapElementsSource, error) {
	source, err := s.resolvePeerswapElementsSourceForInstall(ctx, paths, opts)
	if err != nil {
		return peerswapElementsSource{}, err
	}
	if _, err := testPeerswapElementsSource(ctx, source); err != nil {
		return peerswapElementsSource{}, err
	}
	source, _, err = s.completePeerswapElementsSource(ctx, source)
	if err != nil {
		return peerswapElementsSource{}, err
	}
	if err := writePeerswapElementsSource(ctx, paths, source); err != nil {
		return peerswapElementsSource{}, err
	}
	return source, nil
}

func (s *Server) preparePeerswapElementsSourceForStart(ctx context.Context, paths peerswapPaths) (peerswapElementsSource, error) {
	source, configured, err := readPeerswapElementsSource(ctx, paths)
	if err != nil {
		return peerswapElementsSource{}, err
	}
	if !configured {
		if ok, _ := peerswapLocalElementsReady(ctx); ok {
			source = peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}
			if err := writePeerswapElementsSource(ctx, paths, source); err != nil {
				return peerswapElementsSource{}, err
			}
		} else {
			return peerswapElementsSource{}, errors.New("Elements local must be running before starting Peerswap, or configure a remote Elements RPC source")
		}
	}
	completed, changed, err := s.completePeerswapElementsSource(ctx, source)
	if err != nil {
		return peerswapElementsSource{}, err
	}
	source = completed
	if changed {
		if err := writePeerswapElementsSource(ctx, paths, source); err != nil {
			return peerswapElementsSource{}, err
		}
	}
	if _, err := testPeerswapElementsSource(ctx, source); err != nil {
		return peerswapElementsSource{}, err
	}
	return source, nil
}

func (s *Server) resolvePeerswapElementsSourceForInstall(ctx context.Context, paths peerswapPaths, opts peerswapInstallOptions) (peerswapElementsSource, error) {
	req := opts.sourceRequest()
	if strings.TrimSpace(req.Mode) != "" {
		return normalizePeerswapElementsSourceRequest(req)
	}
	if source, configured, err := readPeerswapElementsSource(ctx, paths); err != nil {
		return peerswapElementsSource{}, err
	} else if configured {
		return s.completeConfiguredPeerswapElementsSource(ctx, source)
	}
	if ok, _ := peerswapLocalElementsReady(ctx); ok {
		return peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}, nil
	}
	return peerswapElementsSource{}, errors.New("Elements is required before installing Peerswap. Start local Elements or configure remote Elements RPC")
}

func (s *Server) resolvePeerswapElementsSourceForConfig(ctx context.Context, paths peerswapPaths) (peerswapElementsSource, error) {
	if source, configured, err := readPeerswapElementsSource(ctx, paths); err != nil {
		return peerswapElementsSource{}, err
	} else if configured {
		return s.completeConfiguredPeerswapElementsSource(ctx, source)
	}
	if ok, _ := peerswapLocalElementsReady(ctx); ok {
		return peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}, nil
	}
	return peerswapElementsSource{}, errors.New("Peerswap Elements source is not configured")
}

func normalizePeerswapElementsSourceRequest(req peerswapElementsSourceRequest) (peerswapElementsSource, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = peerswapElementsModeRemote
	}
	switch mode {
	case peerswapElementsModeLocal:
		return peerswapElementsSource{Mode: peerswapElementsModeLocal, Wallet: peerswapElementsWallet}, nil
	case peerswapElementsModeRemote:
		endpoint, err := normalizePeerswapRemoteEndpoint(req.URL)
		if err != nil {
			return peerswapElementsSource{}, err
		}
		user := strings.TrimSpace(req.User)
		password := strings.TrimSpace(req.Password)
		if user == "" || password == "" {
			return peerswapElementsSource{}, errors.New("remote Elements RPC user and password are required")
		}
		return peerswapElementsSource{
			Mode:     peerswapElementsModeRemote,
			URL:      endpoint.URL,
			User:     user,
			Password: password,
		}, nil
	default:
		return peerswapElementsSource{}, errors.New("invalid Elements source mode")
	}
}

func (s *Server) completeConfiguredPeerswapElementsSource(ctx context.Context, source peerswapElementsSource) (peerswapElementsSource, error) {
	completed, _, err := s.completePeerswapElementsSource(ctx, source)
	return completed, err
}

func (s *Server) completePeerswapElementsSource(ctx context.Context, source peerswapElementsSource) (peerswapElementsSource, bool, error) {
	if source.Mode != peerswapElementsModeRemote {
		if strings.TrimSpace(source.Wallet) == "" {
			source.Wallet = peerswapElementsWallet
			return source, true, nil
		}
		return source, false, nil
	}
	if isPeerswapRemoteWalletName(source.Wallet) {
		cachedPubkey := ""
		if s != nil && s.lnd != nil {
			cachedPubkey = strings.TrimSpace(s.lnd.CachedPubkey())
		}
		if cachedPubkey == "" {
			return source, false, nil
		}
		wallet, err := peerswapRemoteWalletNameFromPubkey(cachedPubkey)
		if err != nil {
			return peerswapElementsSource{}, false, err
		}
		if wallet == source.Wallet {
			return source, false, nil
		}
		source.Wallet = wallet
		return source, true, nil
	}
	wallet, err := s.defaultPeerswapRemoteWallet(ctx)
	if err != nil {
		return peerswapElementsSource{}, false, err
	}
	if wallet != source.Wallet {
		source.Wallet = wallet
		return source, true, nil
	}
	return source, false, nil
}

func (s *Server) defaultPeerswapRemoteWallet(ctx context.Context) (string, error) {
	if s == nil || s.lnd == nil {
		return "", errors.New("LND pubkey is required to derive the remote Peerswap wallet")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, peerswapLNDIdentityTimeout)
	defer cancel()
	pubkey, err := s.lnd.SelfPubkey(lookupCtx)
	if err != nil {
		if errors.Is(lookupCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("LND is temporarily busy and did not return the node pubkey within 6 seconds; wait for LND RPC to recover, then retry the Peerswap installation")
		}
		return "", fmt.Errorf("failed to read LND pubkey for remote Peerswap wallet: %w", err)
	}
	return peerswapRemoteWalletNameFromPubkey(pubkey)
}

func isPeerswapRemoteWalletName(wallet string) bool {
	normalized := strings.ToLower(strings.TrimSpace(wallet))
	if !strings.HasPrefix(normalized, peerswapRemoteWalletPrefix) {
		return false
	}
	pubkey := strings.TrimPrefix(normalized, peerswapRemoteWalletPrefix)
	derived, err := peerswapRemoteWalletNameFromPubkey(pubkey)
	return err == nil && derived == normalized
}

func peerswapRemoteWalletNameFromPubkey(pubkey string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(pubkey))
	if len(normalized) != 66 {
		return "", errors.New("LND pubkey is required to derive the remote Peerswap wallet")
	}
	for _, ch := range normalized {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') {
			continue
		}
		return "", errors.New("LND pubkey is required to derive the remote Peerswap wallet")
	}
	return peerswapRemoteWalletPrefix + normalized, nil
}

func normalizePeerswapRemoteEndpoint(raw string) (peerswapRemoteEndpoint, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL must include scheme and host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL must use http or https")
	}
	if parsed.User != nil {
		return peerswapRemoteEndpoint{}, errors.New("put remote Elements RPC credentials in the user/password fields, not in the URL")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL must point to the RPC root")
	}
	port := 0
	if parsed.Port() != "" {
		n, err := strconv.Atoi(parsed.Port())
		if err != nil || n <= 0 || n > 65535 {
			return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL has an invalid port")
		}
		port = n
	} else if parsed.Scheme == "https" {
		port = 443
	} else {
		port = 80
	}
	host := parsed.Hostname()
	if host == "" {
		return peerswapRemoteEndpoint{}, errors.New("remote Elements RPC URL host is required")
	}
	canonicalURL := parsed.Scheme + "://" + parsed.Host
	return peerswapRemoteEndpoint{
		URL:  canonicalURL,
		Host: parsed.Scheme + "://" + host,
		Port: port,
	}, nil
}

func readPeerswapElementsSource(ctx context.Context, _ peerswapPaths) (peerswapElementsSource, bool, error) {
	handled, brokerSource, err := system.ReadPeerSwapSourceWithBroker(ctx)
	if !handled {
		return peerswapElementsSource{}, false, errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err != nil || !brokerSource.Configured {
		return peerswapElementsSource{}, false, err
	}
	source := peerswapElementsSource{Mode: brokerSource.Mode, URL: brokerSource.URL, User: brokerSource.User, Password: brokerSource.Password, Wallet: brokerSource.Wallet}
	return source, true, nil
}

func writePeerswapElementsSource(ctx context.Context, _ peerswapPaths, source peerswapElementsSource) error {
	handled, err := system.WritePeerSwapSourceWithBroker(ctx, system.PeerSwapBrokerSource{
		Configured: true,
		Mode:       source.Mode,
		URL:        source.URL,
		User:       source.User,
		Password:   source.Password,
		Wallet:     source.Wallet,
	})
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	return err
}

func peerswapSourceWallet(source peerswapElementsSource) string {
	if strings.TrimSpace(source.Wallet) != "" {
		return source.Wallet
	}
	return peerswapElementsWallet
}

func peerswapLocalElementsReady(ctx context.Context) (bool, string) {
	paths := elementsAppPaths()
	if !fileExists(paths.ElementsdPath) {
		return false, "not_installed"
	}
	status, err := elementsServiceStatus(ctx)
	if err != nil {
		return false, "unknown"
	}
	return status == "running", status
}

func testPeerswapElementsSource(ctx context.Context, source peerswapElementsSource) (string, error) {
	switch source.Mode {
	case peerswapElementsModeLocal:
		if ok, status := peerswapLocalElementsReady(ctx); !ok {
			return "", fmt.Errorf("local Elements is not running (status: %s)", status)
		}
		return "", nil
	case peerswapElementsModeRemote:
		return testPeerswapRemoteElementsRPC(ctx, source)
	default:
		return "", errors.New("invalid Elements source mode")
	}
}

func testPeerswapRemoteElementsRPC(ctx context.Context, source peerswapElementsSource) (string, error) {
	if _, err := normalizePeerswapRemoteEndpoint(source.URL); err != nil {
		return "", err
	}
	payload := []byte(`{"jsonrpc":"1.0","id":"t","method":"getblockchaininfo","params":[]}`)
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, source.URL+"/", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(source.User, source.Password)
	req.Header.Set("content-type", "text/plain;")
	resp, err := peerswapElementsHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("remote Elements RPC request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("remote Elements RPC returned %s", resp.Status)
	}
	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  any             `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return "", fmt.Errorf("remote Elements RPC returned invalid JSON: %w", err)
	}
	if rpcResp.Error != nil {
		return "", fmt.Errorf("remote Elements RPC returned error: %v", rpcResp.Error)
	}
	var result struct {
		Chain string `json:"chain"`
	}
	if len(rpcResp.Result) > 0 {
		_ = json.Unmarshal(rpcResp.Result, &result)
	}
	if result.Chain != "" && result.Chain != "liquidv1" {
		return "", fmt.Errorf("remote Elements RPC chain must be liquidv1, got %s", result.Chain)
	}
	return result.Chain, nil
}

func (s *Server) reconfigureInstalledPeerswap(ctx context.Context, paths peerswapPaths, source peerswapElementsSource) error {
	if err := s.ensurePeerswapBrokerRuntime(ctx, paths, source); err != nil {
		return err
	}
	handled, state, err := system.PeerSwapStatusWithBroker(ctx)
	if !handled {
		return errors.New("PeerSwap requires the privileged broker in enforce mode")
	}
	if err == nil && state.Status == "running" {
		if handled, err := system.PeerSwapLifecycleWithBroker(ctx, "restart"); !handled {
			return errors.New("PeerSwap requires the privileged broker in enforce mode")
		} else if err != nil {
			return err
		}
	}
	return nil
}
