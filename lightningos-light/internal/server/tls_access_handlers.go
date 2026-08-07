package server

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type tlsAccessInfoResponse struct {
	Available           bool     `json:"available"`
	LocalName           string   `json:"local_name,omitempty"`
	PreferredURL        string   `json:"preferred_url,omitempty"`
	IPURLs              []string `json:"ip_urls,omitempty"`
	CAName              string   `json:"ca_name,omitempty"`
	CAFingerprintSHA256 string   `json:"ca_fingerprint_sha256,omitempty"`
	CADownloadURL       string   `json:"ca_download_url,omitempty"`
	WindowsInstallerURL string   `json:"windows_installer_url,omitempty"`
	ServerExpiresAt     string   `json:"server_expires_at,omitempty"`
}

type tlsAccessMaterial struct {
	info     tlsAccessInfoResponse
	caPEM    []byte
	fileStem string
}

func (s *Server) handleTLSAccessInfo(w http.ResponseWriter, _ *http.Request) {
	setNoStore(w)
	material, err := s.loadTLSAccessMaterial()
	if err != nil {
		writeJSON(w, http.StatusOK, tlsAccessInfoResponse{Available: false})
		return
	}
	writeJSON(w, http.StatusOK, material.info)
}

func (s *Server) handleTLSCADownload(w http.ResponseWriter, _ *http.Request) {
	setNoStore(w)
	material, err := s.loadTLSAccessMaterial()
	if err != nil {
		writeError(w, http.StatusNotFound, "local node CA is not available")
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="lightningos-%s-ca.crt"`, material.fileStem))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(material.caPEM)
}

func (s *Server) handleTLSWindowsInstallerDownload(w http.ResponseWriter, _ *http.Request) {
	setNoStore(w)
	material, err := s.loadTLSAccessMaterial()
	if err != nil {
		writeError(w, http.StatusNotFound, "local node CA is not available")
		return
	}

	script := windowsTrustScript(material)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="lightningos-trust-%s.ps1"`, material.fileStem))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(script))
}

func (s *Server) loadTLSAccessMaterial() (tlsAccessMaterial, error) {
	if s == nil || s.cfg == nil {
		return tlsAccessMaterial{}, fmt.Errorf("server configuration unavailable")
	}

	tlsDir := filepath.Dir(strings.TrimSpace(s.cfg.Server.TLSCert))
	if tlsDir == "" || tlsDir == "." {
		return tlsAccessMaterial{}, fmt.Errorf("TLS directory unavailable")
	}

	caPath := ""
	for _, name := range []string{"local-ca.crt", "los-local-ca.crt"} {
		candidate := filepath.Join(tlsDir, name)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			caPath = candidate
			break
		}
	}
	if caPath == "" {
		return tlsAccessMaterial{}, fmt.Errorf("local CA unavailable")
	}

	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return tlsAccessMaterial{}, err
	}
	caCert, err := parsePEMCertificate(caPEM)
	if err != nil || !caCert.IsCA {
		return tlsAccessMaterial{}, fmt.Errorf("invalid local CA certificate")
	}

	serverPEM, err := os.ReadFile(s.cfg.Server.TLSCert)
	if err != nil {
		return tlsAccessMaterial{}, err
	}
	serverCert, err := parsePEMCertificate(serverPEM)
	if err != nil {
		return tlsAccessMaterial{}, err
	}
	localName := preferredLocalDNSName(serverCert.DNSNames)
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := serverCert.Verify(x509.VerifyOptions{
		Roots:     roots,
		DNSName:   localName,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		return tlsAccessMaterial{}, fmt.Errorf("server certificate is not trusted by the local CA: %w", err)
	}

	port := s.cfg.Server.Port
	if port <= 0 {
		port = 8443
	}
	ipURLs := privateIPURLs(serverCert.IPAddresses, port)
	preferredURL := ""
	if localName != "" {
		preferredURL = "https://" + net.JoinHostPort(localName, fmt.Sprintf("%d", port))
	} else if len(ipURLs) > 0 {
		preferredURL = ipURLs[0]
	}

	fingerprint := sha256.Sum256(caCert.Raw)
	fileStem := safeDownloadStem(strings.TrimSuffix(localName, ".local"))
	if fileStem == "node" && len(serverCert.IPAddresses) > 0 {
		fileStem = safeDownloadStem(serverCert.IPAddresses[0].String())
	}

	return tlsAccessMaterial{
		info: tlsAccessInfoResponse{
			Available:           true,
			LocalName:           localName,
			PreferredURL:        preferredURL,
			IPURLs:              ipURLs,
			CAName:              strings.TrimSpace(caCert.Subject.CommonName),
			CAFingerprintSHA256: colonFingerprint(fingerprint[:]),
			CADownloadURL:       "/api/tls/ca",
			WindowsInstallerURL: "/api/tls/windows",
			ServerExpiresAt:     serverCert.NotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		},
		caPEM:    caPEM,
		fileStem: fileStem,
	}, nil
}

func parsePEMCertificate(contents []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(contents)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("certificate PEM block missing")
	}
	return x509.ParseCertificate(block.Bytes)
}

func preferredLocalDNSName(names []string) string {
	for _, name := range names {
		trimmed := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(name, ".")))
		if strings.HasSuffix(trimmed, ".local") && trimmed != ".local" {
			return trimmed
		}
	}
	return ""
}

func privateIPURLs(ips []net.IP, port int) []string {
	seen := make(map[string]struct{})
	urls := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == nil || ip.IsLoopback() || !ip.IsPrivate() {
			continue
		}
		host := ip.String()
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		urls = append(urls, "https://"+net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	}
	sort.Strings(urls)
	return urls
}

func safeDownloadStem(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return "node"
	}
	if len(trimmed) > 64 {
		return trimmed[:64]
	}
	return trimmed
}

func colonFingerprint(raw []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(raw))
	parts := make([]string, 0, len(encoded)/2)
	for i := 0; i+2 <= len(encoded); i += 2 {
		parts = append(parts, encoded[i:i+2])
	}
	return strings.Join(parts, ":")
}

func windowsTrustScript(material tlsAccessMaterial) string {
	expected := strings.ReplaceAll(material.info.CAFingerprintSHA256, ":", "")
	preferredURL := strings.ReplaceAll(material.info.PreferredURL, "'", "''")
	encodedCA := base64.StdEncoding.EncodeToString(material.caPEM)
	return fmt.Sprintf(`# LightningOS local CA installer
# CA SHA256: %s
$ErrorActionPreference = 'Stop'
$losCaPath = Join-Path ([System.IO.Path]::GetTempPath()) ('lightningos-ca-' + [guid]::NewGuid().ToString('N') + '.crt')
try {
  [System.IO.File]::WriteAllBytes($losCaPath, [Convert]::FromBase64String('%s'))
  $losCertificate = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($losCaPath)
  $losSha256 = [System.Security.Cryptography.SHA256]::Create()
  try {
    $losActual = ([BitConverter]::ToString($losSha256.ComputeHash($losCertificate.RawData))).Replace('-', '')
  } finally {
    $losSha256.Dispose()
  }
  if ($losActual -ne '%s') {
    throw "LightningOS CA fingerprint mismatch. Expected %s, got $losActual"
  }
  & "$env:SystemRoot\System32\certutil.exe" -f -user -addstore Root $losCaPath
  if ($LASTEXITCODE -ne 0) {
    throw "certutil failed with exit code $LASTEXITCODE"
  }
  Write-Host ''
  Write-Host 'LightningOS node CA trusted for the current Windows user.' -ForegroundColor Green
  Write-Host 'Verified SHA256: %s'
  Write-Host 'Open: %s' -ForegroundColor Cyan
} finally {
  Remove-Item -LiteralPath $losCaPath -Force -ErrorAction SilentlyContinue
}
`, material.info.CAFingerprintSHA256, encodedCA, expected, expected, material.info.CAFingerprintSHA256, preferredURL)
}
