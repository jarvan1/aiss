package main

import "testing"

func TestPlanResumeShell(t *testing.T) {
	cases := []struct {
		s    Session
		want string
	}{
		{Session{Provider: "claude", ID: "abc", Cwd: "/tmp/p"}, "( cd '/tmp/p' && claude '--resume' 'abc' )"},
		{Session{Provider: "copilot", ID: "u-1", Cwd: "/tmp/x"}, "( cd '/tmp/x' && copilot '--resume=u-1' )"},
		{Session{Provider: "gemini", ID: "h", Cwd: "/tmp/g"}, "( cd '/tmp/g' && gemini )"},
	}
	for _, c := range cases {
		p, err := planResume(c.s)
		if err != nil {
			t.Fatalf("%s: %v", c.s.Provider, err)
		}
		if got := p.Shell(); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.s.Provider, got, c.want)
		}
	}
}

func TestPlanResumePowerShell(t *testing.T) {
	cases := []struct {
		s    Session
		want string
	}{
		{Session{Provider: "claude", ID: "abc", Cwd: `C:\tmp\p`}, `Push-Location 'C:\tmp\p'; try { & 'claude' '--resume' 'abc' } finally { Pop-Location }`},
		{Session{Provider: "copilot", ID: "u-1", Cwd: `C:\x`}, `Push-Location 'C:\x'; try { & 'copilot' '--resume=u-1' } finally { Pop-Location }`},
		{Session{Provider: "gemini", ID: "h", Cwd: `C:\g`}, `Push-Location 'C:\g'; try { & 'gemini' } finally { Pop-Location }`},
	}
	for _, c := range cases {
		p, err := planResume(c.s)
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
	if _, err := planResume(Session{Provider: "nope"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}
