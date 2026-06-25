package main

import "testing"

func TestMatchProvider(t *testing.T) {
	cases := map[string]string{
		"claude":  "claude", // exact
		"codex":   "codex",
		"cla":     "claude", // unambiguous prefix
		"gem":     "gemini",
		"co":      "",       // ambiguous (codex/copilot) → not a provider term
		"c":       "",       // ambiguous
		"binance": "",       // not a provider
	}
	for term, want := range cases {
		got, _ := matchProvider(term)
		if got != want {
			t.Errorf("matchProvider(%q) = %q, want %q", term, got, want)
		}
	}
}

func newTestPicker() *picker {
	sessions := []Session{
		{Provider: "claude", Cwd: "/p/binance", Preview: "add an indicator"},
		{Provider: "codex", Cwd: "/p/multica", Preview: "继续完成 claude 没完成的任务"}, // mentions "claude"
		{Provider: "codex", Cwd: "/p/binance", Preview: "fix the upload step"},
		{Provider: "copilot", Cwd: "/p/xsh", Preview: "运行微信报错"},
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

	// provider + text term: codex sessions about binance.
	m.input.SetValue("codex binance")
	m.refilter()
	got := m.providersOf()
	if len(got) != 1 || got[0] != "codex" {
		t.Errorf(`search "codex binance" → providers %v, want [codex]`, got)
	}

	// plain text term spans providers.
	m.input.SetValue("binance")
	m.refilter()
	if len(m.filtered) != 2 {
		t.Errorf(`search "binance" → %d rows, want 2`, len(m.filtered))
	}

	// empty query shows everything.
	m.input.SetValue("")
	m.refilter()
	if len(m.filtered) != 4 {
		t.Errorf(`empty query → %d rows, want 4`, len(m.filtered))
	}
}
