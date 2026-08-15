// Package certs 提供 CA 管理、CSR 生成与签发、证书校验能力。
//
// 私钥原则：
//   - Agent 私钥只在 Agent 本地生成（GenerateCSR）。
//   - CA 私钥只保存在离线/受保护的证书管理机上，不放入 Relay 或 WebUI。
package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

// CA 代表一个证书颁发机构。
type CA struct {
	Cert *x509.Certificate
	Key  *rsa.PrivateKey
	Pool *x509.CertPool
}

// LoadCA 从 PEM 字节加载 CA 证书与私钥。
func LoadCA(certPEM, keyPEM []byte) (*CA, error) {
	cert, err := ParseCert(certPEM)
	if err != nil {
		return nil, err
	}
	key, err := ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return nil, fmt.Errorf("certs: cannot add CA cert to pool")
	}
	return &CA{Cert: cert, Key: key, Pool: pool}, nil
}

// LoadCAFiles 从文件加载 CA。
func LoadCAFiles(certPath, keyPath string) (*CA, error) {
	cp, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	kp, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	return LoadCA(cp, kp)
}

// CreateCA 生成一个新的 CA（用于 certctl init-ca）。
func CreateCA(cn string, days int) (certPEM, keyPEM []byte, err error) {
	if days <= 0 {
		return nil, nil, fmt.Errorf("certs: validity days must be positive")
	}
	if strings.TrimSpace(cn) == "" {
		return nil, nil, fmt.Errorf("certs: CA common name is required")
	}
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"ModelRelay"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// GenerateCSR 在本地生成私钥与 CSR（Agent 机器上执行）。
// 私钥绝不离开本机。
func GenerateCSR(cn string) (keyPEM, csrPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn, Organization: []string{"ModelRelay Agent"}},
	}
	tmpl.URIs = []*url.URL{{Scheme: "spiffe", Host: "modelrelay", Path: "/agent/" + url.PathEscape(cn)}}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	return keyPEM, csrPEM, nil
}

// ParseCSR 解析 PEM 编码的证书请求。
func ParseCSR(pemBytes []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("certs: invalid CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parse csr: %w", err)
	}
	return csr, nil
}

// ValidateAgentCSR 校验 Agent CSR：签名、CN 与 node_id、URI SAN。
func ValidateAgentCSR(csr *x509.CertificateRequest, nodeID string) error {
	if csr == nil {
		return fmt.Errorf("certs: csr is nil")
	}
	if strings.TrimSpace(nodeID) == "" {
		return fmt.Errorf("certs: node id is required")
	}
	if err := csr.CheckSignature(); err != nil {
		return fmt.Errorf("certs: csr signature invalid: %w", err)
	}
	if csr.Subject.CommonName != nodeID {
		return fmt.Errorf("certs: csr CN %q does not match requested node id %q", csr.Subject.CommonName, nodeID)
	}
	want := AgentIdentityURI(nodeID)
	if len(csr.URIs) != 1 || csr.URIs[0] == nil || csr.URIs[0].String() != want {
		var got []string
		for _, u := range csr.URIs {
			if u != nil {
				got = append(got, u.String())
			}
		}
		return fmt.Errorf("certs: csr URI SAN %v does not match %s", got, want)
	}
	return nil
}

// IssueFromCSR 使用 CA 为 CSR 签发证书（证书管理机上执行）。
func (c *CA) IssueFromCSR(csrPEM []byte, cn string, days int) ([]byte, error) {
	if days <= 0 {
		return nil, fmt.Errorf("certs: validity days must be positive")
	}
	csr, err := ParseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	if err := ValidateAgentCSR(csr, cn); err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      csr.Subject,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, days),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     csr.DNSNames,
		URIs:         csr.URIs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, csr.PublicKey, c.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// IssueServerCert 为 Relay 签发服务端证书（供测试与私有部署使用；公网部署建议使用公共 CA）。
func (c *CA) IssueServerCert(cn string, ips []net.IP, dnsNames []string, days int) (certPEM, keyPEM []byte, err error) {
	if days <= 0 {
		return nil, nil, fmt.Errorf("certs: validity days must be positive")
	}
	if strings.TrimSpace(cn) == "" {
		return nil, nil, fmt.Errorf("certs: server common name is required")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"ModelRelay"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(0, 0, days),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, &key.PublicKey, c.Key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}

// VerifyClientCert 校验客户端证书：CA 签发、未过期、CN 与 node_id 一致。
func (c *CA) VerifyClientCert(certPEM []byte, nodeID string) error {
	cert, err := ParseCert(certPEM)
	if err != nil {
		return err
	}
	opts := x509.VerifyOptions{
		Roots:     c.Pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		return fmt.Errorf("certs: verify client cert: %w", err)
	}
	if !identityMatches(cert, nodeID) {
		return fmt.Errorf("certs: identity mismatch: cert CN %q != node_id %q", cert.Subject.CommonName, nodeID)
	}
	return nil
}

// AgentIdentityURI 是 Agent 客户端证书使用的 URI SAN 身份。
func AgentIdentityURI(nodeID string) string {
	return "spiffe://modelrelay/agent/" + url.PathEscape(nodeID)
}

func identityMatches(cert *x509.Certificate, nodeID string) bool {
	if len(cert.URIs) > 0 {
		for _, u := range cert.URIs {
			if u != nil && u.String() == AgentIdentityURI(nodeID) {
				return cert.Subject.CommonName == nodeID
			}
		}
		return false
	}
	// 兼容升级前仅使用 CN 的证书；新签发证书始终带 URI SAN。
	return cert.Subject.CommonName == nodeID && !strings.Contains(nodeID, "\x00")
}

// ParseCert 解析 PEM 证书。
func ParseCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("certs: invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parse certificate: %w", err)
	}
	return cert, nil
}

// ParsePrivateKey 解析 RSA 私钥（PKCS1 或 PKCS8）。
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("certs: invalid private key PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	return nil, fmt.Errorf("certs: unsupported private key format")
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
