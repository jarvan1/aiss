// Command aiss is a cross-platform fuzzy picker over the session histories of
// several AI CLIs (Claude Code, Codex, Copilot CLI, Gemini CLI). Pick a session
// and it re-enters it in the original working directory.
//
// Usage:
//
//	aiss [query]            interactive picker; resume the chosen session
//	aiss --print [query]    print the resume command instead of running it
//	aiss scan               list discovered sessions (tab-separated, for scripts)
//	aiss preview <prov> <file>   render a session's preview (used for debugging)
package main

import (
	"fmt"
	"os"

	"github.com/jarvan1/aiss/internal/picker"
	"github.com/jarvan1/aiss/internal/preview"
	"github.com/jarvan1/aiss/internal/resume"
	"github.com/jarvan1/aiss/internal/scan"
	"github.com/jarvan1/aiss/internal/session"
	"github.com/jarvan1/aiss/internal/shellinit"
	"github.com/mattn/go-runewidth"
)

// version is overridden at build time via -ldflags "-X main.version=…".
var version = "dev"

func init() {
	// Box-drawing (─ │ ╭ ╮) and symbols like · → ⎇ are East-Asian *ambiguous*
	// width: under a CJK locale go-runewidth counts them as 2 cells, but the
	// terminal renders them as 1. That mismatch made lipgloss/truncate chop the
	// preview box's right border. Force narrow so measurement matches the screen.
	runewidth.DefaultCondition.EastAsianWidth = false
}

func main() {
	args := os.Args[1:]

	// Subcommands that don't open the picker.
	if len(args) > 0 {
		switch args[0] {
		case "init":
			cmdInit(args[1:])
			return
		case "version", "--version", "-v":
			fmt.Printf("aiss %s\n", version)
			return
		case "scan":
			cmdScan()
			return
		case "preview":
			cmdPreview(args[1:])
			return
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		}
	}

	printOnly := false
	pwsh := false
	var query string
	for _, a := range args {
		switch a {
		case "--print", "-p":
			printOnly = true
		case "--pwsh":
			// emit PowerShell syntax for --print (used by the pwsh widget)
			pwsh = true
		case "--":
			// argument separator (used by the shell widget); ignore
		default:
			if query == "" {
				query = a
			}
		}
	}

	sessions := scan.Scan(session.DefaultDirs(), session.ShowMissing())
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "aiss: no AI CLI sessions found.")
		os.Exit(1)
	}

	s, ok := picker.Pick(sessions, query)
	if !ok {
		return // user aborted
	}

	plan, err := resume.PlanResume(s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiss:", err)
		os.Exit(1)
	}

	if printOnly {
		if pwsh {
			fmt.Println(plan.PowerShell())
		} else {
			fmt.Println(plan.Shell())
		}
		return
	}
	if plan.Note() != "" {
		fmt.Fprintln(os.Stderr, "aiss:", plan.Note())
	}
	if err := plan.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "aiss:", err)
		os.Exit(1)
	}
}

// cmdInit prints the shell integration snippet for the requested shell.
func cmdInit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: aiss init <zsh|bash|fish|powershell>")
		os.Exit(2)
	}
	snippet, ok := shellinit.Snippet(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "aiss: unsupported shell %q (use zsh, bash, fish, or powershell)\n", args[0])
		os.Exit(2)
	}
	fmt.Print(snippet)
}

func cmdScan() {
	for _, s := range scan.Scan(session.DefaultDirs(), session.ShowMissing()) {
		prev := s.Preview
		if prev == "" {
			prev = "(no prompt)"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", s.Provider, s.ID, session.Tilde(s.Cwd), session.TruncRunes(prev, 100), s.File)
	}
}

func cmdPreview(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aiss preview <provider> <file>")
		os.Exit(2)
	}
	fmt.Print(preview.Preview(session.Session{Provider: args[0], File: args[1]}, 0))
}

const usage = `aiss — fuzzy picker over AI CLI session histories

  aiss [query]              pick a session and resume it in its original dir
  aiss --print [query]      print the resume command instead of running it
  aiss init <shell>         print shell integration for zsh|bash|fish|powershell
  aiss scan                 list sessions (provider, id, cwd, preview, file)
  aiss preview <prov> <f>   render one session's preview

Providers: claude, codex, copilot, gemini
Env: AISS_CLAUDE_DIR, AISS_CODEX_DIR, AISS_COPILOT_DIR, AISS_GEMINI_DIR,
     AISS_SHOW_MISSING=1 (keep sessions whose dir was deleted)
`
