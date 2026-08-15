package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"lightningos-light/internal/system"
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

type elementsChainInfo struct {
	Chain                string  `json:"chain"`
	Blocks               int64   `json:"blocks"`
	Headers              int64   `json:"headers"`
	VerificationProgress float64 `json:"verificationprogress"`
	InitialBlockDownload bool    `json:"initialblockdownload"`
	SizeOnDisk           int64   `json:"size_on_disk"`
}

type elementsNetworkInfo struct {
	Version     int    `json:"version"`
	Subversion  string `json:"subversion"`
	Connections int    `json:"connections"`
}

func (s *Server) handleElementsStatus(w http.ResponseWriter, r *http.Request) {
	paths := elementsAppPaths()
	resp := elementsStatus{
		Installed: false,
		Status:    "not_installed",
		DataDir:   paths.DataDir,
	}
	resp.MainchainSource = readElementsMainchainSource(paths)
	if !fileExists(paths.ElementsdPath) {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Installed = true

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

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

	status, err := elementsServiceStatus(ctx)
	if err != nil {
		resp.Status = "unknown"
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Status = status
	if status != "running" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	chainInfo, networkInfo, err := fetchElementsInfo(ctx, paths)
	if err != nil {
		resp.RPCOk = false
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.RPCOk = true
	resp.Chain = chainInfo.Chain
	resp.Blocks = chainInfo.Blocks
	resp.Headers = chainInfo.Headers
	resp.VerificationProgress = chainInfo.VerificationProgress
	resp.InitialBlockDownload = chainInfo.InitialBlockDownload
	resp.SizeOnDisk = chainInfo.SizeOnDisk
	resp.Version = networkInfo.Version
	resp.Subversion = networkInfo.Subversion
	resp.Peers = networkInfo.Connections

	writeJSON(w, http.StatusOK, resp)
}

func fetchElementsInfo(ctx context.Context, paths elementsPaths) (elementsChainInfo, elementsNetworkInfo, error) {
	if handled, raw, err := system.ElementsStatusWithBroker(ctx, paths.DataDir); handled {
		if err != nil {
			return elementsChainInfo{}, elementsNetworkInfo{}, err
		}
		var state struct {
			RPCOK                bool    `json:"rpc_ok"`
			Chain                string  `json:"chain"`
			Blocks               int64   `json:"blocks"`
			Headers              int64   `json:"headers"`
			VerificationProgress float64 `json:"verification_progress"`
			InitialBlockDownload bool    `json:"initial_block_download"`
			Peers                int     `json:"peers"`
			Version              int     `json:"version"`
			Subversion           string  `json:"subversion"`
			SizeOnDisk           int64   `json:"size_on_disk"`
		}
		if err := json.Unmarshal([]byte(raw), &state); err != nil || !state.RPCOK {
			return elementsChainInfo{}, elementsNetworkInfo{}, errors.New("Elements RPC unavailable")
		}
		return elementsChainInfo{
			Chain: state.Chain, Blocks: state.Blocks, Headers: state.Headers,
			VerificationProgress: state.VerificationProgress, InitialBlockDownload: state.InitialBlockDownload,
			SizeOnDisk: state.SizeOnDisk,
		}, elementsNetworkInfo{Version: state.Version, Subversion: state.Subversion, Connections: state.Peers}, nil
	}
	return elementsChainInfo{}, elementsNetworkInfo{}, errors.New("Elements status requires privileged broker enforce mode")
}
