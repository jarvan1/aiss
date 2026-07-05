// Package scan discovers resumable sessions on disk across every supported
// AI CLI (Claude Code, Codex, Copilot CLI, Gemini CLI).
package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jarvan1/aiss/internal/session"
)

// Scan walks every provider and returns all sessions, newest first.
func Scan(d session.Dirs, keepMissing bool) []session.Session {
	var out []session.Session
	out = append(out, scanClaude(d.Claude, keepMissing)...)
	out = append(out, scanCodex(d.Codex, keepMissing)...)
	out = append(out, scanCopilot(d.Copilot, keepMissing)...)
	out = append(out, scanGemini(d.Gemini)...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out
}

func mtime(path string) (t session.Session, ok bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return session.Session{}, false
	}
	return session.Session{ModTime: fi.ModTime()}, true
}

// contentText pulls the first usable text out of a message "content" field,
// which may be a plain string or an array of typed blocks.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}

// glob is a thin wrapper that swallows the (only-on-bad-pattern) error.
func glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

// --- Claude: ~/.claude/projects/<dir>/<uuid>.jsonl ------------------------

func scanClaude(root string, keepMissing bool) []session.Session {
	var out []session.Session
	for _, f := range glob(filepath.Join(root, "*", "*.jsonl")) {
		base, ok := mtime(f)
		if !ok {
			continue
		}
		type line struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		var cwd, prompt string
		session.EachLine(f, func(raw []byte) bool {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				return true
			}
			if cwd == "" && l.Cwd != "" {
				cwd = l.Cwd
			}
			if prompt == "" && l.Type == "user" {
				t := contentText(l.Message.Content)
				if t != "" && !session.ReAngle.MatchString(t) && !session.ReCaveat.MatchString(t) {
					prompt = t
				}
			}
			return !(cwd != "" && prompt != "")
		})
		if cwd == "" {
			cwd = session.Home()
		}
		if !keepMissing && !session.DirExists(cwd) {
			continue
		}
		base.Provider = "claude"
		base.ID = strings.TrimSuffix(filepath.Base(f), ".jsonl")
		base.Cwd = cwd
		base.File = f
		base.Preview = session.CollapseWS(prompt)
		out = append(out, base)
	}
	return out
}

// --- Codex: ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl ------------------

func scanCodex(root string, keepMissing bool) []session.Session {
	var out []session.Session
	for _, f := range globRecursive(root, "rollout-*.jsonl") {
		base, ok := mtime(f)
		if !ok {
			continue
		}
		type line struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				ID      string `json:"id"`
				Cwd     string `json:"cwd"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		var id, cwd, prompt string
		session.EachLine(f, func(raw []byte) bool {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				return true
			}
			if l.Type == "session_meta" {
				if id == "" {
					id = l.Payload.ID
				}
				if cwd == "" {
					cwd = l.Payload.Cwd
				}
			}
			if prompt == "" && l.Payload.Type == "message" && l.Payload.Role == "user" {
				for _, c := range l.Payload.Content {
					if c.Text != "" && !session.ReCodexInjected.MatchString(c.Text) {
						prompt = c.Text
						break
					}
				}
			}
			return !(id != "" && cwd != "" && prompt != "")
		})
		if id == "" {
			// fallback: trailing uuid in the filename rollout-<ts>-<uuid>.jsonl
			name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			if i := strings.LastIndex(name, "-"); i >= 0 {
				id = name[i+1:]
			}
		}
		if !keepMissing && cwd != "" && !session.DirExists(cwd) {
			continue
		}
		base.Provider = "codex"
		base.ID = id
		base.Cwd = cwd
		base.File = f
		base.Preview = session.CollapseWS(prompt)
		out = append(out, base)
	}
	return out
}

// --- Copilot: ~/.copilot/session-state/<uuid>/events.jsonl ----------------

func scanCopilot(root string, keepMissing bool) []session.Session {
	var out []session.Session
	for _, f := range glob(filepath.Join(root, "*", "events.jsonl")) {
		base, ok := mtime(f)
		if !ok {
			continue
		}
		type line struct {
			Type string `json:"type"`
			Data struct {
				Content string `json:"content"`
				Context struct {
					Cwd string `json:"cwd"`
				} `json:"context"`
			} `json:"data"`
		}
		var cwd, prompt string
		session.EachLine(f, func(raw []byte) bool {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				return true
			}
			if cwd == "" && l.Type == "session.start" {
				cwd = l.Data.Context.Cwd
			}
			if prompt == "" && l.Type == "user.message" {
				if t := l.Data.Content; t != "" && !session.ReAngle.MatchString(t) {
					prompt = t
				}
			}
			return !(cwd != "" && prompt != "")
		})
		if !keepMissing && cwd != "" && !session.DirExists(cwd) {
			continue
		}
		base.Provider = "copilot"
		base.ID = filepath.Base(filepath.Dir(f)) // parent dir = session uuid
		base.Cwd = cwd
		base.File = f
		base.Preview = session.CollapseWS(prompt)
		out = append(out, base)
	}
	return out
}

// --- Gemini: ~/.gemini/tmp/<hash>/logs.json (experimental) ----------------

func scanGemini(root string) []session.Session {
	var out []session.Session
	for _, f := range glob(filepath.Join(root, "*", "logs.json")) {
		base, ok := mtime(f)
		if !ok {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var entries []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &entries)
		var prompt string
		for _, e := range entries {
			if e.Type == "user" && e.Message != "" {
				prompt = e.Message
				break
			}
		}
		dir := filepath.Base(filepath.Dir(f))
		base.Provider = "gemini"
		base.ID = dir
		base.Cwd = dir // gemini stores a hash, not a real path
		base.File = f
		base.Preview = session.CollapseWS(prompt)
		out = append(out, base)
	}
	return out
}

// globRecursive walks root and returns files whose base name matches pattern.
func globRecursive(root, pattern string) []string {
	var matches []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ok, _ := filepath.Match(pattern, d.Name()); ok {
			matches = append(matches, p)
		}
		return nil
	})
	return matches
}
