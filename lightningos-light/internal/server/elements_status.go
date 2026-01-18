package server

import (
  "context"
  "encoding/json"
  "errors"
  "net/http"
  "strings"
  "time"
)

type elementsStatus struct {
  Installed bool `json:"installed"`
  Status string `json:"status"`
  DataDir string `json:"data_dir"`
  MainchainSource string `json:"mainchain_source,omitempty"`
  MainchainRPCHost string `json:"mainchain_rpchost,omitempty"`
  MainchainRPCPort int `json:"mainchain_rpcport,omitempty"`
  RPCOk bool `json:"rpc_ok"`
  Chain string `json:"chain,omitempty"`
  Blocks int64 `json:"blocks,omitempty"`
  Headers int64 `json:"headers,omitempty"`
  VerificationProgress float64 `json:"verification_progress,omitempty"`
  InitialBlockDownload bool `json:"initial_block_download,omitempty"`
  Peers int `json:"peers,omitempty"`
  Version int `json:"version,omitempty"`
  Subversion string `json:"subversion,omitempty"`
  SizeOnDisk int64 `json:"size_on_disk,omitempty"`
}

type elementsChainInfo struct {
  Chain string `json:"chain"`
  Blocks int64 `json:"blocks"`
  Headers int64 `json:"headers"`
  VerificationProgress float64 `json:"verificationprogress"`
  InitialBlockDownload bool `json:"initialblockdownload"`
  SizeOnDisk int64 `json:"size_on_disk"`
}

type elementsNetworkInfo struct {
  Version int `json:"version"`
  Subversion string `json:"subversion"`
  Connections int `json:"connections"`
}

func (s *Server) handleElementsStatus(w http.ResponseWriter, r *http.Request) {
  paths := elementsAppPaths()
  resp := elementsStatus{
    Installed: false,
    Status: "not_installed",
    DataDir: paths.DataDir,
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
  out, err := execElementsCLI(ctx, paths, "getblockchaininfo")
  if err != nil {
    return elementsChainInfo{}, elementsNetworkInfo{}, err
  }
  chainInfo := elementsChainInfo{}
  if err := json.Unmarshal([]byte(out), &chainInfo); err != nil {
    return elementsChainInfo{}, elementsNetworkInfo{}, err
  }

  netOut, err := execElementsCLI(ctx, paths, "getnetworkinfo")
  if err != nil {
    return chainInfo, elementsNetworkInfo{}, err
  }
  netInfo := elementsNetworkInfo{}
  if err := json.Unmarshal([]byte(netOut), &netInfo); err != nil {
    return chainInfo, elementsNetworkInfo{}, err
  }

  return chainInfo, netInfo, nil
}

func execElementsCLI(ctx context.Context, paths elementsPaths, args ...string) (string, error) {
  if !fileExists(paths.ElementsCliPath) {
    return "", errors.New("elements-cli missing")
  }
  cliArgs := []string{
    "--uid", elementsUser,
    "--gid", elementsUser,
    "--property=WorkingDirectory=" + paths.DataDir,
    paths.ElementsCliPath,
    "-conf=" + paths.ConfigPath,
    "-datadir=" + paths.DataDir,
    "-rpcwait",
    "-rpcwaittimeout=5",
  }
  cliArgs = append(cliArgs, args...)
  out, err := runSystemd(ctx, cliArgs...)
  if err != nil {
    return "", err
  }
  return strings.TrimSpace(out), nil
}
