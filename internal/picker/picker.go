// Package picker is a top-anchored fuzzy finder over discovered sessions, with
// a live preview pane. It's a small bubbletea model so the layout is controlled
// precisely — off-the-shelf finders either hardcode the input at the bottom or
// mis-render the preview pane.
package picker

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/jarvan1/aiss/internal/preview"
	"github.com/jarvan1/aiss/internal/session"
	"github.com/muesli/reflow/truncate"
)

// rowLabel is the single-line list entry: provider, cwd, preview.
func rowLabel(s session.Session) string {
	prev := s.Preview
	if prev == "" {
		prev = "(no prompt)"
	}
	return fmt.Sprintf("%-7s %-34.34s %s", s.Provider, session.Tilde(s.Cwd), prev)
}

var (
	cursorStyle  = lipgloss.NewStyle().Bold(true).Reverse(true)
	confirmStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")) // red
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red key hint
)

// deleteHint is the shortcut reminder shown at the right of the search line.
const deleteHint = "Ctrl+D delete"

// deleteFunc removes a session from disk. Injected so the picker doesn't import
// the del package directly (keeps it testable and decoupled).
type deleteFunc func(session.Session) error

// previewFunc renders one session's preview. It is a field on picker so tests
// can prove that rendering happens in a tea.Cmd rather than on the UI goroutine.
type previewFunc func(session.Session, int) string

type previewKey struct {
	file  string
	width int
}

type previewRequestMsg struct {
	key     previewKey
	session session.Session
	token   uint64
}

type previewLoadedMsg struct {
	key  previewKey
	text string
}

const previewDebounce = 75 * time.Millisecond

// picker is the bubbletea model backing Pick.
type picker struct {
	sessions []session.Session
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

	del        deleteFunc // removes a session from disk; nil disables deletion
	confirming bool       // awaiting y/N on a pending delete
	pendingRow int        // index into filtered marked for deletion
	status     string     // transient message shown on the prompt line

	previewer      previewFunc
	previewCache   map[previewKey]string
	previewKey     previewKey
	previewText    string
	previewLoading bool
	previewToken   uint64
}

// resizeTickMsg drives the Windows size poller (see Init).
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

// paneWidths returns the same layout dimensions View uses. Keeping this in one
// place also lets the async preview loader use the exact render width.
func (m *picker) paneWidths() (leftW, rightW int, showPreview bool) {
	leftW = m.width * 45 / 100
	if leftW < 24 {
		leftW = 24
	}
	if leftW > m.width {
		leftW = m.width
	}
	rightW = m.width - leftW - 1 // 1 col for the divider
	return leftW, rightW, rightW >= 12
}

// queuePreview schedules a debounced preview load for the highlighted row.
// Session files produced by Codex Desktop can be hundreds of MB, so parsing one
// synchronously from View would block every repaint and make selection appear
// frozen. The token discards requests superseded by quick cursor movement.
func (m *picker) queuePreview() tea.Cmd {
	_, rightW, showPreview := m.paneWidths()
	if !showPreview || len(m.filtered) == 0 {
		m.previewToken++
		m.previewKey = previewKey{}
		m.previewText = ""
		m.previewLoading = false
		return nil
	}

	s := m.sessions[m.filtered[m.cursor]]
	key := previewKey{file: s.File, width: rightW}
	if key == m.previewKey && (m.previewLoading || m.previewText != "") {
		return nil
	}

	m.previewToken++
	m.previewKey = key
	if text, ok := m.previewCache[key]; ok {
		m.previewText = text
		m.previewLoading = false
		return nil
	}

	m.previewText = ""
	m.previewLoading = true
	token := m.previewToken
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewRequestMsg{key: key, session: s, token: token}
	})
}

func (m *picker) loadPreview(msg previewRequestMsg) tea.Cmd {
	previewer := m.previewer
	if previewer == nil {
		previewer = preview.Preview
	}
	return func() tea.Msg {
		return previewLoadedMsg{key: msg.key, text: previewer(msg.session, msg.key.width)}
	}
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

// doDelete removes the session under pendingRow from disk and from the model,
// leaving the highlight on a sensible neighbor. Called only after y-confirm.
func (m *picker) doDelete() {
	m.confirming = false
	if m.del == nil || m.pendingRow < 0 || m.pendingRow >= len(m.filtered) {
		m.status = ""
		return
	}
	si := m.filtered[m.pendingRow]
	s := m.sessions[si]
	if err := m.del(s); err != nil {
		m.status = "delete failed: " + err.Error()
		return
	}
	m.dropSession(si)
	m.status = "deleted " + s.Provider + " " + session.Tilde(s.Cwd)
}

// dropSession removes sessions[si] and its parallel targets entry, then rebuilds
// the filtered view. filtered holds indices into sessions, so every index past
// si shifts down by one — refilter() recomputes them from scratch.
func (m *picker) dropSession(si int) {
	m.sessions = append(m.sessions[:si], m.sessions[si+1:]...)
	m.targets = append(m.targets[:si], m.targets[si+1:]...)
	if m.cursor >= len(m.filtered)-1 {
		m.cursor = max(0, m.cursor-1)
	}
	m.refilter() // rebuilds filtered against the shrunk sessions slice
}

func (m *picker) clampCursor() {
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	m.fixScroll()
}

func (m *picker) move(d int) {
	m.status = ""
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
		return m, m.queuePreview()
	case resizeTickMsg:
		// Poll the size and re-arm the tick. pollSize only emits a
		// WindowSizeMsg when the size actually changed, so idle ticks are cheap.
		return m, tea.Batch(m.pollSize(), tea.Tick(resizePollInterval, func(time.Time) tea.Msg {
			return resizeTickMsg{}
		}))
	case previewRequestMsg:
		if msg.token != m.previewToken || msg.key != m.previewKey {
			return m, nil
		}
		return m, m.loadPreview(msg)
	case previewLoadedMsg:
		if m.previewCache == nil {
			m.previewCache = make(map[previewKey]string)
		}
		m.previewCache[msg.key] = msg.text
		if msg.key == m.previewKey {
			m.previewText = msg.text
			m.previewLoading = false
		}
		return m, nil
	case tea.KeyMsg:
		// While confirming a delete, keys answer y/N and nothing else.
		if m.confirming {
			switch msg.String() {
			case "y", "Y":
				m.doDelete()
				return m, m.queuePreview()
			default: // any other key cancels
				m.confirming = false
				m.status = ""
			}
			return m, nil
		}
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
			old := m.cursor
			m.move(-1)
			if m.cursor != old {
				return m, m.queuePreview()
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			old := m.cursor
			m.move(1)
			if m.cursor != old {
				return m, m.queuePreview()
			}
			return m, nil
		case "ctrl+d":
			// Arm a delete on the highlighted row; requires y confirmation.
			if m.del != nil && len(m.filtered) > 0 {
				m.confirming = true
				m.pendingRow = m.cursor
			}
			return m, nil
		}
	}
	old := m.input.Value()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() != old {
		m.cursor, m.offset = 0, 0
		m.status = ""
		m.refilter()
		return m, tea.Batch(cmd, m.queuePreview())
	}
	return m, cmd
}

// topLine is the first row: the search input, or a delete confirm/status line.
func (m *picker) topLine() string {
	if m.confirming && m.pendingRow >= 0 && m.pendingRow < len(m.filtered) {
		s := m.sessions[m.filtered[m.pendingRow]]
		return confirmStyle.Render(fmt.Sprintf("delete %s  %s ?  (y/N)", s.Provider, session.Tilde(s.Cwd)))
	}
	if m.status != "" {
		return statusStyle.Render(m.status)
	}
	in := m.input.View()
	// When deletion is enabled, right-align a dim "^d delete" reminder on the
	// search line so the shortcut is discoverable. Dropped if the terminal is
	// too narrow to fit it without crowding the input.
	if m.del != nil && m.width > 0 {
		hint := hintStyle.Render(deleteHint)
		gap := m.width - lipgloss.Width(in) - lipgloss.Width(hint)
		if gap >= 2 {
			return in + strings.Repeat(" ", gap) + hint
		}
	}
	return in
}

func (m *picker) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	body := m.bodyHeight()

	leftW, rightW, showPreview := m.paneWidths()

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
		return m.topLine() + "\n" + left
	}

	// --- preview (right) ---
	prev := m.previewText
	if m.previewLoading {
		prev = hintStyle.Render("Loading preview…")
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
	return m.topLine() + "\n" + body2
}

// Pick shows the interactive fuzzy finder and returns the chosen session. The
// bool is false if the user aborted (Esc/Ctrl-C) or nothing matched. del, if
// non-nil, enables Ctrl-D to delete the highlighted session from disk.
func Pick(sessions []session.Session, query string, del func(session.Session) error) (session.Session, bool) {
	ti := textinput.New()
	ti.Prompt = "ai-sessions ❯ "
	ti.SetValue(query)
	ti.Focus()
	ti.CursorEnd()

	m := &picker{
		sessions:     sessions,
		input:        ti,
		chosen:       -1,
		del:          del,
		previewer:    preview.Preview,
		previewCache: make(map[previewKey]string),
	}
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
		return session.Session{}, false
	}
	fm := res.(*picker)
	if fm.chosen < 0 {
		return session.Session{}, false
	}
	return sessions[fm.chosen], true
}
