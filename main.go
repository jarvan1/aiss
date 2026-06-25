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
)

// version is overridden at build time via -ldflags "-X main.version=…".
var version = "dev"

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
	var query string
	for _, a := range args {
		switch a {
		case "--print", "-p":
			printOnly = true
		case "--":
			// argument separator (used by the shell widget); ignore
		default:
			if query == "" {
				query = a
			}
		}
	}

	sessions := Scan(defaultDirs(), showMissing())
	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "aiss: no AI CLI sessions found.")
		os.Exit(1)
	}

	s, ok := Pick(sessions, query)
	if !ok {
		return // user aborted
	}

	plan, err := planResume(s)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiss:", err)
		os.Exit(1)
	}

	if printOnly {
		fmt.Println(plan.Shell())
		return
	}
	if plan.note != "" {
		fmt.Fprintln(os.Stderr, "aiss:", plan.note)
	}
	if err := plan.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "aiss:", err)
		os.Exit(1)
	}
}

func cmdScan() {
	for _, s := range Scan(defaultDirs(), showMissing()) {
		preview := s.Preview
		if preview == "" {
			preview = "(no prompt)"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", s.Provider, s.ID, tilde(s.Cwd), truncRunes(preview, 100), s.File)
	}
}

func cmdPreview(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aiss preview <provider> <file>")
		os.Exit(2)
	}
	fmt.Print(Preview(Session{Provider: args[0], File: args[1]}))
}

const usage = `aiss — fuzzy picker over AI CLI session histories

  aiss [query]              pick a session and resume it in its original dir
  aiss --print [query]      print the resume command instead of running it
  aiss init <shell>         print shell integration for zsh|bash|fish
  aiss scan                 list sessions (provider, id, cwd, preview, file)
  aiss preview <prov> <f>   render one session's preview

Providers: claude, codex, copilot, gemini
Env: AISS_CLAUDE_DIR, AISS_CODEX_DIR, AISS_COPILOT_DIR, AISS_GEMINI_DIR,
     AISS_SHOW_MISSING=1 (keep sessions whose dir was deleted)
`
