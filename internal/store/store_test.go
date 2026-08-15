package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateAndVersion(t *testing.T) {
	s := openTest(t)
	v, err := s.Version()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v < 1 {
		t.Fatalf("version=%d", v)
	}
}

func TestAdminUserLifecycle(t *testing.T) {
	s := openTest(t)
	if err := s.EnsureAdmin("admin", "secret123", "admin"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.EnsureAdmin("admin", "secret123", "admin"); err != nil {
		t.Fatalf("ensure idempotent: %v", err)
	}
	u, err := s.GetAdminUser("admin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if u.Role != "admin" {
		t.Fatalf("role=%s", u.Role)
	}
	if !CheckPassword(u.PasswordHash, "secret123") {
		t.Fatal("password mismatch")
	}
	if CheckPassword(u.PasswordHash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	users, err := s.ListAdminUsers()
	if err != nil || len(users) != 1 {
		t.Fatalf("list: %v %d", err, len(users))
	}
}

func TestCertMetaLifecycle(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	c := CertMeta{
		NodeID: "gpu-1", Serial: "AA11", Subject: "CN=gpu-1",
		Issuer: "CN=ca", NotBefore: now, NotAfter: now.AddDate(0, 0, 30),
		Status: "active", Fingerprint: "abc",
	}
	if err := s.AddCertMeta(c); err != nil {
		t.Fatalf("add: %v", err)
	}
	// 重复 serial 必须失败（唯一约束）。
	if err := s.AddCertMeta(c); err == nil {
		t.Fatal("duplicate serial must fail")
	}
	n, err := s.CertExpiring(60)
	if err != nil || n != 1 {
		t.Fatalf("expiring: %d %v", n, err)
	}
	if err := s.RevokeCert("AA11", "compromised"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	certs, err := s.ListCerts()
	if err != nil || len(certs) != 1 {
		t.Fatalf("list: %v %d", err, len(certs))
	}
	if certs[0].Status != "revoked" || certs[0].RevokeReason != "compromised" {
		t.Fatalf("status=%s reason=%s", certs[0].Status, certs[0].RevokeReason)
	}
	// 吊销后不应计入临期。
	n, _ = s.CertExpiring(60)
	if n != 0 {
		t.Fatalf("revoked cert counted as expiring: %d", n)
	}
}

func TestAuditAndRequests(t *testing.T) {
	s := openTest(t)
	if err := s.AddAudit("admin", "drain", "gpu-1", "grace=300"); err != nil {
		t.Fatalf("audit: %v", err)
	}
	audits, err := s.ListAudit(10)
	if err != nil || len(audits) != 1 {
		t.Fatalf("audit list: %v %d", err, len(audits))
	}
	if audits[0].Action != "drain" || audits[0].Target != "gpu-1" {
		t.Fatalf("audit: %+v", audits[0])
	}

	if err := s.AddRequestSummary(RequestSummary{
		RequestID: "r1", Path: "/v1/chat/completions", Model: "m", Node: "gpu-1",
		Status: 200, TTFTMs: 12, DurationMs: 100,
	}); err != nil {
		t.Fatalf("req: %v", err)
	}
	reqs, err := s.ListRequestSummaries(10)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("req list: %v %d", err, len(reqs))
	}
	if reqs[0].RequestID != "r1" || reqs[0].Status != 200 {
		t.Fatalf("req: %+v", reqs[0])
	}

	sum, err := s.Summary()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if sum["requests"] != 1 || sum["audit"] != 1 {
		t.Fatalf("summary: %+v", sum)
	}

	// 保留期清理。
	if err := s.PruneRequestSummaries(1); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if err := s.PruneAudit(1); err != nil {
		t.Fatalf("prune audit: %v", err)
	}
}

func TestHashPassword(t *testing.T) {
	h, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if h == "x" || h == "" {
		t.Fatal("hash must be bcrypt")
	}
}
