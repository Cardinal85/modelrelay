package certmgr

import (
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"modelrelay/internal/certs"
)

const expiringWindow = 30 * 24 * time.Hour

const (
	StatusValid    = "valid"
	StatusExpiring = "expiring"
	StatusExpired  = "expired"
)

// InspectInfo 是证书检查结果。
type InspectInfo struct {
	Subject     string
	Issuer      string
	Serial      string
	NotBefore   time.Time
	NotAfter    time.Time
	DNSNames    []string
	IPAddresses []string
	URIs        []string
	IsCA        bool
	ClientAuth  bool
	ServerAuth  bool
	Status      string
	Warning     string
}

// Inspect 解析证书并提示临期/过期。
func Inspect(pemBytes []byte) (*InspectInfo, error) {
	cert, err := certs.ParseCert(pemBytes)
	if err != nil {
		return nil, err
	}
	info := &InspectInfo{
		Subject:   cert.Subject.String(),
		Issuer:    cert.Issuer.String(),
		Serial:    fmt.Sprintf("%x", cert.SerialNumber),
		NotBefore: cert.NotBefore,
		NotAfter:  cert.NotAfter,
		DNSNames:  append([]string{}, cert.DNSNames...),
		IsCA:      cert.IsCA,
	}
	for _, ip := range cert.IPAddresses {
		if ip != nil {
			info.IPAddresses = append(info.IPAddresses, ip.String())
		}
	}
	for _, u := range cert.URIs {
		if u != nil {
			info.URIs = append(info.URIs, u.String())
		}
	}
	for _, ku := range cert.ExtKeyUsage {
		switch ku {
		case x509.ExtKeyUsageClientAuth:
			info.ClientAuth = true
		case x509.ExtKeyUsageServerAuth:
			info.ServerAuth = true
		}
	}
	now := time.Now()
	switch {
	case now.After(cert.NotAfter):
		info.Status = StatusExpired
		info.Warning = "证书已过期"
	case cert.NotAfter.Sub(now) <= expiringWindow:
		info.Status = StatusExpiring
		info.Warning = "证书将在 30 天内过期"
	default:
		info.Status = StatusValid
	}
	return info, nil
}

// InspectFile 读取并检查 PEM 证书文件。
func InspectFile(path string) (*InspectInfo, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Inspect(pemBytes)
}

// UsageSummary 返回用途摘要。
func (i *InspectInfo) UsageSummary() string {
	var parts []string
	if i.IsCA {
		parts = append(parts, "CA")
	}
	if i.ClientAuth {
		parts = append(parts, "ClientAuth")
	}
	if i.ServerAuth {
		parts = append(parts, "ServerAuth")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

// Text 返回适合 GUI 展示的检查文本。
func (i *InspectInfo) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject: %s\n", i.Subject)
	fmt.Fprintf(&b, "Issuer: %s\n", i.Issuer)
	fmt.Fprintf(&b, "Serial: %s\n", i.Serial)
	fmt.Fprintf(&b, "NotBefore: %s\n", i.NotBefore.Format(time.RFC3339))
	fmt.Fprintf(&b, "NotAfter: %s\n", i.NotAfter.Format(time.RFC3339))
	fmt.Fprintf(&b, "DNS SAN: %s\n", joinOrDash(i.DNSNames))
	fmt.Fprintf(&b, "IP SAN: %s\n", joinOrDash(i.IPAddresses))
	fmt.Fprintf(&b, "URI SAN: %s\n", joinOrDash(i.URIs))
	fmt.Fprintf(&b, "Usage: %s\n", i.UsageSummary())
	fmt.Fprintf(&b, "Status: %s\n", i.Status)
	if i.Warning != "" {
		fmt.Fprintf(&b, "Warning: %s\n", i.Warning)
	}
	return b.String()
}

func joinOrDash(v []string) string {
	if len(v) == 0 {
		return "-"
	}
	return strings.Join(v, ", ")
}
