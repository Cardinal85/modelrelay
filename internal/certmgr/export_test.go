package certmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modelrelay/internal/certs"
)

func TestExportRelayAndAgentBoundaries(t *testing.T) {
	root := t.TempDir()
	agentWS, err := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "agent-ca"), Kind: KindAgent, Days: 365})
	if err != nil {
		t.Fatal(err)
	}
	relayWS, err := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "relay-ca"), Kind: KindRelay, Days: 365})
	if err != nil {
		t.Fatal(err)
	}
	_, csrPEM, err := certs.GenerateCSR("gpu-001")
	if err != nil {
		t.Fatal(err)
	}
	issued := filepath.Join(root, "issued")
	agentCert, err := agentWS.IssueAgent(IssueAgentOptions{CSRPEM: csrPEM, NodeID: "gpu-001", Days: 30, OutDir: issued})
	if err != nil {
		t.Fatal(err)
	}
	relayCert, err := relayWS.IssueRelay(IssueRelayOptions{
		CN: "relay.example.com", DNSNames: []string{"relay.example.com"}, Days: 30, OutDir: issued,
	})
	if err != nil {
		t.Fatal(err)
	}

	relayOut := filepath.Join(root, "export-relay")
	if err := ExportRelay(RelayExportInput{
		ServerCertPath: relayCert.CertPath,
		ServerKeyPath:  relayCert.KeyPath,
		AgentCAPath:    agentWS.CertPath(),
		DestDir:        relayOut,
	}); err != nil {
		t.Fatalf("export relay: %v", err)
	}
	for _, name := range []string{"relay.crt", "relay.key", "agent-ca.crt", "README.txt"} {
		if _, err := os.Stat(filepath.Join(relayOut, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(relayOut, "agent-ca.key")); err == nil {
		t.Fatal("relay export must not include agent-ca.key")
	}

	agentOut := filepath.Join(root, "export-agent")
	if err := ExportAgent(AgentExportInput{
		AgentCertPath: agentCert.Path,
		RelayCAPath:   relayWS.CertPath(),
		DestDir:       agentOut,
		NodeID:        "gpu-001",
	}); err != nil {
		t.Fatalf("export agent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentOut, "gpu-001.crt")); err != nil {
		t.Fatalf("agent cert: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agentOut, "relay-ca.crt")); err != nil {
		t.Fatalf("relay ca: %v", err)
	}
	entries, _ := os.ReadDir(agentOut)
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".key") {
			t.Fatalf("agent export must not contain key: %s", e.Name())
		}
	}
	readme, _ := os.ReadFile(filepath.Join(agentOut, "README.txt"))
	if !strings.Contains(string(readme), "私钥必须由 GPU 主机本地") {
		t.Fatalf("missing private-key warning: %s", readme)
	}
}

func TestExportRejectsCAKeys(t *testing.T) {
	root := t.TempDir()
	agentWS, _ := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "agent"), Kind: KindAgent, Days: 10})
	relayWS, _ := CreateWorkspace(CreateOptions{Dir: filepath.Join(root, "relay"), Kind: KindRelay, Days: 10})
	dummy := filepath.Join(root, "dummy.crt")
	_ = os.WriteFile(dummy, []byte("x"), 0o644)

	err := ExportRelay(RelayExportInput{
		ServerCertPath: dummy,
		ServerKeyPath:  agentWS.KeyPath(),
		AgentCAPath:    dummy,
		DestDir:        filepath.Join(root, "bad-relay"),
	})
	if err != ErrCAKeyNotExported {
		t.Fatalf("want ErrCAKeyNotExported, got %v", err)
	}

	err = ExportAgent(AgentExportInput{
		AgentCertPath: agentWS.KeyPath(),
		RelayCAPath:   relayWS.CertPath(),
		DestDir:       filepath.Join(root, "bad-agent"),
	})
	if err != ErrAgentKeyNotExported {
		t.Fatalf("want ErrAgentKeyNotExported, got %v", err)
	}

	err = ExportAgent(AgentExportInput{
		AgentCertPath: dummy,
		RelayCAPath:   relayWS.KeyPath(),
		DestDir:       filepath.Join(root, "bad-agent2"),
	})
	if err != ErrCAKeyNotExported {
		t.Fatalf("want ErrCAKeyNotExported for relay ca key, got %v", err)
	}
}
