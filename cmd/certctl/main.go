// certctl 是 ModelRelay 的证书管理工具。
//
// 用法：
//
//	certctl init-ca  -out <dir> [-cn <name>] [-days 3650]     # 生成 CA（离线机器）
//	certctl csr      -cn <node_id> -out <dir>                 # Agent 本地生成私钥+CSR
//	certctl issue    -ca <crt> -ca-key <key> -csr <csr> -cn <node_id> [-days 365]
//	certctl inspect  -cert <pem>                              # 查看证书信息
//	certctl version
package main

import (
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modelrelay/internal/certs"
	"modelrelay/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "-version", "--version":
		fmt.Printf("%s certctl %s (go %s)\n", version.Name, version.ToolVersion(), version.GoVersion())
	case "init-ca":
		cmdInitCA(os.Args[2:])
	case "csr":
		cmdCSR(os.Args[2:])
	case "issue":
		cmdIssue(os.Args[2:])
	case "server-cert":
		cmdServerCert(os.Args[2:])
	case "inspect":
		cmdInspect(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "certctl: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ModelRelay certctl — 证书管理工具

用法:
  certctl init-ca -out <dir> [-cn <name>] [-days 3650]
  certctl csr -cn <node_id> -out <dir>
  certctl issue -ca <ca.crt> -ca-key <ca.key> -csr <node.csr> -cn <node_id> [-days 365]
  certctl server-cert -ca <ca.crt> -ca-key <ca.key> [-cn relay] [-ip 1.2.3.4] [-dns relay.example.com] [-days 365] -out <dir>
  certctl inspect -cert <cert.pem>
  certctl version

私钥原则: Agent 私钥只在 Agent 机器本地生成 (csr)；CA 私钥只在证书管理机 (init-ca/issue)。
`)
}

func cmdInitCA(args []string) {
	fs := flag.NewFlagSet("init-ca", flag.ExitOnError)
	out := fs.String("out", ".", "output directory")
	cn := fs.String("cn", "ModelRelay Agent CA", "CA common name")
	days := fs.Int("days", 3650, "validity days")
	fs.Parse(args)

	certPEM, keyPEM, err := certs.CreateCA(*cn, *days)
	if err != nil {
		fatal("init-ca", err)
	}
	if err := writeFile(filepath.Join(*out, "agent-ca.crt"), certPEM); err != nil {
		fatal("init-ca", err)
	}
	if err := writeFile(filepath.Join(*out, "agent-ca.key"), keyPEM); err != nil {
		fatal("init-ca", err)
	}
	fmt.Printf("CA written: %s, %s (keep agent-ca.key offline!)\n",
		filepath.Join(*out, "agent-ca.crt"), filepath.Join(*out, "agent-ca.key"))
}

func cmdCSR(args []string) {
	fs := flag.NewFlagSet("csr", flag.ExitOnError)
	out := fs.String("out", ".", "output directory")
	cn := fs.String("cn", "", "node_id (required)")
	fs.Parse(args)
	if *cn == "" {
		fatal("csr", fmt.Errorf("-cn <node_id> is required"))
	}

	keyPEM, csrPEM, err := certs.GenerateCSR(*cn)
	if err != nil {
		fatal("csr", err)
	}
	base := filepath.Join(*out, *cn)
	if err := writeFile(base+".key", keyPEM); err != nil {
		fatal("csr", err)
	}
	if err := writeFile(base+".csr", csrPEM); err != nil {
		fatal("csr", err)
	}
	fmt.Printf("private key and CSR written: %s.key, %s.csr (key stays on this machine)\n", base, base)
}

func cmdIssue(args []string) {
	fs := flag.NewFlagSet("issue", flag.ExitOnError)
	caPath := fs.String("ca", "", "CA cert PEM")
	caKey := fs.String("ca-key", "", "CA private key PEM")
	csrPath := fs.String("csr", "", "CSR PEM")
	cn := fs.String("cn", "", "node_id the cert is issued for (must match CSR CN)")
	days := fs.Int("days", 365, "validity days")
	out := fs.String("out", ".", "output directory")
	fs.Parse(args)

	if *caPath == "" || *caKey == "" || *csrPath == "" || *cn == "" {
		fatal("issue", fmt.Errorf("-ca, -ca-key, -csr and -cn are required"))
	}
	ca, err := certs.LoadCAFiles(*caPath, *caKey)
	if err != nil {
		fatal("issue", err)
	}
	csrPEM, err := os.ReadFile(*csrPath)
	if err != nil {
		fatal("issue", err)
	}
	certPEM, err := ca.IssueFromCSR(csrPEM, *cn, *days)
	if err != nil {
		fatal("issue", err)
	}
	cert, err := certs.ParseCert(certPEM)
	if err != nil {
		fatal("issue", err)
	}
	outPath := filepath.Join(*out, *cn+".crt")
	if err := writeFile(outPath, certPEM); err != nil {
		fatal("issue", err)
	}
	fmt.Printf("issued %s (serial %x, expires %s)\n", outPath, cert.SerialNumber, cert.NotAfter.Format(time.RFC3339))
}

func cmdServerCert(args []string) {
	fs := flag.NewFlagSet("server-cert", flag.ExitOnError)
	caPath := fs.String("ca", "", "CA cert PEM")
	caKey := fs.String("ca-key", "", "CA private key PEM")
	cn := fs.String("cn", "relay", "server common name")
	ip := fs.String("ip", "", "IP SAN (comma separated)")
	dns := fs.String("dns", "", "DNS SAN (comma separated)")
	days := fs.Int("days", 365, "validity days")
	out := fs.String("out", ".", "output directory")
	fs.Parse(args)

	if *caPath == "" || *caKey == "" {
		fatal("server-cert", fmt.Errorf("-ca and -ca-key are required"))
	}
	ca, err := certs.LoadCAFiles(*caPath, *caKey)
	if err != nil {
		fatal("server-cert", err)
	}
	var ips []net.IP
	for _, s := range strings.Split(*ip, ",") {
		if s = strings.TrimSpace(s); s != "" {
			ips = append(ips, net.ParseIP(s))
		}
	}
	var dnsNames []string
	for _, s := range strings.Split(*dns, ",") {
		if s = strings.TrimSpace(s); s != "" {
			dnsNames = append(dnsNames, s)
		}
	}
	certPEM, keyPEM, err := ca.IssueServerCert(*cn, ips, dnsNames, *days)
	if err != nil {
		fatal("server-cert", err)
	}
	base := filepath.Join(*out, *cn)
	if err := writeFile(base+".crt", certPEM); err != nil {
		fatal("server-cert", err)
	}
	if err := writeFile(base+".key", keyPEM); err != nil {
		fatal("server-cert", err)
	}
	fmt.Printf("server cert written: %s.crt, %s.key\n", base, base)
}

func cmdInspect(args []string) {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	certPath := fs.String("cert", "", "certificate PEM path")
	fs.Parse(args)
	if *certPath == "" {
		fatal("inspect", fmt.Errorf("-cert is required"))
	}
	pemBytes, err := os.ReadFile(*certPath)
	if err != nil {
		fatal("inspect", err)
	}
	cert, err := certs.ParseCert(pemBytes)
	if err != nil {
		fatal("inspect", err)
	}
	fmt.Printf("Subject: %s\n", cert.Subject.String())
	fmt.Printf("Issuer:  %s\n", cert.Issuer.String())
	fmt.Printf("Serial:  %x\n", cert.SerialNumber)
	fmt.Printf("NotBefore: %s\n", cert.NotBefore.Format(time.RFC3339))
	fmt.Printf("NotAfter:  %s\n", cert.NotAfter.Format(time.RFC3339))
	clientAuth := false
	for _, ku := range cert.ExtKeyUsage {
		if ku == x509.ExtKeyUsageClientAuth {
			clientAuth = true
			break
		}
	}
	fmt.Printf("IsCA: %v  ClientAuth: %v\n", cert.IsCA, clientAuth)
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func fatal(cmd string, err error) {
	fmt.Fprintf(os.Stderr, "certctl %s: %v\n", cmd, err)
	os.Exit(1)
}
