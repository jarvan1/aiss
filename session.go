package main

import "time"

// Session is one resumable AI-CLI conversation discovered on disk.
type Session struct {
	Provider string    // claude | codex | copilot | gemini
	ID       string    // resume key (uuid / dir name)
	Cwd      string    // original working directory
	File     string    // path to the session file on disk
	Preview  string    // first real user message, single-lined
	ModTime  time.Time // file mtime, used as the sort key (newest first)
}

// dirs holds the per-provider scan roots, overridable via AISS_*_DIR env vars.
type dirs struct {
	claude  string
	codex   string
	copilot string
	gemini  string
}

func defaultDirs() dirs {
	h := home()
	return dirs{
		claude:  envOr("AISS_CLAUDE_DIR", h+"/.claude/projects"),
		codex:   envOr("AISS_CODEX_DIR", h+"/.codex/sessions"),
		copilot: envOr("AISS_COPILOT_DIR", h+"/.copilot/session-state"),
		gemini:  envOr("AISS_GEMINI_DIR", h+"/.gemini/tmp"),
	}
}

// showMissing reports whether sessions whose cwd no longer exists should be kept.
func showMissing() bool { return envOr("AISS_SHOW_MISSING", "") != "" }
