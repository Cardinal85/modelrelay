package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AdminUser 是管理员账号。
type AdminUser struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

// HashPassword 计算 bcrypt 密码哈希。
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验密码。
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// CreateAdminUser 创建管理员账号（已存在则返回错误）。
func (s *Store) CreateAdminUser(username, plainPassword, role string) (*AdminUser, error) {
	hash, err := HashPassword(plainPassword)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(`INSERT INTO admin_users(username, password_hash, role, created_at) VALUES(?,?,?,?)`,
		username, hash, role, nowStr())
	if err != nil {
		return nil, fmt.Errorf("store: create admin user: %w", err)
	}
	id, _ := res.LastInsertId()
	return &AdminUser{ID: id, Username: username, PasswordHash: hash, Role: role, CreatedAt: time.Now()}, nil
}

// GetAdminUser 按用户名查询。
func (s *Store) GetAdminUser(username string) (*AdminUser, error) {
	var u AdminUser
	var created string
	err := s.db.QueryRow(`SELECT id, username, password_hash, role, created_at FROM admin_users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &created)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &u, nil
}

// ListAdminUsers 列出全部账号。
func (s *Store) ListAdminUsers() ([]AdminUser, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash, role, created_at FROM admin_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &created); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// EnsureAdmin 确保至少存在一个管理员账号（首次启动用配置初始化）。
func (s *Store) EnsureAdmin(username, plainPassword, role string) error {
	if _, err := s.GetAdminUser(username); err == nil {
		return nil
	}
	_, err := s.CreateAdminUser(username, plainPassword, role)
	return err
}

// CertMeta 是证书元数据（不含私钥）。
type CertMeta struct {
	ID           int64      `json:"id"`
	NodeID       string     `json:"node_id"`
	Serial       string     `json:"serial"`
	Subject      string     `json:"subject"`
	Issuer       string     `json:"issuer"`
	NotBefore    time.Time  `json:"not_before"`
	NotAfter     time.Time  `json:"not_after"`
	Status       string     `json:"status"` // active | revoked | expired
	Fingerprint  string     `json:"fingerprint"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason string     `json:"revoke_reason,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// AddCertMeta 记录证书元数据。
func (s *Store) AddCertMeta(c CertMeta) error {
	_, err := s.db.Exec(`INSERT INTO certs(node_id, serial, subject, issuer, not_before, not_after, status, fingerprint, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		c.NodeID, c.Serial, c.Subject, c.Issuer,
		c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339),
		c.Status, c.Fingerprint, nowStr())
	return err
}

// EnsureCertMeta 记录证书元数据；同一序列号重复上线时保持原记录。
func (s *Store) EnsureCertMeta(c CertMeta) error {
	_, err := s.db.Exec(`INSERT INTO certs(node_id, serial, subject, issuer, not_before, not_after, status, fingerprint, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(serial) DO NOTHING`,
		c.NodeID, c.Serial, c.Subject, c.Issuer,
		c.NotBefore.UTC().Format(time.RFC3339), c.NotAfter.UTC().Format(time.RFC3339),
		c.Status, c.Fingerprint, nowStr())
	return err
}

// IsCertRevoked 返回证书是否已被管理台吊销。
func (s *Store) IsCertRevoked(serial string) (bool, error) {
	var status string
	err := s.db.QueryRow(`SELECT status FROM certs WHERE serial=?`, serial).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return status == "revoked", err
}

// GetCert 按序列号查询证书元数据。
func (s *Store) GetCert(serial string) (*CertMeta, error) {
	var c CertMeta
	var nb, na, created string
	var revokedAt, reason sql.NullString
	err := s.db.QueryRow(`SELECT id, node_id, serial, subject, issuer, not_before, not_after, status, fingerprint, revoked_at, revoke_reason, created_at
		FROM certs WHERE serial=?`, serial).
		Scan(&c.ID, &c.NodeID, &c.Serial, &c.Subject, &c.Issuer, &nb, &na, &c.Status, &c.Fingerprint, &revokedAt, &reason, &created)
	if err != nil {
		return nil, err
	}
	c.NotBefore, _ = time.Parse(time.RFC3339, nb)
	c.NotAfter, _ = time.Parse(time.RFC3339, na)
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if revokedAt.Valid {
		t, _ := time.Parse(time.RFC3339Nano, revokedAt.String)
		c.RevokedAt = &t
	}
	if reason.Valid {
		c.RevokeReason = reason.String
	}
	return &c, nil
}

// RevokeCert 吊销证书。
func (s *Store) RevokeCert(serial, reason string) error {
	res, err := s.db.Exec(`UPDATE certs SET status='revoked', revoked_at=?, revoke_reason=? WHERE serial=?`,
		nowStr(), reason, serial)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: cert serial %s not found", serial)
	}
	return nil
}

// ListCerts 列出证书元数据。
func (s *Store) ListCerts() ([]CertMeta, error) {
	rows, err := s.db.Query(`SELECT id, node_id, serial, subject, issuer, not_before, not_after, status, fingerprint, revoked_at, revoke_reason, created_at FROM certs ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CertMeta
	for rows.Next() {
		var c CertMeta
		var nb, na, created string
		var revokedAt, reason sql.NullString
		if err := rows.Scan(&c.ID, &c.NodeID, &c.Serial, &c.Subject, &c.Issuer, &nb, &na, &c.Status, &c.Fingerprint, &revokedAt, &reason, &created); err != nil {
			return nil, err
		}
		c.NotBefore, _ = time.Parse(time.RFC3339, nb)
		c.NotAfter, _ = time.Parse(time.RFC3339, na)
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		if revokedAt.Valid {
			t, _ := time.Parse(time.RFC3339Nano, revokedAt.String)
			c.RevokedAt = &t
		}
		if reason.Valid {
			c.RevokeReason = reason.String
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CertExpiring 返回指定天数内到期的活动证书数量。
func (s *Store) CertExpiring(days int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, days).UTC().Format(time.RFC3339)
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM certs WHERE status='active' AND not_after <= ?`, cutoff).Scan(&n)
	return n, err
}

// CertFingerprintHex 计算证书 PEM 的 SHA256 指纹（十六进制）。
func CertFingerprintHex(pemBytes []byte) string {
	sum := sha256.Sum256(pemBytes)
	return hex.EncodeToString(sum[:])
}
