package resume

import (
	"strings"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
)

func TestPlanResumeShell(t *testing.T) {
	cases := []struct {
		s    session.Session
		want string
	}{
		{session.Session{Provider: "claude", ID: "abc", Cwd: "/tmp/p"}, "( cd '/tmp/p' && claude '--resume' 'abc' )"},
		{session.Session{Provider: "copilot", ID: "u-1", Cwd: "/tmp/x"}, "( cd '/tmp/x' && copilot '--resume=u-1' )"},
		{session.Session{Provider: "gemini", ID: "h", Cwd: "/tmp/g"}, "( cd '/tmp/g' && gemini )"},
	}
	for _, c := range cases {
		p, err := PlanResume(c.s)
		if err != nil {
			t.Fatalf("%s: %v", c.s.Provider, err)
		}
		if got := p.Shell(); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.s.Provider, got, c.want)
		}
	}
}

func TestCodexProfileEnvironmentIsRenderedAndRestored(t *testing.T) {
	s := session.Session{
		Provider:        "codex",
		ID:              "uuid-work",
		Cwd:             "/tmp/project",
		CodexHome:       "/Users/test/.codex-work",
		CodexSQLiteHome: "/Users/test/.codex",
	}
	p, err := PlanResume(s)
	if err != nil {
		t.Fatal(err)
	}
	wantShell := "( cd '/tmp/project' && CODEX_HOME='/Users/test/.codex-work' CODEX_SQLITE_HOME='/Users/test/.codex' codex 'resume' 'uuid-work' )"
	if got := p.Shell(); got != wantShell {
		t.Fatalf("shell:\n got %q\nwant %q", got, wantShell)
	}

	pwsh := p.PowerShell()
	for _, want := range []string{
		"$env:CODEX_HOME = '/Users/test/.codex-work'",
		"$env:CODEX_SQLITE_HOME = '/Users/test/.codex'",
		"& 'codex' 'resume' 'uuid-work'",
		"$env:CODEX_HOME = $__aiss_old_CODEX_HOME",
		"Remove-Item Env:CODEX_SQLITE_HOME -ErrorAction SilentlyContinue",
	} {
		if !strings.Contains(pwsh, want) {
			t.Errorf("PowerShell command missing %q: %s", want, pwsh)
		}
	}
}

func TestMergeEnvReplacesProfileValues(t *testing.T) {
	got := mergeEnv([]string{"PATH=/bin", "CODEX_HOME=/old", "CODEX_SQLITE_HOME=/old-db"}, []envVar{
		{"CODEX_HOME", "/work"},
		{"CODEX_SQLITE_HOME", "/shared"},
	})
	want := []string{"PATH=/bin", "CODEX_HOME=/work", "CODEX_SQLITE_HOME=/shared"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("env = %v, want %v", got, want)
	}
}

func TestPlanResumePowerShell(t *testing.T) {
	cases := []struct {
		s    session.Session
		want string
	}{
		{session.Session{Provider: "claude", ID: "abc", Cwd: `C:\tmp\p`}, `Push-Location 'C:\tmp\p'; try { & 'claude' '--resume' 'abc' } finally { Pop-Location }`},
		{session.Session{Provider: "copilot", ID: "u-1", Cwd: `C:\x`}, `Push-Location 'C:\x'; try { & 'copilot' '--resume=u-1' } finally { Pop-Location }`},
		{session.Session{Provider: "gemini", ID: "h", Cwd: `C:\g`}, `Push-Location 'C:\g'; try { & 'gemini' } finally { Pop-Location }`},
	}
	for _, c := range cases {
		p, err := PlanResume(c.s)
		if err != nil {
			t.Fatalf("%s: %v", c.s.Provider, err)
		}
		if got := p.PowerShell(); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.s.Provider, got, c.want)
		}
	}
}

func TestShellQuoteEscapesQuotes(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("got %q", got)
	}
}

func TestPwshQuoteEscapesQuotes(t *testing.T) {
	if got := pwshQuote("a'b"); got != `'a''b'` {
		t.Errorf("got %q", got)
	}
}

func TestPlanResumeUnknown(t *testing.T) {
	if _, err := PlanResume(session.Session{Provider: "nope"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}
