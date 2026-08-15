//go:build !cgo

package main

import (
	"fmt"
	"os"
)

func runUI() {
	fmt.Fprintln(os.Stderr, "certmgr GUI requires a native build with CGO_ENABLED=1 (Fyne desktop).")
	os.Exit(2)
}
