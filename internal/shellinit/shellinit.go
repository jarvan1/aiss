// Package shellinit serves the shell integration snippets embedded in the
// binary, so a single binary can emit them:
//
//	eval "$(aiss init zsh)"
//
// which keeps the snippet and the binary from ever drifting apart.
package shellinit

import _ "embed"

var (
	//go:embed aiss.zsh
	zsh string
	//go:embed aiss.bash
	bash string
	//go:embed aiss.fish
	fish string
	//go:embed aiss.ps1
	pwsh string
)

// Snippet returns the integration snippet for the named shell. ok is false for
// an unsupported shell name.
func Snippet(shell string) (snippet string, ok bool) {
	switch shell {
	case "zsh":
		return zsh, true
	case "bash":
		return bash, true
	case "fish":
		return fish, true
	case "powershell", "pwsh":
		return pwsh, true
	}
	return "", false
}
