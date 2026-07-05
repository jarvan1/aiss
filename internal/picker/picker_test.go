package picker

import (
	"testing"

	"github.com/jarvan1/aiss/internal/session"
)

func TestMatchProvider(t *testing.T) {
	cases := map[string]string{
		"claude":  "claude", // exact
		"codex":   "codex",
		"cla":     "claude", // unambiguous prefix
		"gem":     "gemini",
		"co":      "", // ambiguous (codex/copilot) → not a provider term
		"c":       "", // ambiguous
		"binance": "", // not a provider
	}
	for term, want := range cases {
		got, _ := matchProvider(term)
		if got != want {
			t.Errorf("matchProvider(%q) = %q, want %q", term, got, want)
		}
	}
}

func newTestPicker() *picker {
	sessions := []session.Session{
		{Provider: "claude", Cwd: "/p/test", Preview: "add an indicator"},
		{Provider: "codex", Cwd: "/p/demo", Preview: "继续完成 claude 没完成的任务"}, // mentions "claude"
		{Provider: "codex", Cwd: "/p/alpha", Preview: "fix the upload step"},
		{Provider: "copilot", Cwd: "/p/beta", Preview: "运行微信报错"},
	}
	m := &picker{sessions: sessions, chosen: -1}
	m.targets = make([]string, len(sessions))
	for i, s := range sessions {
		m.targets[i] = rowLabel(s)
	}
	return m
}

func (m *picker) providersOf() []string {
	var out []string
	for _, i := range m.filtered {
		out = append(out, m.sessions[i].Provider)
	}
	return out
}

func TestRefilterProviderTermExcludesOthers(t *testing.T) {
	m := newTestPicker()

	// "claude" must select only claude sessions, even though a codex prompt
	// contains the word "claude".
	m.input.SetValue("claude")
	m.refilter()
	if got := m.providersOf(); len(got) != 1 || got[0] != "claude" {
		t.Errorf(`search "claude" → providers %v, want [claude]`, got)
	}

	// provider + text term: the codex session whose cwd is /p/alpha.
	m.input.SetValue("codex alpha")
	m.refilter()
	got := m.providersOf()
	if len(got) != 1 || got[0] != "codex" {
		t.Errorf(`search "codex alpha" → providers %v, want [codex]`, got)
	}

	// plain (non-provider) text term matches by row content, whatever the
	// provider — here only the codex "fix the upload step" row.
	m.input.SetValue("upload")
	m.refilter()
	if got := m.providersOf(); len(got) != 1 || got[0] != "codex" {
		t.Errorf(`search "upload" → providers %v, want [codex]`, got)
	}

	// empty query shows everything.
	m.input.SetValue("")
	m.refilter()
	if len(m.filtered) != 4 {
		t.Errorf(`empty query → %d rows, want 4`, len(m.filtered))
	}
}
