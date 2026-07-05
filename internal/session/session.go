// Package session defines the core Session type shared across aiss and the
// small filesystem/text helpers the scanners and preview renderers both use.
package session

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"
)

// Session is one resumable AI-CLI conversation discovered on disk.
type Session struct {
	Provider string    // claude | codex | copilot | gemini
	ID       string    // resume key (uuid / dir name)
	Cwd      string    // original working directory
	File     string    // path to the session file on disk
	Preview  string    // first real user message, single-lined
	ModTime  time.Time // file mtime, used as the sort key (newest first)
}

// Dirs holds the per-provider scan roots, overridable via AISS_*_DIR env vars.
type Dirs struct {
	Claude  string
	Codex   string
	Copilot string
	Gemini  string
}

// DefaultDirs returns the scan roots, honoring the AISS_*_DIR overrides.
func DefaultDirs() Dirs {
	h := Home()
	return Dirs{
		Claude:  EnvOr("AISS_CLAUDE_DIR", h+"/.claude/projects"),
		Codex:   EnvOr("AISS_CODEX_DIR", h+"/.codex/sessions"),
		Copilot: EnvOr("AISS_COPILOT_DIR", h+"/.copilot/session-state"),
		Gemini:  EnvOr("AISS_GEMINI_DIR", h+"/.gemini/tmp"),
	}
}

// ShowMissing reports whether sessions whose cwd no longer exists should be kept.
func ShowMissing() bool { return EnvOr("AISS_SHOW_MISSING", "") != "" }

// Shared regexes: a leading "<" marks an injected/system message, and the codex
// variant also catches its wrapper tags. Both scan and preview filter on these.
var (
	ReAngle         = regexp.MustCompile(`^<`)
	ReCaveat        = regexp.MustCompile(`^Caveat:`)
	ReCodexInjected = regexp.MustCompile(`^(<(environment_context|user_instructions|permissions|INSTRUCTIONS)|# AGENTS\.md)`)
)

// EnvOr returns the value of the environment variable key, or def if unset/empty.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Home is the user's home directory, or "" if it can't be determined.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// Tilde abbreviates $HOME to ~ for display.
func Tilde(p string) string {
	h := Home()
	if h != "" && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

// DirExists reports whether p is an existing directory.
func DirExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

var wsRun = regexp.MustCompile(`[\n\t\r]+`)

// CollapseWS replaces runs of newlines/tabs/CRs with a single space and trims.
func CollapseWS(s string) string {
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// TruncRunes truncates s to at most n runes (so we never cut a multibyte char).
func TruncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// EachLine streams a (possibly large) JSONL file line by line. The callback gets
// the raw bytes of each non-empty line; return false to stop early.
func EachLine(path string, fn func(raw []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Session lines (esp. assistant turns with embedded tool output) can be huge.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		if !fn(b) {
			return nil
		}
	}
	return sc.Err()
}

// ParseTime parses the ISO-8601 timestamps the CLIs emit (RFC3339, with or
// without fractional seconds). Returns zero time on failure.
func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
