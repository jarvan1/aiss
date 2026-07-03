package main

import (
	_ "embed"
	"fmt"
	"os"
)

// The shell integration snippets are embedded so a single binary can emit them:
//
//	eval "$(aiss init zsh)"
//
// keeps the snippet and the binary from ever drifting apart.
var (
	//go:embed shell/aiss.zsh
	initZsh string
	//go:embed shell/aiss.bash
	initBash string
	//go:embed shell/aiss.fish
	initFish string
	//go:embed shell/aiss.ps1
	initPwsh string
)

// cmdInit prints the shell integration snippet for the requested shell.
func cmdInit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: aiss init <zsh|bash|fish|powershell>")
		os.Exit(2)
	}
	switch args[0] {
	case "zsh":
		fmt.Print(initZsh)
	case "bash":
		fmt.Print(initBash)
	case "fish":
		fmt.Print(initFish)
	case "powershell", "pwsh":
		fmt.Print(initPwsh)
	default:
		fmt.Fprintf(os.Stderr, "aiss: unsupported shell %q (use zsh, bash, fish, or powershell)\n", args[0])
		os.Exit(2)
	}
}
