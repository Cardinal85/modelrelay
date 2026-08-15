//go:build cgo && !windows

package main

func nativePickFile(string, string, []string) (string, bool, bool) {
	return "", false, false
}

func nativePickFolder(string, string) (string, bool, bool) {
	return "", false, false
}
