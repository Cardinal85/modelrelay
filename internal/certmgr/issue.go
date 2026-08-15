package certmgr

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"modelrelay/internal/certs"
)

// CSRInfo 是导入 CSR 后展示给用户的元数据。
type CSRInfo struct {
	CommonName string
	NodeID     string
	URIs       []string
	DNSNames   []string
}

// ReadCSR 读取并校验 Agent CSR。nodeID 为空时使用 CSR CN。
func ReadCSR(path, nodeID string) (*CSRInfo, []byte, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := InspectCSR(pemBytes, nodeID)
	if err != nil {
		return nil, nil, err
	}
	return info, pemBytes, nil
}

// InspectCSR 解析 CSR 并校验签名、CN 与 URI SAN。
func InspectCSR(csrPEM []byte, nodeID string) (*CSRInfo, error) {
	csr, err := certs.ParseCSR(csrPEM)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(nodeID)
	if id == "" {
		id = csr.Subject.CommonName
	}
	if err := certs.ValidateAgentCSR(csr, id); err != nil {
		return nil, err
	}
	info := &CSRInfo{
		CommonName: csr.Subject.CommonName,
		NodeID:     id,
		DNSNames:   append([]string{}, csr.DNSNames...),
	}
	for _, u := range csr.URIs {
		if u != nil {
			info.URIs = append(info.URIs, u.String())
		}
	}
	return info, nil
}

// IssueAgentOptions 签发 Agent 证书的参数。
type IssueAgentOptions struct {
	CSRPEM  []byte
	CSRPath string
	NodeID  string
	Days    int
	OutDir  string
}

// IssueAgentResult 是 Agent 签发结果。
type IssueAgentResult struct {
	NodeID  string
	CertPEM []byte
	Path    string
	Info    *InspectInfo
}

// IssueAgent 使用 Agent CA 签发客户端证书。不生成 Agent 私钥。
func (w *Workspace) IssueAgent(opt IssueAgentOptions) (*IssueAgentResult, error) {
	if w == nil || w.CA == nil {
		return nil, fmt.Errorf("certmgr: Agent CA workspace is not open")
	}
	if w.Kind != KindAgent {
		return nil, fmt.Errorf("certmgr: Agent certificates must be issued by an Agent CA")
	}
	csrPEM := opt.CSRPEM
	if len(csrPEM) == 0 {
		if strings.TrimSpace(opt.CSRPath) == "" {
			return nil, fmt.Errorf("certmgr: CSR is required")
		}
		b, err := os.ReadFile(opt.CSRPath)
		if err != nil {
			return nil, err
		}
		csrPEM = b
	}
	days := opt.Days
	if days <= 0 {
		return nil, fmt.Errorf("certmgr: validity days must be positive")
	}
	info, err := InspectCSR(csrPEM, opt.NodeID)
	if err != nil {
		return nil, err
	}
	certPEM, err := w.CA.IssueFromCSR(csrPEM, info.NodeID, days)
	if err != nil {
		return nil, err
	}
	certInfo, err := Inspect(certPEM)
	if err != nil {
		return nil, err
	}
	result := &IssueAgentResult{
		NodeID:  info.NodeID,
		CertPEM: certPEM,
		Info:    certInfo,
	}
	if strings.TrimSpace(opt.OutDir) != "" {
		if err := mkdirSecure(opt.OutDir); err != nil {
			return nil, err
		}
		result.Path = filepath.Join(opt.OutDir, info.NodeID+".crt")
		if err := writePublicFile(result.Path, certPEM); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// IssueRelayOptions 签发 Relay 服务端证书的参数。
type IssueRelayOptions struct {
	CN       string
	DNSNames []string
	IPs      []string
	Days     int
	OutDir   string
}

// IssueRelayResult 是 Relay 签发结果。
type IssueRelayResult struct {
	CertPEM  []byte
	KeyPEM   []byte
	CertPath string
	KeyPath  string
	Info     *InspectInfo
}

// IssueRelay 使用 Relay CA 签发服务端证书和私钥。
func (w *Workspace) IssueRelay(opt IssueRelayOptions) (*IssueRelayResult, error) {
	if w == nil || w.CA == nil {
		return nil, fmt.Errorf("certmgr: Relay CA workspace is not open")
	}
	if w.Kind != KindRelay {
		return nil, fmt.Errorf("certmgr: Relay server certificates must be issued by a Relay CA")
	}
	cn := strings.TrimSpace(opt.CN)
	if cn == "" {
		return nil, fmt.Errorf("certmgr: server common name is required")
	}
	if opt.Days <= 0 {
		return nil, fmt.Errorf("certmgr: validity days must be positive")
	}
	var dnsNames []string
	for _, n := range opt.DNSNames {
		n = strings.TrimSpace(n)
		if n != "" {
			dnsNames = append(dnsNames, n)
		}
	}
	var ips []net.IP
	for _, s := range opt.IPs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, fmt.Errorf("certmgr: invalid IP SAN %q", s)
		}
		ips = append(ips, ip)
	}
	if len(dnsNames) == 0 && len(ips) == 0 {
		return nil, fmt.Errorf("certmgr: at least one DNS or IP SAN is required")
	}
	certPEM, keyPEM, err := w.CA.IssueServerCert(cn, ips, dnsNames, opt.Days)
	if err != nil {
		return nil, err
	}
	info, err := Inspect(certPEM)
	if err != nil {
		return nil, err
	}
	result := &IssueRelayResult{CertPEM: certPEM, KeyPEM: keyPEM, Info: info}
	if strings.TrimSpace(opt.OutDir) != "" {
		if err := mkdirSecure(opt.OutDir); err != nil {
			return nil, err
		}
		base := filepath.Join(opt.OutDir, sanitizeFileName(cn))
		result.CertPath = base + ".crt"
		result.KeyPath = base + ".key"
		if err := writePublicFile(result.CertPath, certPEM); err != nil {
			return nil, err
		}
		if err := writePrivateFile(result.KeyPath, keyPEM); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "relay"
	}
	var b strings.Builder
	for _, r := range name {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
