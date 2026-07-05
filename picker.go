package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/reflow/truncate"
)

// rowLabel is the single-line list entry: provider, cwd, preview.
func rowLabel(s Session) string {
	preview := s.Preview
	if preview == "" {
		preview = "(no prompt)"
	}
	return fmt.Sprintf("%-7s %-34.34s %s", s.Provider, tilde(s.Cwd), preview)
}

var cursorStyle = lipgloss.NewStyle().Bold(true).Reverse(true)

// picker is a top-anchored fuzzy finder (input on top, list left, live preview
// right). It's a small bubbletea model so we control the layout precisely —
// off-the-shelf finders either hardcode the input at the bottom or mis-render
// the preview pane.
type picker struct {
	sessions []Session
	targets  []string // rowLabel per session, used for both search and display
	filtered []int    // indices into sessions, in match order
	input    textinput.Model
	cursor   int // index into filtered (highlighted row)
	offset   int // first visible row (scroll)
	width    int
	height   int
	chosen   int     // index into sessions; -1 = aborted
	pollFd   uintptr // output fd to poll for size (Windows); 0 disables polling
	pollOn   bool    // whether size polling is active
}

// resizeTickMsg drives the Windows size poller (see startPolling).
type resizeTickMsg struct{}

const resizePollInterval = 120 * time.Millisecond

func (m *picker) Init() tea.Cmd {
	if m.pollOn {
		// Prime the size immediately so the first paint is correctly sized even
		// on paths where bubbletea sends no initial WindowSizeMsg, then start the
		// tick loop.
		return tea.Batch(textinput.Blink, m.pollSize(), tea.Tick(resizePollInterval, func(time.Time) tea.Msg {
			return resizeTickMsg{}
		}))
	}
	return textinput.Blink
}

// pollSize reads the current terminal size from pollFd and, if it changed,
// feeds a WindowSizeMsg — the same message a native resize would send. On
// Windows the picker often runs on a console handle (CONIN$/CONOUT$) that
// bubbletea can't deliver resize events for, so we poll instead.
func (m *picker) pollSize() tea.Cmd {
	return func() tea.Msg {
		w, h, err := term.GetSize(m.pollFd)
		if err != nil || w <= 0 || h <= 0 {
			return nil
		}
		if w == m.width && h == m.height {
			return nil
		}
		return tea.WindowSizeMsg{Width: w, Height: h}
	}
}

func (m *picker) bodyHeight() int {
	if m.height <= 1 {
		return 0
	}
	return m.height - 1 // one row for the input line
}

var providers = []string{"claude", "codex", "copilot", "gemini"}

// matchProvider reports the provider a query term selects: an exact name, or an
// unambiguous prefix (e.g. "cla" → claude, but "co" is ambiguous so it's not a
// provider term and falls through to a normal text match).
func matchProvider(term string) (string, bool) {
	for _, p := range providers {
		if p == term {
			return p, true
		}
	}
	hit, n := "", 0
	for _, p := range providers {
		if strings.HasPrefix(p, term) {
			hit, n = p, n+1
		}
	}
	if n == 1 {
		return hit, true
	}
	return "", false
}

// refilter rebuilds the visible list. A provider name (or unambiguous prefix)
// filters by provider; every other space-separated term must appear (case-
// insensitive substring) in the row. This keeps "claude" from matching codex
// sessions whose prompt text merely mentions the word "claude".
func (m *picker) refilter() {
	m.filtered = m.filtered[:0]
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	if q == "" {
		for i := range m.sessions {
			m.filtered = append(m.filtered, i)
		}
		m.clampCursor()
		return
	}

	var prov string
	var textTerms []string
	for _, t := range strings.Fields(q) {
		if p, ok := matchProvider(t); ok {
			prov = p
		} else {
			textTerms = append(textTerms, t)
		}
	}

	for i := range m.sessions {
		if prov != "" && m.sessions[i].Provider != prov {
			continue
		}
		label := strings.ToLower(m.targets[i])
		ok := true
		for _, t := range textTerms {
			if !strings.Contains(label, t) {
				ok = false
				break
			}
		}
		if ok {
			m.filtered = append(m.filtered, i)
		}
	}
	m.clampCursor()
}

func (m *picker) clampCursor() {
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	m.fixScroll()
}

func (m *picker) move(d int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+d, 0), len(m.filtered)-1)
	m.fixScroll()
}

func (m *picker) fixScroll() {
	body := m.bodyHeight()
	if body < 1 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+body {
		m.offset = m.cursor - body + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		return m, nil
	case resizeTickMsg:
		// Poll the size and re-arm the tick. pollSize only emits a
		// WindowSizeMsg when the size actually changed, so idle ticks are cheap.
		return m, tea.Batch(m.pollSize(), tea.Tick(resizePollInterval, func(time.Time) tea.Msg {
			return resizeTickMsg{}
		}))
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.chosen = -1
			return m, tea.Quit
		case "enter":
			if len(m.filtered) > 0 {
				m.chosen = m.filtered[m.cursor]
			}
			return m, tea.Quit
		case "up", "ctrl+p", "ctrl+k":
			m.move(-1)
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			m.move(1)
			return m, nil
		}
	}
	old := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != old {
		m.cursor, m.offset = 0, 0
		m.refilter()
	}
	return m, cmd
}

func (m *picker) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	body := m.bodyHeight()

	leftW := m.width * 45 / 100
	if leftW < 24 {
		leftW = 24
	}
	if leftW > m.width {
		leftW = m.width
	}
	rightW := m.width - leftW - 1 // 1 col for the divider
	showPreview := rightW >= 12

	// --- list (left) ---
	var rows []string
	for i := 0; i < body; i++ {
		idx := m.offset + i
		if idx >= len(m.filtered) {
			rows = append(rows, "")
			continue
		}
		line := "  " + m.targets[m.filtered[idx]]
		if idx == m.cursor {
			line = "> " + m.targets[m.filtered[idx]]
		}
		line = truncate.String(line, uint(leftW))
		if idx == m.cursor {
			line = cursorStyle.Render(line)
		}
		rows = append(rows, line)
	}
	left := lipgloss.NewStyle().Width(leftW).Height(body).Render(strings.Join(rows, "\n"))

	if !showPreview {
		return m.input.View() + "\n" + left
	}

	// --- preview (right) ---
	var prev string
	if len(m.filtered) > 0 {
		prev = Preview(m.sessions[m.filtered[m.cursor]], rightW)
	}
	plines := strings.Split(prev, "\n")
	if len(plines) > body {
		plines = plines[:body]
	}
	for i := range plines {
		plines[i] = truncate.String(plines[i], uint(rightW))
	}
	right := lipgloss.NewStyle().
		Width(rightW).Height(body).
		BorderStyle(lipgloss.NormalBorder()).BorderLeft(true).
		Render(strings.Join(plines, "\n"))

	body2 := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return m.input.View() + "\n" + body2
}

// Pick shows the interactive fuzzy finder and returns the chosen session. The
// bool is false if the user aborted (Esc/Ctrl-C) or nothing matched.
func Pick(sessions []Session, query string) (Session, bool) {
	ti := textinput.New()
	ti.Prompt = "ai-sessions ❯ "
	ti.SetValue(query)
	ti.Focus()
	ti.CursorEnd()

	m := &picker{sessions: sessions, input: ti, chosen: -1}
	m.targets = make([]string, len(sessions))
	for i, s := range sessions {
		m.targets[i] = rowLabel(s)
	}
	m.refilter()

	// Render the TUI directly on the controlling terminal, like fzf does. This
	// is essential for the shell/pwsh widgets, which run us with stdout captured
	// (`cmd=$(aiss --print ...)` / `$cmd = & aiss --print --pwsh`): a TUI on
	// stdout would be invisible and, on Windows, the inherited stdin carries no
	// key events. openConsole grabs /dev/tty (POSIX) or CONIN$/CONOUT$ (Windows);
	// both input and output must go there. Falls back to stderr if unavailable.
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if in, out, closeConsole, ok := openConsole(); ok {
		defer closeConsole()
		opts = append(opts, tea.WithInput(in), tea.WithOutput(out))
		// On Windows the picker often draws on a console handle bubbletea can't
		// deliver resize events for, so it polls the output size instead (no-op
		// on POSIX, which gets native resize events). enablePolling is true only
		// on Windows and only when out is a real terminal.
		if enablePolling && term.IsTerminal(out.Fd()) {
			m.pollFd, m.pollOn = out.Fd(), true
		}
	} else {
		opts = append(opts, tea.WithOutput(os.Stderr))
		if enablePolling && term.IsTerminal(os.Stderr.Fd()) {
			m.pollFd, m.pollOn = os.Stderr.Fd(), true
		}
	}

	res, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return Session{}, false
	}
	fm := res.(*picker)
	if fm.chosen < 0 {
		return Session{}, false
	}
	return sessions[fm.chosen], true
}
