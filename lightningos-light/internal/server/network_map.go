package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/lndclient"
)

const (
	networkAtlasNodeLabelEnv     = "NETWORK_ATLAS_NODE_LABEL"
	networkAtlasNodeLatEnv       = "NETWORK_ATLAS_NODE_LAT"
	networkAtlasNodeLonEnv       = "NETWORK_ATLAS_NODE_LON"
	networkAtlasGeoCacheTTL      = 24 * time.Hour
	networkAtlasGeoRequestTTL    = 5 * time.Second
	networkAtlasLocationEndpoint = "https://ipwho.is/"
)

type networkMapGeoCacheEntry struct {
	location  networkMapResolvedLocation
	expiresAt time.Time
}

type networkMapResolvedLocation struct {
	IP          string
	Country     string
	CountryCode string
	City        string
	Latitude    float64
	Longitude   float64
}

type networkMapNode struct {
	Alias       string   `json:"alias"`
	PubKey      string   `json:"pubkey"`
	Label       string   `json:"label"`
	Latitude    float64  `json:"lat"`
	Longitude   float64  `json:"lon"`
	LocationSet bool     `json:"location_set"`
	Source      string   `json:"source"`
	Warnings    []string `json:"warnings,omitempty"`
}

type networkMapLink struct {
	PubKey         string  `json:"pubkey"`
	Alias          string  `json:"alias"`
	Socket         string  `json:"socket,omitempty"`
	Host           string  `json:"host,omitempty"`
	Country        string  `json:"country,omitempty"`
	CountryCode    string  `json:"country_code,omitempty"`
	City           string  `json:"city,omitempty"`
	Latitude       float64 `json:"lat,omitempty"`
	Longitude      float64 `json:"lon,omitempty"`
	ConnectionKind string  `json:"connection_kind"`
	ChannelCount   int     `json:"channel_count"`
	CapacitySat    int64   `json:"capacity_sat"`
	Active         bool    `json:"active"`
	IsOnion        bool    `json:"is_onion"`
	IsPrivateIP    bool    `json:"is_private_ip"`
	Mapped         bool    `json:"mapped"`
	Reason         string  `json:"reason,omitempty"`
}

type networkMapSummary struct {
	TotalPeers        int   `json:"total_peers"`
	ChannelPeers      int   `json:"channel_peers"`
	PeerOnly          int   `json:"peer_only"`
	UnknownLocation   int   `json:"unknown_location"`
	Countries         int   `json:"countries"`
	MappedCapacitySat int64 `json:"mapped_capacity_sat"`
}

type networkMapResponse struct {
	LocalNode networkMapNode    `json:"local_node"`
	Summary   networkMapSummary `json:"summary"`
	Links     []networkMapLink  `json:"links"`
	Unmapped  []networkMapLink  `json:"unmapped"`
}

type networkAtlasConfigPayload struct {
	Label               string   `json:"label"`
	Latitude            *float64 `json:"lat"`
	Longitude           *float64 `json:"lon"`
	HasExplicitLocation bool     `json:"has_explicit_location"`
}

type ipWhoisResponse struct {
	Success     bool    `json:"success"`
	Message     string  `json:"message"`
	IP          string  `json:"ip"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	City        string  `json:"city"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type networkMapPeerState struct {
	PubKey         string
	Alias          string
	Socket         string
	ConnectionKind string
	ChannelCount   int
	CapacitySat    int64
	Active         bool
}

func (s *Server) handleLNNetworkMapGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	payload, err := s.buildNetworkMapPayload(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleLNNetworkMapConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := readNetworkAtlasConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read network atlas config")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleLNNetworkMapConfigPost(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string  `json:"label"`
		Lat   *string `json:"lat"`
		Lon   *string `json:"lon"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	lat, latSet, err := parseOptionalCoordinate(req.Lat, -90, 90)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid latitude")
		return
	}
	lon, lonSet, err := parseOptionalCoordinate(req.Lon, -180, 180)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid longitude")
		return
	}
	if latSet != lonSet {
		writeError(w, http.StatusBadRequest, "lat and lon must both be set or both be empty")
		return
	}

	if err := writeEnvFileValue(secretsPath, networkAtlasNodeLabelEnv, strings.TrimSpace(req.Label)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save network atlas label")
		return
	}
	if latSet {
		if err := writeEnvFileValue(secretsPath, networkAtlasNodeLatEnv, strconv.FormatFloat(lat, 'f', 6, 64)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save network atlas latitude")
			return
		}
		if err := writeEnvFileValue(secretsPath, networkAtlasNodeLonEnv, strconv.FormatFloat(lon, 'f', 6, 64)); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save network atlas longitude")
			return
		}
	} else {
		if err := writeEnvFileValue(secretsPath, networkAtlasNodeLatEnv, ""); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear network atlas latitude")
			return
		}
		if err := writeEnvFileValue(secretsPath, networkAtlasNodeLonEnv, ""); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clear network atlas longitude")
			return
		}
	}

	cfg, err := readNetworkAtlasConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read saved network atlas config")
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) buildNetworkMapPayload(ctx context.Context) (networkMapResponse, error) {
	status, err := s.lnd.GetStatus(ctx)
	if err != nil {
		return networkMapResponse{}, errors.New(lndDetailedErrorMessage(err))
	}

	channels, err := s.lnd.ListChannels(ctx)
	if err != nil {
		return networkMapResponse{}, errors.New(lndDetailedErrorMessage(err))
	}
	peers, err := s.lnd.ListPeers(ctx)
	if err != nil {
		return networkMapResponse{}, errors.New(lndDetailedErrorMessage(err))
	}

	peerStates := map[string]*networkMapPeerState{}
	for _, ch := range channels {
		pubkey := strings.TrimSpace(ch.RemotePubkey)
		if pubkey == "" {
			continue
		}
		state := peerStates[pubkey]
		if state == nil {
			state = &networkMapPeerState{
				PubKey:         pubkey,
				Alias:          strings.TrimSpace(ch.PeerAlias),
				ConnectionKind: "channel",
			}
			peerStates[pubkey] = state
		}
		state.ChannelCount++
		state.CapacitySat += ch.CapacitySat
		state.Active = state.Active || ch.Active
		if strings.TrimSpace(ch.PeerAlias) != "" {
			state.Alias = strings.TrimSpace(ch.PeerAlias)
		}
	}

	for _, peer := range peers {
		pubkey := strings.TrimSpace(peer.PubKey)
		if pubkey == "" {
			continue
		}
		state := peerStates[pubkey]
		if state == nil {
			state = &networkMapPeerState{
				PubKey:         pubkey,
				ConnectionKind: "peer",
			}
			peerStates[pubkey] = state
		}
		if strings.TrimSpace(peer.Alias) != "" {
			state.Alias = strings.TrimSpace(peer.Alias)
		}
		if socket := normalizeSocket(peer.Address); socket != "" {
			state.Socket = socket
		}
	}

	localNode, err := s.resolveLocalNode(ctx, status)
	if err != nil {
		return networkMapResponse{}, err
	}

	links := make([]networkMapLink, 0, len(peerStates))
	unmapped := make([]networkMapLink, 0)
	countries := map[string]struct{}{}

	for _, state := range peerStates {
		link := networkMapLink{
			PubKey:         state.PubKey,
			Alias:          fallbackAtlasAlias(state.Alias, state.PubKey),
			ConnectionKind: state.ConnectionKind,
			ChannelCount:   state.ChannelCount,
			CapacitySat:    state.CapacitySat,
			Active:         state.Active,
		}

		socket, isOnion, isPrivateIP, reason := s.resolveAtlasSocket(ctx, state)
		link.Socket = socket
		link.IsOnion = isOnion
		link.IsPrivateIP = isPrivateIP
		link.Reason = reason

		host, _ := splitHostPortLoose(socket)
		link.Host = strings.Trim(strings.TrimSpace(host), "[]")
		if reason == "" && link.Host != "" {
			location, geoErr := s.resolveAtlasGeo(ctx, link.Host)
			if geoErr == nil {
				link.Latitude = location.Latitude
				link.Longitude = location.Longitude
				link.Country = location.Country
				link.CountryCode = location.CountryCode
				link.City = location.City
				link.Mapped = true
				if code := strings.TrimSpace(location.CountryCode); code != "" {
					countries[code] = struct{}{}
				}
			} else {
				link.Reason = "geo_unavailable"
			}
		}

		if link.Mapped {
			links = append(links, link)
		} else {
			unmapped = append(unmapped, link)
		}
	}

	sort.Slice(links, func(i, j int) bool {
		if links[i].CapacitySat == links[j].CapacitySat {
			return strings.ToLower(links[i].Alias) < strings.ToLower(links[j].Alias)
		}
		return links[i].CapacitySat > links[j].CapacitySat
	})
	sort.Slice(unmapped, func(i, j int) bool {
		if unmapped[i].CapacitySat == unmapped[j].CapacitySat {
			return strings.ToLower(unmapped[i].Alias) < strings.ToLower(unmapped[j].Alias)
		}
		return unmapped[i].CapacitySat > unmapped[j].CapacitySat
	})

	var mappedCapacity int64
	channelPeers := 0
	peerOnly := 0
	for _, item := range links {
		if item.ConnectionKind == "channel" {
			channelPeers++
		} else {
			peerOnly++
		}
		mappedCapacity += item.CapacitySat
	}
	for _, item := range unmapped {
		if item.ConnectionKind == "channel" {
			channelPeers++
		} else {
			peerOnly++
		}
	}

	return networkMapResponse{
		LocalNode: localNode,
		Summary: networkMapSummary{
			TotalPeers:        len(peerStates),
			ChannelPeers:      channelPeers,
			PeerOnly:          peerOnly,
			UnknownLocation:   len(unmapped),
			Countries:         len(countries),
			MappedCapacitySat: mappedCapacity,
		},
		Links:    links,
		Unmapped: unmapped,
	}, nil
}

func readNetworkAtlasConfig() (networkAtlasConfigPayload, error) {
	cfg := networkAtlasConfigPayload{
		Label: strings.TrimSpace(osEnvOrFile(networkAtlasNodeLabelEnv)),
	}

	latRaw := strings.TrimSpace(osEnvOrFile(networkAtlasNodeLatEnv))
	lonRaw := strings.TrimSpace(osEnvOrFile(networkAtlasNodeLonEnv))
	if latRaw == "" || lonRaw == "" {
		return cfg, nil
	}

	lat, err := strconv.ParseFloat(latRaw, 64)
	if err != nil {
		return cfg, err
	}
	lon, err := strconv.ParseFloat(lonRaw, 64)
	if err != nil {
		return cfg, err
	}
	cfg.Latitude = &lat
	cfg.Longitude = &lon
	cfg.HasExplicitLocation = true
	return cfg, nil
}

func osEnvOrFile(key string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	value, _ := readEnvFileValue(secretsPath, key)
	return strings.TrimSpace(value)
}

func parseOptionalCoordinate(raw *string, minValue, maxValue float64) (float64, bool, error) {
	if raw == nil {
		return 0, false, nil
	}
	value := strings.TrimSpace(*raw)
	if value == "" {
		return 0, false, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, err
	}
	if parsed < minValue || parsed > maxValue {
		return 0, false, fmt.Errorf("out of range")
	}
	return parsed, true, nil
}

func (s *Server) resolveLocalNode(ctx context.Context, status lndclient.Status) (networkMapNode, error) {
	cfg, err := readNetworkAtlasConfig()
	if err != nil {
		return networkMapNode{}, errors.New("failed to load network atlas config")
	}

	label := strings.TrimSpace(cfg.Label)
	pubkey := strings.TrimSpace(status.Pubkey)
	alias := label
	if alias == "" {
		alias = shortAtlasPubkey(pubkey)
	}

	node := networkMapNode{
		Alias:  alias,
		Label:  alias,
		PubKey: pubkey,
	}

	if cfg.HasExplicitLocation && cfg.Latitude != nil && cfg.Longitude != nil {
		node.Latitude = *cfg.Latitude
		node.Longitude = *cfg.Longitude
		node.LocationSet = true
		node.Source = "configured"
		return node, nil
	}

	location, geoErr := s.fetchAtlasGeo(ctx, "")
	if geoErr == nil {
		node.Latitude = location.Latitude
		node.Longitude = location.Longitude
		node.LocationSet = true
		node.Source = "detected"
		node.Warnings = append(node.Warnings, "Detected from server public IP. You can override this in Network Atlas settings.")
		return node, nil
	}

	node.Source = "unavailable"
	node.Warnings = append(node.Warnings, "Local node location unavailable. Add manual coordinates to render the atlas.")
	return node, nil
}

func fallbackAtlasAlias(alias, pubkey string) string {
	if trimmed := strings.TrimSpace(alias); trimmed != "" {
		return trimmed
	}
	return shortAtlasPubkey(pubkey)
}

func shortAtlasPubkey(pubkey string) string {
	trimmed := strings.TrimSpace(pubkey)
	if len(trimmed) <= 18 {
		return trimmed
	}
	return trimmed[:9] + "..." + trimmed[len(trimmed)-6:]
}

func (s *Server) resolveAtlasSocket(ctx context.Context, state *networkMapPeerState) (socket string, isOnion bool, isPrivateIP bool, reason string) {
	if state == nil {
		return "", false, false, "missing_peer"
	}
	socket = normalizeSocket(state.Socket)
	if socket == "" {
		detailsCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		details, err := s.lnd.GetNodeDetails(detailsCtx, state.PubKey)
		if err == nil {
			clearnet, hasOnion := splitNodeSockets(details.Addresses)
			if len(clearnet) > 0 {
				socket = clearnet[0]
			}
			isOnion = hasOnion && socket == ""
		}
	}
	if socket == "" {
		if isOnion {
			return "", true, false, "tor_only"
		}
		return "", false, false, "address_unavailable"
	}
	if isOnionSocket(socket) {
		return socket, true, false, "tor_only"
	}
	host, _ := splitHostPortLoose(socket)
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return socket, false, false, "host_unavailable"
	}
	if isLocalAtlasHost(host) {
		return socket, false, true, "private_ip"
	}
	return socket, false, false, ""
}

func isLocalAtlasHost(host string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(host))
	if trimmed == "" {
		return true
	}
	if trimmed == "localhost" || strings.HasSuffix(trimmed, ".local") {
		return true
	}
	ip := net.ParseIP(trimmed)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func (s *Server) resolveAtlasGeo(ctx context.Context, host string) (networkMapResolvedLocation, error) {
	cacheKey := strings.ToLower(strings.TrimSpace(host))
	if cacheKey == "" {
		return networkMapResolvedLocation{}, errors.New("empty host")
	}

	if cached, ok := s.getAtlasGeoCache(cacheKey); ok {
		return cached, nil
	}

	location, err := s.fetchAtlasGeo(ctx, cacheKey)
	if err != nil {
		return networkMapResolvedLocation{}, err
	}
	s.setAtlasGeoCache(cacheKey, location)
	return location, nil
}

func (s *Server) getAtlasGeoCache(key string) (networkMapResolvedLocation, bool) {
	now := time.Now()
	s.networkMapMu.Lock()
	defer s.networkMapMu.Unlock()

	entry, ok := s.networkMapGeoCache[key]
	if !ok {
		return networkMapResolvedLocation{}, false
	}
	if now.After(entry.expiresAt) {
		delete(s.networkMapGeoCache, key)
		return networkMapResolvedLocation{}, false
	}
	return entry.location, true
}

func (s *Server) setAtlasGeoCache(key string, location networkMapResolvedLocation) {
	s.networkMapMu.Lock()
	defer s.networkMapMu.Unlock()
	s.networkMapGeoCache[key] = networkMapGeoCacheEntry{
		location:  location,
		expiresAt: time.Now().Add(networkAtlasGeoCacheTTL),
	}
}

func (s *Server) fetchAtlasGeo(ctx context.Context, query string) (networkMapResolvedLocation, error) {
	geoCtx, cancel := context.WithTimeout(ctx, networkAtlasGeoRequestTTL)
	defer cancel()

	url := networkAtlasLocationEndpoint
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		url += trimmed
	}

	req, err := http.NewRequestWithContext(geoCtx, http.MethodGet, url, nil)
	if err != nil {
		return networkMapResolvedLocation{}, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return networkMapResolvedLocation{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return networkMapResolvedLocation{}, fmt.Errorf("geo lookup status %d", resp.StatusCode)
	}

	var payload ipWhoisResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return networkMapResolvedLocation{}, err
	}
	if !payload.Success {
		if strings.TrimSpace(payload.Message) == "" {
			return networkMapResolvedLocation{}, errors.New("geo lookup failed")
		}
		return networkMapResolvedLocation{}, errors.New(strings.TrimSpace(payload.Message))
	}

	return networkMapResolvedLocation{
		IP:          strings.TrimSpace(payload.IP),
		Country:     strings.TrimSpace(payload.Country),
		CountryCode: strings.TrimSpace(payload.CountryCode),
		City:        strings.TrimSpace(payload.City),
		Latitude:    payload.Latitude,
		Longitude:   payload.Longitude,
	}, nil
}
