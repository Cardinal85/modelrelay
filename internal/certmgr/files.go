package certmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writePrivateFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return chmodPrivate(path)
}

func writePublicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	return chmodPublic(path)
}

func copyFile(src, dst string, private bool) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if private {
		return writePrivateFile(dst, data)
	}
	return writePublicFile(dst, data)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isCAKeyName(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return name == "agent-ca.key" || name == "relay-ca.key"
}

func rejectCAKey(path string) error {
	if isCAKeyName(path) {
		return fmt.Errorf("certmgr: refusing to export CA private key %s", path)
	}
	return nil
}
