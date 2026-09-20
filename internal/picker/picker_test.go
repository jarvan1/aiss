package picker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
		{Provider: "claude", Cwd: "/p/test", File: "/sessions/claude.jsonl", Preview: "add an indicator"},
		{Provider: "codex", Cwd: "/p/demo", File: "/sessions/codex-demo.jsonl", Preview: "继续完成 claude 没完成的任务"}, // mentions "claude"
		{Provider: "codex", Cwd: "/p/alpha", File: "/sessions/codex-alpha.jsonl", Preview: "fix the upload step"},
		{Provider: "copilot", Cwd: "/p/beta", File: "/sessions/copilot.jsonl", Preview: "运行微信报错"},
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

func TestDeleteConfirmDropsRow(t *testing.T) {
	m := newTestPicker()
	var deleted []session.Session
	m.del = func(s session.Session) error {
		deleted = append(deleted, s)
		return nil
	}
	m.refilter() // all 4 rows
	m.cursor = 1 // the codex "/p/demo" row

	// Ctrl-D arms confirmation but deletes nothing yet.
	m.Update(keyMsg("ctrl+d"))
	if !m.confirming {
		t.Fatal("ctrl+d should enter confirming state")
	}
	if len(deleted) != 0 {
		t.Fatal("nothing should be deleted before y")
	}

	// A non-y key cancels without deleting.
	m.Update(keyMsg("n"))
	if m.confirming || len(deleted) != 0 {
		t.Fatalf("n should cancel: confirming=%v deleted=%d", m.confirming, len(deleted))
	}

	// Ctrl-D then y deletes and drops the row.
	m.cursor = 1
	m.Update(keyMsg("ctrl+d"))
	m.Update(keyMsg("y"))
	if len(deleted) != 1 || deleted[0].Cwd != "/p/demo" {
		t.Fatalf("expected /p/demo deleted, got %v", deleted)
	}
	if len(m.sessions) != 3 || len(m.filtered) != 3 {
		t.Fatalf("row not dropped: sessions=%d filtered=%d", len(m.sessions), len(m.filtered))
	}
	for _, s := range m.sessions {
		if s.Cwd == "/p/demo" {
			t.Error("/p/demo still present in sessions")
		}
	}
}

func TestDeleteFailureKeepsRow(t *testing.T) {
	m := newTestPicker()
	m.del = func(session.Session) error { return errTest }
	m.refilter()
	m.cursor = 0
	m.Update(keyMsg("ctrl+d"))
	m.Update(keyMsg("y"))
	if len(m.sessions) != 4 {
		t.Fatalf("failed delete should keep the row, got %d sessions", len(m.sessions))
	}
	if m.status == "" {
		t.Error("expected an error status after failed delete")
	}
}

func TestTopLineDeleteHint(t *testing.T) {
	m := newTestPicker()
	m.input.Prompt = "ai-sessions ❯ "
	m.width = 80

	// No hint until deletion is enabled.
	if strings.Contains(m.topLine(), deleteHint) {
		t.Error("hint should be absent when del is nil")
	}

	// With del set and a wide terminal, the hint is shown.
	m.del = func(session.Session) error { return nil }
	if !strings.Contains(m.topLine(), deleteHint) {
		t.Errorf("expected %q on the search line, got %q", deleteHint, m.topLine())
	}

	// A narrow terminal drops it rather than crowding the input.
	m.width = 8
	if strings.Contains(m.topLine(), deleteHint) {
		t.Error("hint should be dropped on a narrow terminal")
	}

	// The confirm prompt takes over the line entirely (no hint).
	m.width = 80
	m.confirming = true
	m.pendingRow = 0
	m.filtered = []int{0}
	if strings.Contains(m.topLine(), deleteHint) {
		t.Error("hint should not appear during a delete confirm")
	}
}

func TestPreviewLoadsOutsideView(t *testing.T) {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 30
	calls := 0
	m.previewer = func(s session.Session, width int) string {
		calls++
		return "preview for " + s.Cwd
	}

	// Scheduling and rendering the loading state must not parse the session on
	// the UI goroutine.
	if cmd := m.queuePreview(); cmd == nil {
		t.Fatal("expected a deferred preview request")
	}
	_ = m.View()
	if calls != 0 {
		t.Fatalf("View called previewer %d times, want 0", calls)
	}

	request := previewRequestMsg{
		key:     m.previewKey,
		session: m.sessions[m.filtered[m.cursor]],
		token:   m.previewToken,
	}
	_, load := m.Update(request)
	if load == nil || calls != 0 {
		t.Fatalf("request should return an async load: cmd=%v calls=%d", load != nil, calls)
	}
	loaded, ok := load().(previewLoadedMsg)
	if !ok {
		t.Fatal("preview load returned the wrong message type")
	}
	if calls != 1 {
		t.Fatalf("previewer calls = %d, want 1", calls)
	}
	m.Update(loaded)
	if !strings.Contains(m.View(), "preview for /p/test") {
		t.Fatal("loaded preview was not rendered")
	}
}

func TestStalePreviewRequestIsDiscarded(t *testing.T) {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 30
	m.previewer = func(session.Session, int) string { return "unexpected" }

	m.queuePreview()
	stale := previewRequestMsg{
		key:     m.previewKey,
		session: m.sessions[m.filtered[m.cursor]],
		token:   m.previewToken,
	}
	m.move(1)
	m.queuePreview()
	_, cmd := m.Update(stale)
	if cmd != nil {
		t.Fatal("superseded preview request should not start a load")
	}
}

var errTest = fmtError("boom")

type fmtError string

func (e fmtError) Error() string { return string(e) }

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
