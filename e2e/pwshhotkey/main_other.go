//go:build !windows

// The pwshhotkey end-to-end check drives a real Windows pseudo console and is
// meaningful only there.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "pwshhotkey: Windows-only e2e check; nothing to do here")
	os.Exit(0)
}
