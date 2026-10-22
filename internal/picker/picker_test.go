package picker

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/jarvan1/aiss/internal/session"
	"github.com/muesli/termenv"
)

func scrollTestPicker() *picker {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 120, 16
	m.previewKey = previewKey{file: m.sessions[0].File, width: 65}
	var text strings.Builder
	text.WriteString("\x1b[36m╭─ fixture-model ─╮\x1b[0m\n│ project\n│ timestamp\n│ counts\n╰────────────────╯\n\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&text, "message-%02d\n", i)
	}
	m.previewText = text.String()
	m.previewDoc = preparePreview(m.previewText, 65)
	return m
}

func wheel(x, y int, down bool) tea.MouseMsg {
	button := tea.MouseButtonWheelUp
	if down {
		button = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{X: x, Y: y, Button: button, Action: tea.MouseActionPress}
}

func TestWheelOverPreviewScrollsOnlyTranscript(t *testing.T) {
	m := scrollTestPicker()
	m.previewer = func(session.Session, int) string { t.Fatal("scroll must not read history"); return "" }
	before := strings.Split(m.View(), "\n")
	_, cmd := m.Update(wheel(100, 10, true))
	if cmd != nil {
		t.Fatal("preview scrolling must not schedule a full repaint or history load")
	}
	if m.cursor != 0 || m.previewOffset != 3 {
		t.Fatalf("cursor=%d, preview offset=%d, want 0 and 3", m.cursor, m.previewOffset)
	}
	after := strings.Split(m.View(), "\n")
	for i := 0; i <= len(m.previewDoc.header); i++ {
		if before[i] != after[i] {
			t.Fatalf("fixed header changed on row %d", i)
		}
	}
	if strings.Contains(m.View(), "message-00") || !strings.Contains(m.View(), "message-03") {
		t.Fatal("preview did not scroll past the initial transcript lines")
	}
	m.Update(wheel(100, 10, false))
	if m.previewOffset != 0 || !strings.Contains(m.View(), "message-00") {
		t.Fatal("scrolling up did not restore the initial transcript")
	}
}

func TestWheelOverListSwitchesSessionAndResetsPreview(t *testing.T) {
	m := scrollTestPicker()
	m.previewOffset = 12
	m.previewCache = map[previewKey]*previewDocument{
		{file: m.sessions[1].File, width: 65}: preparePreview("next session", 65),
	}
	_, cmd := m.Update(wheel(4, 8, true))
	if m.cursor != 1 || cmd == nil {
		t.Fatalf("left wheel: cursor=%d, command=%v", m.cursor, cmd != nil)
	}
	m.Update(cmd()) // in-place redraw schedules the newly selected preview
	if m.previewOffset != 0 || m.previewText != "next session" {
		t.Fatalf("cached session did not start at top: offset=%d text=%q", m.previewOffset, m.previewText)
	}
}

func TestPreviewScrollBoundsAndResize(t *testing.T) {
	m := scrollTestPicker()
	for i := 0; i < 100; i++ {
		m.Update(wheel(100, 10, true))
	}
	want := len(m.previewDoc.lines) - (m.bodyHeight() - len(m.previewDoc.header))
	if m.previewOffset != want || !strings.Contains(m.View(), "message-59") {
		t.Fatalf("bottom offset=%d, want %d with last message visible", m.previewOffset, want)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 80})
	if m.previewOffset != 0 {
		t.Fatal("growing the pane must clamp the old scroll offset")
	}
	for i := 0; i < 100; i++ {
		m.Update(wheel(100, 10, false))
	}
	if m.previewOffset != 0 {
		t.Fatal("scrolling above the beginning must clamp at zero")
	}
}

func TestWheelIgnoresPromptDividerAndDeleteConfirmation(t *testing.T) {
	for _, pos := range [][2]int{{100, 0}, {53, 8}, {120, 8}, {100, 16}} {
		m := scrollTestPicker()
		m.Update(wheel(pos[0], pos[1], true))
		if m.cursor != 0 || m.previewOffset != 0 {
			t.Fatalf("outside-pane wheel changed selection at %v", pos)
		}
	}
	m := scrollTestPicker()
	m.confirming = true
	m.Update(wheel(100, 10, true))
	m.Update(wheel(4, 10, true))
	if !m.confirming || m.cursor != 0 || m.previewOffset != 0 {
		t.Fatal("wheel must not change a pending delete target")
	}
}

func TestWheelSupportsProfileListWhileKeepingPreviewIndependent(t *testing.T) {
	m := scrollTestPicker()
	m.profiles = []session.CodexProfile{{Name: "personal"}, {Name: "work"}}
	m.choosingProfile = true
	m.Update(wheel(4, 10, true))
	if m.profileCursor != 1 || m.cursor != 0 {
		t.Fatal("left wheel should move only the profile cursor")
	}
	m.Update(wheel(100, 10, true))
	if m.profileCursor != 1 || m.previewOffset != 3 {
		t.Fatal("right wheel should scroll the same preview in the profile stage")
	}
}

func TestWrappedPreviewKeepsUnicodeAndTerminalWidth(t *testing.T) {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 12
	_, width, _ := m.paneWidths()
	text := "\x1b[36m" + strings.Repeat("中文🙂内容", 40) + "END-OF-MESSAGE\x1b[0m"
	m.previewText = text
	m.previewDoc = preparePreview(text, width)
	if strings.Join(strings.Split(ansi.Strip(strings.Join(m.previewDoc.lines, "")), "\n"), "") != ansi.Strip(text) {
		t.Fatal("wrapping lost transcript content")
	}
	for _, delta := range []int{0, 3, 1000, -1000} {
		m.scrollPreview(delta)
		view := m.View()
		if lipgloss.Height(view) != m.height {
			t.Fatal("wrapped preview changed terminal height")
		}
		for _, line := range strings.Split(view, "\n") {
			if lipgloss.Width(line) >= m.width {
				t.Fatal("wrapped preview used the terminal's last column")
			}
		}
	}
}

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

func TestCodexProfileAppearsInRowsAndCanBeFiltered(t *testing.T) {
	m := newTestPicker()
	m.sessions[1].Profile = "personal"
	m.sessions[2].Profile = "work"
	for i, s := range m.sessions {
		m.targets[i] = rowLabel(s)
	}
	if !strings.Contains(m.targets[1], "codex:personal") || !strings.Contains(m.targets[2], "codex:work") {
		t.Fatalf("profile labels missing: %q / %q", m.targets[1], m.targets[2])
	}
	m.input.SetValue("codex work")
	m.refilter()
	if len(m.filtered) != 1 || m.sessions[m.filtered[0]].Profile != "work" {
		t.Fatalf("filtered sessions = %#v", m.filtered)
	}
}

func TestCodexEnterOpensProfileChoiceWithDetectedProfileSelected(t *testing.T) {
	m := newTestPicker()
	m.profiles = []session.CodexProfile{
		{Name: "personal", Home: "/profiles/personal", SQLiteHome: "/runtime"},
		{Name: "work", Home: "/profiles/work", SQLiteHome: "/runtime"},
	}
	m.sessions[1].Profile = "work"
	m.sessions[1].CodexHome = "/profiles/work"
	m.refilter()
	m.cursor = 1

	m.Update(keyMsg("enter"))
	if !m.choosingProfile {
		t.Fatal("Enter on Codex should open the profile picker")
	}
	if m.profileCursor != 1 {
		t.Fatalf("profile cursor = %d, want detected work profile at 1", m.profileCursor)
	}
	if m.chosen != -1 {
		t.Fatalf("session was chosen before profile confirmation: %d", m.chosen)
	}
}

func TestProfileChoiceCanOverrideResumeEnvironment(t *testing.T) {
	m := newTestPicker()
	m.profiles = []session.CodexProfile{
		{Name: "personal", Home: "/profiles/personal", SQLiteHome: "/runtime"},
		{Name: "work", Home: "/profiles/work", SQLiteHome: "/runtime"},
	}
	m.sessions[1].Profile = "personal"
	m.sessions[1].CodexHome = "/profiles/personal"
	m.refilter()
	m.cursor = 1
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("down"))
	m.Update(keyMsg("enter"))

	if m.chosen != 1 {
		t.Fatalf("chosen = %d, want session index 1", m.chosen)
	}
	got := m.sessions[m.chosen]
	if got.Profile != "work" || got.CodexHome != "/profiles/work" || got.CodexSQLiteHome != "/runtime" {
		t.Fatalf("chosen session profile = %#v", got)
	}
}

func TestProfileChoiceEscReturnsToSessionList(t *testing.T) {
	m := newTestPicker()
	m.profiles = []session.CodexProfile{{Name: "personal"}, {Name: "work"}}
	m.refilter()
	m.cursor = 1
	m.Update(keyMsg("enter"))
	m.Update(keyMsg("esc"))
	if m.choosingProfile {
		t.Fatal("Esc should return to the session list")
	}
	if m.chosen != -1 {
		t.Fatalf("Esc from profile choice should not choose a session: %d", m.chosen)
	}
}

func TestNonCodexEnterDoesNotOpenProfileChoice(t *testing.T) {
	m := newTestPicker()
	m.profiles = []session.CodexProfile{{Name: "personal"}, {Name: "work"}}
	m.refilter()
	m.cursor = 0 // Claude
	m.Update(keyMsg("enter"))
	if m.choosingProfile {
		t.Fatal("non-Codex sessions must not open the profile picker")
	}
	if m.chosen != 0 {
		t.Fatalf("chosen = %d, want Claude session 0", m.chosen)
	}
}

func TestProfileChoiceViewKeepsPreviewAndShowsDetectedProfile(t *testing.T) {
	m := newTestPicker()
	m.profiles = []session.CodexProfile{
		{Name: "personal", Home: "/profiles/personal"},
		{Name: "work", Home: "/profiles/work"},
	}
	m.sessions[1].Profile = "work"
	m.sessions[1].CodexHome = "/profiles/work"
	m.refilter()
	m.cursor = 1
	m.width, m.height = 100, 10
	m.previewText = "existing preview"
	m.Update(keyMsg("enter"))

	view := m.View()
	for _, want := range []string{"codex profile", "personal", "work", "(detected)", "existing preview"} {
		if !strings.Contains(view, want) {
			t.Fatalf("profile view missing %q: %q", want, view)
		}
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

func TestViewLeavesLastTerminalColumnUnused(t *testing.T) {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 30
	m.del = func(session.Session) error { return nil }
	m.previewText = strings.Repeat("一段很长的预览文字\n", 40)

	view := m.View()
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("view height = %d, exceeds terminal height %d", got, m.height)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got >= m.width {
			t.Fatalf("line %d width = %d, must stay below terminal width %d", i+1, got, m.width)
		}
	}
}

func TestFullRepaintReusesWindowSizeWithoutClearing(t *testing.T) {
	m := newTestPicker()
	m.width, m.height = 120, 40

	msg, ok := m.fullRepaint()().(tea.WindowSizeMsg)
	if !ok {
		t.Fatal("full repaint must use WindowSizeMsg instead of clearing the screen")
	}
	if msg.Width != m.width || msg.Height != m.height {
		t.Fatalf("repaint size = %dx%d, want %dx%d", msg.Width, msg.Height, m.width, m.height)
	}
}

func TestViewKeepsStyledPreviewTitleLine(t *testing.T) {
	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 30
	m.previewText = "\x1b[36m╭─ \x1b[1mcodex\x1b[0m\x1b[36m · codex-auto-review ─────╮\x1b[0m\n" +
		"\x1b[36m│\x1b[0m 📁 ~"

	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) < 2 || !strings.Contains(lines[1], "codex-auto-review") {
		t.Fatalf("first preview row lost its styled title: %q", view)
	}
}

func TestUseRendererKeepsSelectionStyledWhenStdoutIsCaptured(t *testing.T) {
	previous := lipgloss.DefaultRenderer()
	t.Cleanup(func() { useRenderer(previous) })

	// A shell widget captures stdout, so Lipgloss's process-wide default would
	// otherwise be the no-style ASCII profile.
	captured := lipgloss.NewRenderer(io.Discard)
	captured.SetColorProfile(termenv.Ascii)
	lipgloss.SetDefaultRenderer(captured)

	// Bubble Tea renders to the controlling terminal instead. Bind styles to a
	// renderer representing that output and verify both our selected row and
	// the text input cursor retain terminal attributes.
	terminal := lipgloss.NewRenderer(io.Discard)
	terminal.SetColorProfile(termenv.ANSI)
	useRenderer(terminal)

	m := newTestPicker()
	m.refilter()
	m.width, m.height = 100, 10
	if view := m.View(); !strings.Contains(view, "\x1b[1;7m") {
		t.Fatalf("selected row lost reverse-video styling: %q", view)
	}

	ti := textinput.New()
	ti.Focus()
	ti.Cursor.Blink = false
	if view := ti.View(); !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("text input cursor lost reverse-video styling: %q", view)
	}
}

var errTest = fmtError("boom")

type fmtError string

func (e fmtError) Error() string { return string(e) }

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}
