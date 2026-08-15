package certs

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

func TestCAIssueVerifyLifecycle(t *testing.T) {
	// CA
	caCert, caKey, err := CreateCA("test-ca", 365)
	if err != nil {
		t.Fatalf("create ca: %v", err)
	}
	ca, err := LoadCA(caCert, caKey)
	if err != nil {
		t.Fatalf("load ca: %v", err)
	}

	// Agent CSR + 签发
	keyPEM, csrPEM, err := GenerateCSR("node-42")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	certPEM, err := ca.IssueFromCSR(csrPEM, "node-42", 90)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := ca.VerifyClientCert(certPEM, "node-42"); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// 身份不匹配必须拒绝
	if err := ca.VerifyClientCert(certPEM, "node-43"); err == nil {
		t.Fatal("identity mismatch must fail")
	}

	// 私钥可用
	if _, err := ParsePrivateKey(keyPEM); err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// 证书有效期
	cert, err := ParseCert(certPEM)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	if len(cert.URIs) != 1 || cert.URIs[0].String() != AgentIdentityURI("node-42") {
		t.Fatalf("missing agent URI identity: %v", cert.URIs)
	}
	if cert.NotAfter.Before(time.Now().AddDate(0, 0, 89)) {
		t.Fatalf("cert validity too short: %s", cert.NotAfter)
	}
}

func TestCSRCNMismatchRejected(t *testing.T) {
	caCert, caKey, _ := CreateCA("test-ca", 365)
	ca, _ := LoadCA(caCert, caKey)
	_, csrPEM, _ := GenerateCSR("node-1")
	if _, err := ca.IssueFromCSR(csrPEM, "node-2", 30); err == nil {
		t.Fatal("CSR CN mismatch must be rejected")
	}
}

func TestWrongCASignatureRejected(t *testing.T) {
	ca1Cert, ca1Key, _ := CreateCA("ca-1", 365)
	ca1, _ := LoadCA(ca1Cert, ca1Key)
	_, csrPEM, _ := GenerateCSR("node-1")
	_, _ = ca1.IssueFromCSR(csrPEM, "node-1", 30) // 由 ca1 签发

	ca2Cert, ca2Key, _ := CreateCA("ca-2", 365)
	ca2, _ := LoadCA(ca2Cert, ca2Key)
	keyPEM, csrPEM2, _ := GenerateCSR("node-1")
	certPEM, _ := ca2.IssueFromCSR(csrPEM2, "node-1", 30)

	if err := ca1.VerifyClientCert(certPEM, "node-1"); err == nil {
		t.Fatal("cert signed by wrong CA must fail verification")
	}
	_ = keyPEM
}

func TestServerCertSANs(t *testing.T) {
	caCert, caKey, _ := CreateCA("test-ca", 365)
	ca, _ := LoadCA(caCert, caKey)
	certPEM, _, err := ca.IssueServerCert("relay", []net.IP{net.ParseIP("127.0.0.1")}, []string{"relay.example.com"}, 365)
	if err != nil {
		t.Fatalf("server cert: %v", err)
	}
	cert, err := ParseCert(certPEM)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "127.0.0.1" {
		t.Fatalf("ip sans: %v", cert.IPAddresses)
	}
	serverAuth := false
	for _, ku := range cert.ExtKeyUsage {
		if ku == x509.ExtKeyUsageServerAuth {
			serverAuth = true
		}
	}
	if !serverAuth {
		t.Fatal("missing server auth usage")
	}
	// 作为服务端校验
	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	opts := x509.VerifyOptions{
		Roots:     pool,
		DNSName:   "relay.example.com",
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		t.Fatalf("server verify: %v", err)
	}
}

func TestCreateCAPEMBlocks(t *testing.T) {
	certPEM, keyPEM, _ := CreateCA("test-ca", 10)
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		t.Fatal("bad cert PEM")
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil || keyBlock.Type != "RSA PRIVATE KEY" {
		t.Fatal("bad key PEM")
	}
}
