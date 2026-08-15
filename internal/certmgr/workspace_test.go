package certmgr

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateAndOpenWorkspace(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	ws, err := CreateWorkspace(CreateOptions{
		Dir:  agentDir,
		Kind: KindAgent,
		CN:   "Test Agent CA",
		Days: 365,
	})
	if err != nil {
		t.Fatalf("create agent ca: %v", err)
	}
	if ws.Info == nil || ws.Info.IsCA != true {
		t.Fatalf("expected CA cert, got %+v", ws.Info)
	}
	if _, err := os.Stat(ws.KeyPath()); err != nil {
		t.Fatalf("agent ca key: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(agentDir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if st.Mode().Perm() != 0o700 {
			t.Fatalf("workspace dir mode = %o", st.Mode().Perm())
		}
		kst, err := os.Stat(ws.KeyPath())
		if err != nil {
			t.Fatalf("stat key: %v", err)
		}
		if kst.Mode().Perm() != 0o600 {
			t.Fatalf("ca key mode = %o", kst.Mode().Perm())
		}
	}

	opened, err := OpenWorkspace(agentDir, KindAgent)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.Info.Subject != ws.Info.Subject {
		t.Fatalf("subject mismatch: %s vs %s", opened.Info.Subject, ws.Info.Subject)
	}
	if _, err := CreateWorkspace(CreateOptions{Dir: agentDir, Kind: KindAgent, CN: "x", Days: 10}); err == nil {
		t.Fatal("overwrite existing CA key must be rejected")
	}
}

func TestRelayWorkspaceLegacyCertctlNames(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "relay")
	if _, err := CreateWorkspace(CreateOptions{Dir: dir, Kind: KindRelay, CN: "Relay CA", Days: 100}); err != nil {
		t.Fatalf("create: %v", err)
	}
	cert, key := CAPaths(dir, KindRelay)
	if filepath.Base(cert) != "relay-ca.crt" || filepath.Base(key) != "relay-ca.key" {
		t.Fatalf("unexpected relay ca names: %s %s", cert, key)
	}
	ws, err := OpenWorkspace(dir, KindRelay)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if ws.Kind != KindRelay {
		t.Fatalf("kind = %s", ws.Kind)
	}

	legacyDir := filepath.Join(root, "legacy")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cert, filepath.Join(legacyDir, "agent-ca.crt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(key, filepath.Join(legacyDir, "agent-ca.key")); err != nil {
		t.Fatal(err)
	}
	legacy, err := OpenWorkspace(legacyDir, KindRelay)
	if err != nil {
		t.Fatalf("open legacy certctl names: %v", err)
	}
	if legacy.Info == nil || !legacy.Info.IsCA {
		t.Fatal("legacy relay ca not loaded")
	}
}

func TestCreateWorkspaceRejectsEmptyDir(t *testing.T) {
	if _, err := CreateWorkspace(CreateOptions{Kind: KindAgent, CN: "x", Days: 10}); err == nil {
		t.Fatal("empty dir must fail")
	}
}
