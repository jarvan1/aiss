// Package scan discovers resumable sessions on disk across every supported
// AI CLI (Claude Code, Codex, Copilot CLI, Gemini CLI).
package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jarvan1/aiss/internal/codexhistory"
	"github.com/jarvan1/aiss/internal/session"
)

// Scan walks every provider and returns all sessions, newest first.
func Scan(d session.Dirs, keepMissing bool) []session.Session {
	var out []session.Session
	out = append(out, scanClaude(d.Claude, keepMissing)...)
	out = append(out, scanCodexHomes(d, keepMissing)...)
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

// --- Codex: <sqlite-home>/sessions or <profile-home>/sessions -------------

func scanCodexHomes(d session.Dirs, keepMissing bool) []session.Session {
	var out []session.Session
	seenRoots := make(map[string]bool)
	scanRoot := func(root string, fixedProfile *session.CodexProfile) {
		root = filepath.Clean(root)
		key := root
		// Profile homes may symlink their sessions tree to the shared runtime
		// home. Resolve that root so it is walked once, including when the shared
		// path was overridden or is itself a symlink.
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			key = resolved
		}
		if root == "." || seenRoots[key] {
			return
		}
		seenRoots[key] = true
		out = append(out, scanCodex(root, keepMissing, d.CodexProfiles, d.CodexProfileSelections, fixedProfile)...)
	}

	// Shared SQLite homes store rollouts from multiple config/auth homes in one
	// sessions tree. creator_account_id associates each rollout with its home.
	scanRoot(d.Codex, nil)
	for i := range d.CodexProfiles {
		profile := &d.CodexProfiles[i]
		scanRoot(filepath.Join(profile.Home, "sessions"), profile)
	}
	// Migrated threads may exist only in SQLite. Merge its current metadata and
	// history location by thread ID rather than emitting duplicate rollout rows.
	byID := make(map[string]int)
	for i, s := range out {
		byID[s.ID] = i
	}
	homes := []string{filepath.Dir(d.Codex)}
	for _, profile := range d.CodexProfiles {
		homes = append(homes, profile.SQLiteHome)
	}
	seenHomes := make(map[string]bool)
	for _, home := range homes {
		if home == "" || seenHomes[home] {
			continue
		}
		seenHomes[home] = true
		threads, err := codexhistory.List(home)
		if err != nil {
			continue // absent/older/busy DB: retain rollout-based discovery
		}
		for _, thread := range threads {
			if !keepMissing && thread.Cwd != "" && !session.DirExists(thread.Cwd) {
				continue
			}
			s := session.Session{Provider: "codex", ID: thread.ID, Cwd: thread.Cwd,
				File: thread.File, Preview: session.CollapseWS(thread.Preview), Model: thread.Model,
				ModTime: thread.UpdatedAt, CodexSQLiteHome: home, CodexHistoryDB: thread.HistoryDB,
				CodexInternalReview: thread.InternalReview}
			if i, ok := byID[s.ID]; ok {
				// Keep the actual on-disk .zst path (state DB retains .jsonl).
				s.File = out[i].File
				s.Profile, s.CodexHome = out[i].Profile, out[i].CodexHome
				s.CodexInternalReview = s.CodexInternalReview || out[i].CodexInternalReview
				if s.Preview == "" {
					s.Preview = out[i].Preview
				}
			}
			applyCodexProfile(&s, d.CodexProfiles, d.CodexProfileSelections, nil, thread.AccountID)
			if i, ok := byID[s.ID]; ok {
				out[i] = s
			} else {
				byID[s.ID] = len(out)
				out = append(out, s)
			}
		}
	}
	if !session.ShowInternal() {
		visible := out[:0]
		for _, s := range out {
			if !s.CodexInternalReview {
				visible = append(visible, s)
			}
		}
		out = visible
	}
	return out
}

func applyCodexProfile(s *session.Session, profiles []session.CodexProfile, remembered map[string]session.CodexProfileSelection, fixed *session.CodexProfile, accountID string) {
	profile := fixed
	if profile == nil && accountID != "" {
		for i := range profiles {
			if profiles[i].AccountID != "" && profiles[i].AccountID == accountID {
				profile = &profiles[i]
				break
			}
		}
	}
	if profile == nil && s.Profile == "" {
		if selected, ok := remembered[s.ID]; ok {
			for i := range profiles {
				if (selected.Home != "" && profiles[i].Home == selected.Home) ||
					(selected.Home == "" && selected.Name != "" && profiles[i].Name == selected.Name) {
					profile = &profiles[i]
					break
				}
			}
		}
	}
	if profile != nil {
		s.Profile = profile.Name
		s.CodexHome = profile.Home
		s.CodexSQLiteHome = profile.SQLiteHome
	}
}

// codexRolloutFiles recognizes both storage forms and prefers the plain file
// when both exist during compression. Temporary and unrelated suffixes are
// ignored rather than parsed as duplicate conversations.
func codexRolloutFiles(root string) []string {
	// WalkDir does not follow a symlink passed as the root. Keep the caller's
	// root for runtime-home inference while walking its actual directory.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	files := globRecursive(root, "rollout-*.jsonl*")
	plain := make(map[string]bool)
	for _, file := range files {
		if strings.HasSuffix(file, ".jsonl") {
			plain[file] = true
		}
	}
	var out []string
	for _, file := range files {
		if strings.HasSuffix(file, ".jsonl") ||
			(strings.HasSuffix(file, ".jsonl.zst") && !plain[strings.TrimSuffix(file, ".zst")]) {
			out = append(out, file)
		}
	}
	return out
}

var codexFilenameUUID = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func scanCodex(root string, keepMissing bool, profiles []session.CodexProfile, remembered map[string]session.CodexProfileSelection, fixedProfile *session.CodexProfile) []session.Session {
	var out []session.Session
	for _, f := range codexRolloutFiles(root) {
		base, ok := mtime(f)
		if !ok {
			continue
		}
		type line struct {
			Type    string `json:"type"`
			Payload struct {
				Type             string          `json:"type"`
				Role             string          `json:"role"`
				ID               string          `json:"id"`
				SessionID        string          `json:"session_id"`
				Cwd              string          `json:"cwd"`
				CreatorAccountID string          `json:"creator_account_id"`
				ThreadSource     string          `json:"thread_source"`
				Source           json.RawMessage `json:"source"`
				Content          []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		var id, cwd, prompt, creatorAccountID string
		err := session.EachLine(f, func(raw []byte) bool {
			var l line
			if json.Unmarshal(raw, &l) != nil {
				return true
			}
			if l.Type == "session_meta" {
				if id == "" {
					id = l.Payload.ID
					if id == "" {
						id = l.Payload.SessionID
					}
				}
				if cwd == "" {
					cwd = l.Payload.Cwd
				}
				if creatorAccountID == "" {
					creatorAccountID = l.Payload.CreatorAccountID
				}
				if session.IsCodexInternalReview(l.Payload.ThreadSource, l.Payload.Source) {
					base.CodexInternalReview = true
					if !session.ShowInternal() {
						return false // don't parse internal approval transcripts
					}
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
		if err != nil && id == "" {
			continue // unreadable/corrupt rollouts are not resumable entries
		}
		if id == "" {
			// fallback: trailing uuid in the filename rollout-<ts>-<uuid>.jsonl
			name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(f), ".zst"), ".jsonl")
			if uuid := codexFilenameUUID.FindString(name); uuid != "" {
				id = uuid
			} else if i := strings.LastIndex(name, "-"); i >= 0 {
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
		base.CodexSQLiteHome = filepath.Dir(root)
		applyCodexProfile(&base, profiles, remembered, fixedProfile, creatorAccountID)
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
