package certmgr

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	// ErrAgentKeyNotExported 表示 CA GUI 不会生成或导出 Agent 私钥。
	ErrAgentKeyNotExported = errors.New("agent private key must be generated on the Agent host and is not exported by certmgr")
	// ErrCAKeyNotExported 表示 CA 私钥不得进入部署目录。
	ErrCAKeyNotExported = errors.New("CA private key must not be exported to deployment directories")
)

// RelayExportInput 导出 Relay 部署文件。
type RelayExportInput struct {
	ServerCertPath string
	ServerKeyPath  string
	AgentCAPath    string
	DestDir        string
}

// AgentExportInput 导出 Agent 部署文件。不包含 Agent 私钥。
type AgentExportInput struct {
	AgentCertPath string
	RelayCAPath   string
	DestDir       string
	NodeID        string
}

// ExportRelay 导出 Relay 所需的服务端证书、私钥和 Agent CA 公钥。
func ExportRelay(in RelayExportInput) error {
	if strings.TrimSpace(in.DestDir) == "" {
		return fmt.Errorf("certmgr: export directory is required")
	}
	if err := rejectCAKey(in.ServerKeyPath); err != nil {
		return ErrCAKeyNotExported
	}
	if err := rejectCAKey(in.ServerCertPath); err != nil {
		return err
	}
	if isCAKeyName(in.AgentCAPath) {
		return ErrCAKeyNotExported
	}
	if err := mkdirSecure(in.DestDir); err != nil {
		return err
	}
	if err := copyFile(in.ServerCertPath, filepath.Join(in.DestDir, "relay.crt"), false); err != nil {
		return err
	}
	if err := copyFile(in.ServerKeyPath, filepath.Join(in.DestDir, "relay.key"), true); err != nil {
		return err
	}
	if err := copyFile(in.AgentCAPath, filepath.Join(in.DestDir, "agent-ca.crt"), false); err != nil {
		return err
	}
	readme := []byte(strings.Join([]string{
		"ModelRelay Relay 部署文件",
		"",
		"本目录包含：",
		"- relay.crt / relay.key：Relay 服务端证书和私钥",
		"- agent-ca.crt：用于校验 Agent 客户端证书的 CA 公钥",
		"",
		"不要把 agent-ca.key 或 relay-ca.key 复制到 Relay 主机。",
		"CA 私钥只留在证书管理机，并做好离线备份。",
		"",
	}, "\n"))
	return writePublicFile(filepath.Join(in.DestDir, "README.txt"), readme)
}

// ExportAgent 导出 Agent 所需的客户端证书和 Relay CA 公钥。
// 明确不导出 Agent 私钥：私钥必须由 GPU 主机用 certctl csr 本地生成。
func ExportAgent(in AgentExportInput) error {
	if strings.TrimSpace(in.DestDir) == "" {
		return fmt.Errorf("certmgr: export directory is required")
	}
	if strings.EqualFold(filepath.Ext(in.AgentCertPath), ".key") || isCAKeyName(in.AgentCertPath) {
		return ErrAgentKeyNotExported
	}
	if isCAKeyName(in.RelayCAPath) || strings.EqualFold(filepath.Ext(in.RelayCAPath), ".key") {
		return ErrCAKeyNotExported
	}
	if err := mkdirSecure(in.DestDir); err != nil {
		return err
	}
	nodeID := strings.TrimSpace(in.NodeID)
	if nodeID == "" {
		nodeID = strings.TrimSuffix(filepath.Base(in.AgentCertPath), filepath.Ext(in.AgentCertPath))
	}
	if nodeID == "" {
		nodeID = "agent"
	}
	certName := sanitizeFileName(nodeID) + ".crt"
	if err := copyFile(in.AgentCertPath, filepath.Join(in.DestDir, certName), false); err != nil {
		return err
	}
	if err := copyFile(in.RelayCAPath, filepath.Join(in.DestDir, "relay-ca.crt"), false); err != nil {
		return err
	}
	readme := []byte(strings.Join([]string{
		"ModelRelay Agent 部署文件",
		"",
		"本目录包含：",
		"- " + certName + "：由证书管理机签发的 Agent 证书",
		"- relay-ca.crt：用于校验 Relay 服务端证书的 CA 公钥",
		"",
		"Agent 私钥必须由 GPU 主机本地执行 certctl csr 生成，不要由证书管理器代生成，",
		"也不要把 *.key 带回证书管理机。",
		"不要把 agent-ca.key 或 relay-ca.key 复制到 Agent 主机。",
		"",
	}, "\n"))
	return writePublicFile(filepath.Join(in.DestDir, "README.txt"), readme)
}
