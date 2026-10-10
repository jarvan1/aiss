// Package del removes a discovered session from disk. Each provider stores a
// session differently, so deletion is provider-specific: some are a single
// file, some a directory, and codex ships a native `codex delete` that also
// prunes its own index db. We prefer the native command where it exists and
// fall back to removing the on-disk files we know about.
package del

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jarvan1/aiss/internal/session"
)

// Plan is the set of filesystem paths to remove plus an optional native CLI
// command to run first. It's built separately from execution so it can be
// unit-tested without touching disk.
type Plan struct {
	// nativeCmd, if non-empty, is a CLI command tried before removing paths
	// (e.g. codex delete <id>). A failure is non-fatal: we still remove paths.
	nativeCmd []string
	// nativeEnv selects the originating Codex config/auth and shared runtime
	// state when nativeCmd operates on a Codex session.
	nativeEnv []string
	// paths are files or directories to remove (os.RemoveAll). Every entry is an
	// absolute path that guardOK has already vetted.
	paths []string
}

// Paths returns the filesystem paths the plan will remove (for tests/logging).
func (p Plan) Paths() []string { return p.paths }

// NativeCmd returns the native CLI command the plan runs first, if any.
func (p Plan) NativeCmd() []string { return p.nativeCmd }

// NativeEnv returns environment overrides for the native command.
func (p Plan) NativeEnv() []string { return p.nativeEnv }

// BuildPlan computes what to remove for a session. dirs supplies the provider
// scan roots so sidecar paths (e.g. claude's session-env/<uuid>) are derived
// from the same overridable roots as scanning.
func BuildPlan(s session.Session, dirs session.Dirs) (Plan, error) {
	var p Plan
	add := func(path string) {
		if path != "" && guardOK(path) {
			p.paths = append(p.paths, path)
		}
	}

	switch s.Provider {
	case "claude":
		// projects/<dir>/<uuid>.jsonl plus the two per-uuid sidecar dirs.
		add(s.File)
		if s.ID != "" {
			claudeHome := filepath.Dir(dirs.Claude) // ~/.claude/projects -> ~/.claude
			add(filepath.Join(claudeHome, "session-env", s.ID))
			add(filepath.Join(claudeHome, "file-history", s.ID))
		}
	case "codex":
		// Native `codex delete` also prunes codex's history/index db, so prefer
		// it; still remove the rollout file in case the command is gone. --force
		// is required: without a TTY (we run from the picker), plain `codex
		// delete` refuses with "cannot confirm ... without an interactive
		// terminal", which would silently leave the index row behind.
		if s.ID != "" {
			p.nativeCmd = []string{"codex", "delete", "--force", s.ID}
			if s.CodexHome != "" {
				p.nativeEnv = append(p.nativeEnv, "CODEX_HOME="+s.CodexHome)
			}
			if s.CodexSQLiteHome != "" {
				p.nativeEnv = append(p.nativeEnv, "CODEX_SQLITE_HOME="+s.CodexSQLiteHome)
			}
		}
		add(s.File)
	case "copilot":
		// session-state/<uuid>/ — remove the whole session directory.
		add(filepath.Dir(s.File))
	case "gemini":
		// tmp/<hash>/ — remove the whole per-session directory.
		add(filepath.Dir(s.File))
	default:
		return Plan{}, fmt.Errorf("unknown provider %q", s.Provider)
	}

	if len(p.paths) == 0 && len(p.nativeCmd) == 0 {
		return Plan{}, fmt.Errorf("nothing to delete for %s session", s.Provider)
	}
	return p, nil
}

// Delete builds and executes the removal for a session.
func Delete(s session.Session, dirs session.Dirs) error {
	p, err := BuildPlan(s, dirs)
	if err != nil {
		return err
	}
	return p.Run()
}

// Run executes the plan: the native command first (best-effort), then removes
// each path. Missing paths are not an error (RemoveAll returns nil for them).
func (p Plan) Run() error {
	if len(p.nativeCmd) > 0 {
		if bin, err := exec.LookPath(p.nativeCmd[0]); err == nil {
			// Best-effort; codex delete prompts nothing for a known id. Ignore a
			// non-zero exit — we still remove the file below.
			cmd := exec.Command(bin, p.nativeCmd[1:]...)
			cmd.Env = mergeEnv(os.Environ(), p.nativeEnv)
			_ = cmd.Run()
		}
	}
	for _, path := range p.paths {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
	}
	return nil
}

func mergeEnv(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	replace := make(map[string]bool, len(overrides))
	for _, item := range overrides {
		name, _, _ := strings.Cut(item, "=")
		replace[name] = true
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		name, _, _ := strings.Cut(item, "=")
		if !replace[name] {
			out = append(out, item)
		}
	}
	return append(out, overrides...)
}

// guardOK is the safety gate: a path must be absolute, live under the user's
// home directory, and be at least a few segments deep past home. This prevents
// a malformed session (empty ID/File) from ever targeting $HOME, ~/.claude, or
// any shallow directory for removal.
func guardOK(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	home := session.Home()
	if home == "" {
		return false // can't vet without a home anchor
	}
	home = filepath.Clean(home)
	if clean == home {
		return false
	}
	rel, err := filepath.Rel(home, clean)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false // outside home
	}
	// Require depth: e.g. ".claude/projects/<dir>/<uuid>.jsonl" is 4 segments,
	// ".copilot/session-state/<uuid>" is 3. Reject anything shallower than 3 so
	// we never remove ~/.claude, ~/.codex, etc. themselves.
	if len(strings.Split(rel, string(filepath.Separator))) < 3 {
		return false
	}
	return true
}
