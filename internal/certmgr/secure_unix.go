//go:build !windows

package certmgr

import "os"

func mkdirSecure(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

func lockDownDir(dir string) error {
	return os.Chmod(dir, 0o700)
}

func chmodPrivate(path string) error {
	return os.Chmod(path, 0o600)
}

func chmodPublic(path string) error {
	return os.Chmod(path, 0o644)
}
