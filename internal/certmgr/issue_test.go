package certmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modelrelay/internal/certs"
)

func TestIssueAgentAndInspect(t *testing.T) {
	root := t.TempDir()
	agentWS, err := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "agent"), Kind: KindAgent, Days: 365})
	if err != nil {
		t.Fatalf("agent ca: %v", err)
	}
	keyPEM, csrPEM, err := certs.GenerateCSR("gpu-001")
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	csrPath := filepath.Join(root, "gpu-001.csr")
	if err := os.WriteFile(csrPath, csrPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _, err := ReadCSR(csrPath, "")
	if err != nil {
		t.Fatalf("read csr: %v", err)
	}
	if info.NodeID != "gpu-001" || info.CommonName != "gpu-001" {
		t.Fatalf("csr info: %+v", info)
	}
	if len(info.URIs) != 1 || info.URIs[0] != certs.AgentIdentityURI("gpu-001") {
		t.Fatalf("uri: %v", info.URIs)
	}

	issuedDir := filepath.Join(root, "issued")
	result, err := agentWS.IssueAgent(IssueAgentOptions{
		CSRPath: csrPath,
		NodeID:  "gpu-001",
		Days:    90,
		OutDir:  issuedDir,
	})
	if err != nil {
		t.Fatalf("issue agent: %v", err)
	}
	if result.Info.Status != StatusValid {
		t.Fatalf("status = %s warning=%s", result.Info.Status, result.Info.Warning)
	}
	if !result.Info.ClientAuth || result.Info.ServerAuth {
		t.Fatalf("usage: client=%v server=%v", result.Info.ClientAuth, result.Info.ServerAuth)
	}
	if len(result.Info.URIs) != 1 {
		t.Fatalf("issued uri: %v", result.Info.URIs)
	}
	text := result.Info.Text()
	if !strings.Contains(text, "gpu-001") || !strings.Contains(text, "ClientAuth") {
		t.Fatalf("inspect text: %s", text)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("issued file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "gpu-001.key"), keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestIssueAgentRejectsBadInputs(t *testing.T) {
	root := t.TempDir()
	agentWS, _ := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "agent"), Kind: KindAgent, Days: 365})
	relayWS, _ := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "relay"), Kind: KindRelay, Days: 365})
	_, csrPEM, _ := certs.GenerateCSR("gpu-001")

	if _, err := agentWS.IssueAgent(IssueAgentOptions{CSRPEM: csrPEM, NodeID: "gpu-001", Days: 0}); err == nil {
		t.Fatal("days=0 must fail")
	}
	if _, err := agentWS.IssueAgent(IssueAgentOptions{CSRPEM: csrPEM, NodeID: "other", Days: 30}); err == nil {
		t.Fatal("node mismatch must fail")
	}
	if _, err := relayWS.IssueAgent(IssueAgentOptions{CSRPEM: csrPEM, NodeID: "gpu-001", Days: 30}); err == nil {
		t.Fatal("relay ca must not issue agent cert")
	}
	if _, err := InspectCSR([]byte("not-a-csr"), "gpu-001"); err == nil {
		t.Fatal("invalid csr must fail")
	}
}

func TestIssueRelaySANAndUsage(t *testing.T) {
	root := t.TempDir()
	relayWS, err := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "relay"), Kind: KindRelay, Days: 365})
	if err != nil {
		t.Fatalf("relay ca: %v", err)
	}
	out := filepath.Join(root, "issued")
	result, err := relayWS.IssueRelay(IssueRelayOptions{
		CN:       "relay.example.com",
		DNSNames: []string{"relay.example.com"},
		IPs:      []string{"203.0.113.10"},
		Days:     180,
		OutDir:   out,
	})
	if err != nil {
		t.Fatalf("issue relay: %v", err)
	}
	if !result.Info.ServerAuth || result.Info.ClientAuth {
		t.Fatalf("usage: %+v", result.Info)
	}
	if len(result.Info.DNSNames) != 1 || result.Info.DNSNames[0] != "relay.example.com" {
		t.Fatalf("dns: %v", result.Info.DNSNames)
	}
	if len(result.Info.IPAddresses) != 1 || result.Info.IPAddresses[0] != "203.0.113.10" {
		t.Fatalf("ip: %v", result.Info.IPAddresses)
	}
	if _, err := os.Stat(result.KeyPath); err != nil {
		t.Fatalf("server key: %v", err)
	}

	if _, err := relayWS.IssueRelay(IssueRelayOptions{CN: "relay", Days: 30}); err == nil {
		t.Fatal("missing SAN must fail")
	}
	if _, err := relayWS.IssueRelay(IssueRelayOptions{CN: "relay", IPs: []string{"not-an-ip"}, Days: 30}); err == nil {
		t.Fatal("bad IP must fail")
	}
	agentWS, _ := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "agent"), Kind: KindAgent, Days: 365})
	if _, err := agentWS.IssueRelay(IssueRelayOptions{CN: "relay", DNSNames: []string{"x"}, Days: 30}); err == nil {
		t.Fatal("agent ca must not issue relay cert")
	}
}

func TestInspectExpiredStatus(t *testing.T) {
	root := t.TempDir()
	ws, err := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "ca"), Kind: KindAgent, Days: 365})
	if err != nil {
		t.Fatal(err)
	}
	_, csrPEM, _ := certs.GenerateCSR("n1")
	result, err := ws.IssueAgent(IssueAgentOptions{CSRPEM: csrPEM, NodeID: "n1", Days: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Info.Status == StatusExpired {
		t.Fatal("fresh 1-day cert should not be expired")
	}
	if _, err := Inspect([]byte("nope")); err == nil {
		t.Fatal("invalid pem must fail")
	}
}
