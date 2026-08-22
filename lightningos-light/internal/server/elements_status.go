package server

import (
	"context"
	"net/http"
	"time"
)

type elementsStatus struct {
	Installed            bool    `json:"installed"`
	Status               string  `json:"status"`
	DataDir              string  `json:"data_dir"`
	MainchainSource      string  `json:"mainchain_source,omitempty"`
	MainchainRPCHost     string  `json:"mainchain_rpchost,omitempty"`
	MainchainRPCPort     int     `json:"mainchain_rpcport,omitempty"`
	RPCOk                bool    `json:"rpc_ok"`
	Chain                string  `json:"chain,omitempty"`
	Blocks               int64   `json:"blocks,omitempty"`
	Headers              int64   `json:"headers,omitempty"`
	VerificationProgress float64 `json:"verification_progress,omitempty"`
	InitialBlockDownload bool    `json:"initial_block_download,omitempty"`
	Peers                int     `json:"peers,omitempty"`
	Version              int     `json:"version,omitempty"`
	Subversion           string  `json:"subversion,omitempty"`
	SizeOnDisk           int64   `json:"size_on_disk,omitempty"`
}

func (s *Server) handleElementsStatus(w http.ResponseWriter, r *http.Request) {
	paths := elementsAppPaths()
	resp := elementsStatus{
		Installed: false,
		Status:    "not_installed",
		DataDir:   paths.DataDir,
	}
	resp.MainchainSource = readElementsMainchainSource(paths)

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	state, err := elementsBrokerStatus(ctx, paths)
	if err != nil {
		resp.Status = "unknown"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Installed = state.Installed
	resp.Status = state.Status
	resp.DataDir = state.DataDir
	if !state.Installed {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if raw, err := readElementsConfig(ctx, paths); err == nil {
		host, port := parseElementsMainchainConfig(raw)
		if host == "" {
			host = defaultElementsMainchainHost(resp.MainchainSource, s.cfg)
		}
		if port == 0 {
			port = defaultElementsMainchainPort(resp.MainchainSource, s.cfg)
		}
		resp.MainchainRPCHost = host
		resp.MainchainRPCPort = port
	} else {
		resp.MainchainRPCHost = defaultElementsMainchainHost(resp.MainchainSource, s.cfg)
		resp.MainchainRPCPort = defaultElementsMainchainPort(resp.MainchainSource, s.cfg)
	}

	if state.Status != "running" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	if !state.RPCOK {
		resp.RPCOk = false
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.RPCOk = true
	resp.Chain = state.Chain
	resp.Blocks = state.Blocks
	resp.Headers = state.Headers
	resp.VerificationProgress = state.VerificationProgress
	resp.InitialBlockDownload = state.InitialBlockDownload
	resp.SizeOnDisk = state.SizeOnDisk
	resp.Version = state.Version
	resp.Subversion = state.Subversion
	resp.Peers = state.Peers

	writeJSON(w, http.StatusOK, resp)
}
