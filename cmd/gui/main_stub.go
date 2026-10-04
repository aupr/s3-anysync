//go:build !cgo && !windows

// Command s3-anysync is the desktop GUI; this build has no CGO and no GUI.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "s3-anysync: this build has no GUI; build with CGO enabled (Xcode command-line tools on macOS)")
	os.Exit(1)
}
