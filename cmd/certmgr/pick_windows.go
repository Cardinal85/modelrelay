//go:build cgo && windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	comdlg32              = syscall.NewLazyDLL("comdlg32.dll")
	shell32               = syscall.NewLazyDLL("shell32.dll")
	ole32                 = syscall.NewLazyDLL("ole32.dll")
	procGetOpenFileNameW    = comdlg32.NewProc("GetOpenFileNameW")
	procCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	procSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree     = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx    = ole32.NewProc("CoInitializeEx")
)

const (
	ofnExplorer      = 0x00080000
	ofnFileMustExist = 0x00001000
	ofnPathMustExist = 0x00000800
	ofnHideReadOnly  = 0x00000004
	ofnNoChangeDir   = 0x00000008
	bifReturnOnlyFS  = 0x00000001
	bifNewDialog     = 0x00000040
	coInitApartment  = 0x00000002
)

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

func nativePickFile(title, initial string, exts []string) (string, bool, bool) {
	buf := make([]uint16, 32768)
	var ofn openFileNameW
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	ofn.lpstrFile = &buf[0]
	ofn.nMaxFile = uint32(len(buf))
	ofn.flags = ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly | ofnNoChangeDir
	if title != "" {
		ofn.lpstrTitle = utf16Ptr(title)
	}
	if dir := initialDir(initial); dir != "" {
		ofn.lpstrInitialDir = utf16Ptr(dir)
	}
	filter := utf16Filter(exts)
	ofn.lpstrFilter = &filter[0]
	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filter)
	if r == 0 {
		if code, _, _ := procCommDlgExtendedError.Call(); code != 0 {
			log.Printf("native file dialog failed: CommDlgExtendedError=0x%x", code)
		}
		return "", false, true
	}
	return syscall.UTF16ToString(buf), true, true
}

func nativePickFolder(title, initial string) (string, bool, bool) {
	_, _, _ = procCoInitializeEx.Call(0, coInitApartment)
	display := make([]uint16, 260)
	var bi browseInfoW
	bi.pszDisplayName = &display[0]
	if title != "" {
		bi.lpszTitle = utf16Ptr(title)
	}
	bi.ulFlags = bifReturnOnlyFS | bifNewDialog
	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false, true
	}
	defer procCoTaskMemFree.Call(pidl)
	path := make([]uint16, 32768)
	ok, _, _ := procSHGetPathFromIDList.Call(pidl, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", false, true
	}
	return syscall.UTF16ToString(path), true, true
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func utf16Filter(exts []string) []uint16 {
	var globs []string
	for _, ext := range exts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		globs = append(globs, "*"+ext)
	}
	parts := []string{"所有文件 (*.*)", "*.*"}
	if len(globs) > 0 {
		joined := strings.Join(globs, ";")
		parts = []string{"证书文件 (" + joined + ")", joined, "所有文件 (*.*)", "*.*"}
	}
	var out []uint16
	for _, p := range parts {
		out = append(out, utf16.Encode([]rune(p))...)
		out = append(out, 0)
	}
	return append(out, 0)
}

func initialDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return ""
	}
	st, err := os.Stat(path)
	if err == nil {
		if st.IsDir() {
			return path
		}
		return filepath.Dir(path)
	}
	parent := filepath.Dir(path)
	if st, err := os.Stat(parent); err == nil && st.IsDir() {
		return parent
	}
	home, _ := os.UserHomeDir()
	return home
}
