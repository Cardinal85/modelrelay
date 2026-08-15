// Package certmgr 提供证书管理器的可测试业务逻辑：CA 工作区、签发、检查、导出
// 以及可选的 Relay 管理 API 客户端。GUI 只调用本包，不在窗口事件中实现签发逻辑。
package certmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"modelrelay/internal/certs"
)

// Kind 是 CA 工作区类型。
type Kind string

const (
	KindAgent Kind = "agent"
	KindRelay Kind = "relay"
)

const (
	DefaultCADays    = 3650
	DefaultIssueDays = 365
	agentCACertName  = "agent-ca.crt"
	agentCAKeyName   = "agent-ca.key"
	relayCACertName  = "relay-ca.crt"
	relayCAKeyName   = "relay-ca.key"
	legacyCACertName = "agent-ca.crt"
	legacyCAKeyName  = "agent-ca.key"
)

// Workspace 是受操作系统权限保护的 CA 工作区。
type Workspace struct {
	Dir     string
	Kind    Kind
	CertPEM []byte
	KeyPEM  []byte
	CA      *certs.CA
	Info    *InspectInfo
}

// CreateOptions 创建 CA 工作区的参数。
type CreateOptions struct {
	Dir  string
	Kind Kind
	CN   string
	Days int
}

// DefaultCN 返回指定类型 CA 的默认主题。
func DefaultCN(kind Kind) string {
	switch kind {
	case KindRelay:
		return "ModelRelay Relay CA"
	default:
		return "ModelRelay Agent CA"
	}
}

// DefaultWorkspaceRoot 返回本机默认 CA 目录。
func DefaultWorkspaceRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", "ca")
	}
	return filepath.Join(home, "ModelRelay", "ca")
}

// CAPaths 返回工作区证书与私钥路径。
func CAPaths(dir string, kind Kind) (certPath, keyPath string) {
	switch kind {
	case KindRelay:
		return filepath.Join(dir, relayCACertName), filepath.Join(dir, relayCAKeyName)
	default:
		return filepath.Join(dir, agentCACertName), filepath.Join(dir, agentCAKeyName)
	}
}

func openCAPaths(dir string, kind Kind) (certPath, keyPath string, err error) {
	certPath, keyPath = CAPaths(dir, kind)
	if fileExists(certPath) && fileExists(keyPath) {
		return certPath, keyPath, nil
	}
	if kind == KindRelay {
		legacyCert := filepath.Join(dir, legacyCACertName)
		legacyKey := filepath.Join(dir, legacyCAKeyName)
		if fileExists(legacyCert) && fileExists(legacyKey) {
			return legacyCert, legacyKey, nil
		}
	}
	return "", "", fmt.Errorf("certmgr: CA files not found in %s", dir)
}

// CreateWorkspace 创建新的 CA 工作区。已有私钥时拒绝覆盖。
func CreateWorkspace(opt CreateOptions) (*Workspace, error) {
	if strings.TrimSpace(opt.Dir) == "" {
		return nil, fmt.Errorf("certmgr: workspace directory is required")
	}
	kind := opt.Kind
	if kind != KindRelay {
		kind = KindAgent
	}
	cn := strings.TrimSpace(opt.CN)
	if cn == "" {
		cn = DefaultCN(kind)
	}
	days := opt.Days
	if days <= 0 {
		days = DefaultCADays
	}
	if err := mkdirSecure(opt.Dir); err != nil {
		return nil, fmt.Errorf("certmgr: create workspace: %w", err)
	}
	if err := lockDownDir(opt.Dir); err != nil {
		return nil, fmt.Errorf("certmgr: lock workspace: %w", err)
	}
	certPath, keyPath := CAPaths(opt.Dir, kind)
	if fileExists(keyPath) {
		return nil, fmt.Errorf("certmgr: CA key already exists, refusing to overwrite: %s", keyPath)
	}
	certPEM, keyPEM, err := certs.CreateCA(cn, days)
	if err != nil {
		return nil, err
	}
	if err := writePublicFile(certPath, certPEM); err != nil {
		return nil, err
	}
	if err := writePrivateFile(keyPath, keyPEM); err != nil {
		return nil, err
	}
	return loadWorkspace(opt.Dir, kind, certPath, keyPath)
}

// OpenWorkspace 打开已有 CA 工作区。
func OpenWorkspace(dir string, kind Kind) (*Workspace, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("certmgr: workspace directory is required")
	}
	if kind != KindRelay {
		kind = KindAgent
	}
	certPath, keyPath, err := openCAPaths(dir, kind)
	if err != nil {
		return nil, err
	}
	if err := lockDownDir(dir); err != nil {
		return nil, fmt.Errorf("certmgr: lock workspace: %w", err)
	}
	if err := chmodPrivate(keyPath); err != nil {
		return nil, fmt.Errorf("certmgr: lock CA key: %w", err)
	}
	return loadWorkspace(dir, kind, certPath, keyPath)
}

func loadWorkspace(dir string, kind Kind, certPath, keyPath string) (*Workspace, error) {
	ca, err := certs.LoadCAFiles(certPath, keyPath)
	if err != nil {
		return nil, err
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	info, err := Inspect(certPEM)
	if err != nil {
		return nil, err
	}
	return &Workspace{
		Dir:     dir,
		Kind:    kind,
		CertPEM: certPEM,
		KeyPEM:  keyPEM,
		CA:      ca,
		Info:    info,
	}, nil
}

// BackupHint 返回离线备份提示。
func (w *Workspace) BackupHint() string {
	_, keyPath := CAPaths(w.Dir, w.Kind)
	return fmt.Sprintf("请离线备份 CA 私钥 %s，不要上传到 Relay、Agent 或 GitHub。", keyPath)
}

// CertPath 返回当前工作区 CA 证书路径。
func (w *Workspace) CertPath() string {
	certPath, _, err := openCAPaths(w.Dir, w.Kind)
	if err != nil {
		certPath, _ = CAPaths(w.Dir, w.Kind)
	}
	return certPath
}

// KeyPath 返回当前工作区 CA 私钥路径。
func (w *Workspace) KeyPath() string {
	_, keyPath, err := openCAPaths(w.Dir, w.Kind)
	if err != nil {
		_, keyPath = CAPaths(w.Dir, w.Kind)
	}
	return keyPath
}
