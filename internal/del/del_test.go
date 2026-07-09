package del

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
)

func TestBuildPlanClaudeIncludesSidecars(t *testing.T) {
	home := session.Home()
	dirs := session.Dirs{Claude: filepath.Join(home, ".claude", "projects")}
	s := session.Session{
		Provider: "claude",
		ID:       "abc-123",
		File:     filepath.Join(home, ".claude", "projects", "-p-x", "abc-123.jsonl"),
	}
	p, err := BuildPlan(s, dirs)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(home, ".claude", "projects", "-p-x", "abc-123.jsonl"),
		filepath.Join(home, ".claude", "session-env", "abc-123"),
		filepath.Join(home, ".claude", "file-history", "abc-123"),
	}
	if strings.Join(p.Paths(), "|") != strings.Join(want, "|") {
		t.Errorf("paths:\n got %v\nwant %v", p.Paths(), want)
	}
	if len(p.NativeCmd()) != 0 {
		t.Errorf("claude should have no native cmd, got %v", p.NativeCmd())
	}
}

func TestBuildPlanCodexPrefersNativeCmd(t *testing.T) {
	home := session.Home()
	s := session.Session{
		Provider: "codex",
		ID:       "uuid-9",
		File:     filepath.Join(home, ".codex", "sessions", "2026", "07", "01", "rollout-x.jsonl"),
	}
	p, err := BuildPlan(s, session.Dirs{})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.NativeCmd(); len(got) != 4 || got[0] != "codex" || got[1] != "delete" || got[2] != "--force" || got[3] != "uuid-9" {
		t.Errorf("native cmd = %v, want [codex delete --force uuid-9]", got)
	}
	if len(p.Paths()) != 1 {
		t.Errorf("codex should also remove the rollout file, got %v", p.Paths())
	}
}

func TestBuildPlanCopilotAndGeminiRemoveDir(t *testing.T) {
	home := session.Home()
	cop := session.Session{Provider: "copilot", ID: "u", File: filepath.Join(home, ".copilot", "session-state", "u", "events.jsonl")}
	p, _ := BuildPlan(cop, session.Dirs{})
	if len(p.Paths()) != 1 || filepath.Base(p.Paths()[0]) != "u" {
		t.Errorf("copilot should remove the session dir, got %v", p.Paths())
	}

	gem := session.Session{Provider: "gemini", ID: "h", File: filepath.Join(home, ".gemini", "tmp", "h", "logs.json")}
	p, _ = BuildPlan(gem, session.Dirs{})
	if len(p.Paths()) != 1 || filepath.Base(p.Paths()[0]) != "h" {
		t.Errorf("gemini should remove the tmp dir, got %v", p.Paths())
	}
}

func TestBuildPlanUnknownProvider(t *testing.T) {
	if _, err := BuildPlan(session.Session{Provider: "nope"}, session.Dirs{}); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestGuardRejectsShallowAndOutsideHome(t *testing.T) {
	home := session.Home()
	bad := []string{
		"",                             // empty
		"relative/path",                // not absolute
		home,                           // home itself
		filepath.Join(home, ".claude"), // one segment deep
		filepath.Join(home, ".claude", "projects"), // two segments deep
		"/etc/passwd",             // outside home
		filepath.Join(home, ".."), // escapes home
	}
	for _, b := range bad {
		if guardOK(b) {
			t.Errorf("guardOK(%q) = true, want false", b)
		}
	}
	good := filepath.Join(home, ".claude", "projects", "-p", "u.jsonl")
	if !guardOK(good) {
		t.Errorf("guardOK(%q) = false, want true", good)
	}
}
