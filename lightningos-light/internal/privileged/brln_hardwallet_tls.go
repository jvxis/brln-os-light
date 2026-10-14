package privileged

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"path/filepath"
	"time"
)

// Recheck trust, expiry and the names used by the manager before reusing a leaf.
// The caller supplies the evaluation time so ensure can renew before expiry.
func (m *NativeHardwalletManager) tlsCurrent(raw []byte, bindIPs []string, at time.Time) bool {
	block, _ := pem.Decode(raw)
	if block == nil {
		return false
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	caRaw, err := readRegularFile(m.Paths.ManagerCA, 64*1024)
	if err != nil {
		return false
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caRaw) {
		return false
	}
	if len(bindIPs) == 0 {
		return false
	}
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: bindIPs[0], CurrentTime: at}); err != nil {
		return false
	}
	for _, ip := range bindIPs {
		if leaf.VerifyHostname(ip) != nil {
			return false
		}
	}
	managerRaw, err := readRegularFile(filepath.Join(filepath.Dir(m.Paths.ManagerCA), "server.crt"), 64*1024)
	if err != nil {
		return false
	}
	managerBlock, _ := pem.Decode(managerRaw)
	if managerBlock == nil {
		return false
	}
	manager, err := x509.ParseCertificate(managerBlock.Bytes)
	if err != nil {
		return false
	}
	for _, name := range manager.DNSNames {
		if leaf.VerifyHostname(name) != nil {
			return false
		}
	}
	for _, ip := range manager.IPAddresses {
		if leaf.VerifyHostname(ip.String()) != nil {
			return false
		}
	}
	return true
}

// A distinct app key, signed by the existing local LOS CA. No CA key or manager
// private key is ever mounted in a container. Paths are broker-owned constants.
func (m *NativeHardwalletManager) issueTLS(bindIPs []string) ([]byte, []byte, error) {
	caRaw, err := readRegularFile(m.Paths.ManagerCA, 64*1024)
	if err != nil {
		return nil, nil, err
	}
	caBlock, _ := pem.Decode(caRaw)
	if caBlock == nil {
		return nil, nil, errors.New("invalid local CA")
	}
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil || !ca.IsCA {
		return nil, nil, errors.New("invalid local CA")
	}
	if time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
		return nil, nil, errors.New("local CA is not currently valid")
	}
	caKeyPath := filepath.Join(filepath.Dir(m.Paths.ManagerCA), "local-ca.key")
	if filepath.Base(m.Paths.ManagerCA) == "los-local-ca.crt" {
		caKeyPath = filepath.Join(filepath.Dir(m.Paths.ManagerCA), "los-local-ca.key")
	}
	if err = validatePrivilegedPrivateFile(caKeyPath); err != nil {
		return nil, nil, err
	}
	raw, err := readRegularFile(caKeyPath, 64*1024)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, nil, errors.New("invalid local CA key")
	}
	var parsed any
	parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		parsed, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	if err != nil {
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	}
	signer, ok := parsed.(crypto.Signer)
	if err != nil || !ok {
		return nil, nil, errors.New("unsupported local CA key")
	}
	managerRaw, err := readRegularFile(filepath.Join(filepath.Dir(m.Paths.ManagerCA), "server.crt"), 64*1024)
	if err != nil {
		return nil, nil, err
	}
	managerBlock, _ := pem.Decode(managerRaw)
	if managerBlock == nil {
		return nil, nil, errors.New("invalid manager certificate")
	}
	managerCert, err := x509.ParseCertificate(managerBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	expires := time.Now().Add(90 * 24 * time.Hour)
	if ca.NotAfter.Before(expires) {
		expires = ca.NotAfter
	}
	addresses := append([]net.IP(nil), managerCert.IPAddresses...)
	for _, address := range bindIPs {
		addresses = append(addresses, net.ParseIP(address))
	}
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "BRLN Hardwallet"}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: managerCert.DNSNames, IPAddresses: addresses}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, ca, &key.PublicKey, signer)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil || cert.CheckSignatureFrom(ca) != nil {
		return nil, nil, errors.New("local CA key mismatch")
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), nil
}
