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
	"github.com/charmbracelet/x/ansi"
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
	return fmt.Sprintf("%-15.15s %-26.26s %s", s.ProviderLabel(), session.Tilde(s.Cwd), prev)
}

var (
	cursorStyle  = lipgloss.NewStyle().Bold(true).Reverse(true)
	confirmStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")) // red
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))            // green
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))            // red key hint
)

// useRenderer binds every Lipgloss style, including styles created later by
// bubbles/textinput, to the terminal Bubble Tea actually draws on. Shell
// widgets capture stdout to obtain the selected resume command, so the default
// Lipgloss renderer otherwise sees a pipe, disables terminal attributes, and
// turns the highlighted row into a bare ">" marker.
func useRenderer(r *lipgloss.Renderer) {
	lipgloss.SetDefaultRenderer(r)
	cursorStyle = r.NewStyle().Bold(true).Reverse(true)
	confirmStyle = r.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	statusStyle = r.NewStyle().Foreground(lipgloss.Color("2"))
	hintStyle = r.NewStyle().Foreground(lipgloss.Color("1"))
}

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
	key      previewKey
	document *previewDocument
}

type previewDocument struct {
	text   string
	header []string // fixed metadata box
	lines  []string // wrapped transcript, prepared off the UI goroutine
}

func preparePreview(text string, width int) *previewDocument {
	doc := &previewDocument{text: text}
	body := text
	if head, rest, ok := strings.Cut(text, "\n\n"); ok {
		lines := strings.Split(head, "\n")
		if strings.HasPrefix(ansi.Strip(lines[0]), "╭") &&
			strings.HasPrefix(ansi.Strip(lines[len(lines)-1]), "╰") {
			doc.header = append(lines, "")
			body = rest
		}
	}
	doc.lines = strings.Split(ansi.Hardwrap(strings.TrimRight(body, "\n"), max(1, width), true), "\n")
	return doc
}

const previewDebounce = 75 * time.Millisecond

// picker is the bubbletea model backing Pick.
type picker struct {
	sessions []session.Session
	profiles []session.CodexProfile
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

	choosingProfile bool // second-stage Codex profile selector is active
	profileCursor   int  // highlighted entry in profiles
	profileOffset   int  // first visible profile when the list is taller than the pane

	previewer      previewFunc
	previewCache   map[previewKey]*previewDocument
	previewKey     previewKey
	previewText    string
	previewDoc     *previewDocument
	previewOffset  int
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

// renderWidth leaves the terminal's last column unused. Some embedded
// terminals immediately auto-wrap when a glyph is written into that column;
// Bubble Tea then emits its own CRLF and the extra line gradually scrolls the
// search prompt off-screen, leaving stale cursor markers behind.
func (m *picker) renderWidth() int {
	return max(0, m.width-1)
}

// paneWidths returns the same layout dimensions View uses. Keeping this in one
// place also lets the async preview loader use the exact render width.
func (m *picker) paneWidths() (leftW, rightW int, showPreview bool) {
	usableW := m.renderWidth()
	leftW = usableW * 45 / 100
	if leftW < 24 {
		leftW = 24
	}
	if leftW > usableW {
		leftW = usableW
	}
	rightW = usableW - leftW - 1 // 1 col for the divider
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
		m.previewDoc = nil
		m.previewOffset = 0
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
	m.previewOffset = 0
	if doc, ok := m.previewCache[key]; ok {
		m.previewText = doc.text
		m.previewDoc = doc
		m.previewLoading = false
		return nil
	}

	m.previewText = ""
	m.previewDoc = nil
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
		text := previewer(msg.session, msg.key.width)
		return previewLoadedMsg{key: msg.key, document: preparePreview(text, msg.key.width)}
	}
}

func (m *picker) currentPreview() *previewDocument {
	if m.previewDoc != nil && m.previewDoc.text == m.previewText {
		return m.previewDoc
	}
	_, width, _ := m.paneWidths()
	return preparePreview(m.previewText, width)
}

func (m *picker) scrollPreview(delta int) {
	if m.previewLoading {
		return
	}
	doc := m.currentPreview()
	visible := m.bodyHeight() - len(doc.header)
	if visible <= 0 {
		return
	}
	m.previewOffset = min(max(0, m.previewOffset+delta), max(0, len(doc.lines)-visible))
}

func (m *picker) mouseWheel(msg tea.MouseMsg) tea.Cmd {
	delta := 0
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		delta = -1
	case tea.MouseButtonWheelDown:
		delta = 1
	default:
		return nil
	}
	if m.confirming || msg.Y < 1 || msg.Y >= m.height || msg.X < 0 || msg.X >= m.renderWidth() {
		return nil
	}
	leftW, _, showPreview := m.paneWidths()
	if showPreview && msg.X > leftW {
		m.scrollPreview(delta * 3)
		return nil // differential repaint; no file read or full-screen refresh
	}
	if showPreview && msg.X == leftW {
		return nil // divider
	}
	if m.choosingProfile {
		m.moveProfile(delta)
	} else {
		m.move(delta)
	}
	return m.fullRepaint()
}

// fullRepaint asks Bubble Tea to invalidate its differential-render cache and
// overwrite every row in place. Some embedded terminals disagree with the
// renderer about wide-rune column positions, which can otherwise leave an old
// highlighted row visible. Re-sending the current size triggers a repaint
// without ClearScreen's visible flash.
func (m *picker) fullRepaint() tea.Cmd {
	w, h := m.width, m.height
	return func() tea.Msg {
		return tea.WindowSizeMsg{Width: w, Height: h}
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

// beginProfileChoice enters the second picker stage for Codex sessions when
// more than one configuration home is available. The originating profile is
// preselected, but the user can deliberately resume with another account.
func (m *picker) beginProfileChoice() bool {
	if len(m.filtered) == 0 || len(m.profiles) < 2 {
		return false
	}
	s := m.sessions[m.filtered[m.cursor]]
	if s.Provider != "codex" {
		return false
	}
	m.choosingProfile = true
	m.profileCursor, m.profileOffset = 0, 0
	for i, profile := range m.profiles {
		if (s.CodexHome != "" && profile.Home == s.CodexHome) ||
			(s.CodexHome == "" && s.Profile != "" && profile.Name == s.Profile) {
			m.profileCursor = i
			break
		}
	}
	m.fixProfileScroll()
	return true
}

func (m *picker) moveProfile(d int) {
	if len(m.profiles) == 0 {
		return
	}
	m.profileCursor = min(max(m.profileCursor+d, 0), len(m.profiles)-1)
	m.fixProfileScroll()
}

func (m *picker) fixProfileScroll() {
	body := m.bodyHeight()
	if body < 1 {
		return
	}
	if m.profileCursor < m.profileOffset {
		m.profileOffset = m.profileCursor
	}
	if m.profileCursor >= m.profileOffset+body {
		m.profileOffset = m.profileCursor - body + 1
	}
	if m.profileOffset < 0 {
		m.profileOffset = 0
	}
}

func (m *picker) chooseProfile() {
	if len(m.filtered) == 0 || m.profileCursor < 0 || m.profileCursor >= len(m.profiles) {
		return
	}
	si := m.filtered[m.cursor]
	profile := m.profiles[m.profileCursor]
	m.sessions[si].Profile = profile.Name
	m.sessions[si].CodexHome = profile.Home
	m.sessions[si].CodexSQLiteHome = profile.SQLiteHome
	m.chosen = si
}

func (m *picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fixScroll()
		m.fixProfileScroll()
		m.scrollPreview(0)
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
			m.previewCache = make(map[previewKey]*previewDocument)
		}
		m.previewCache[msg.key] = msg.document
		if msg.key == m.previewKey {
			m.previewText = msg.document.text
			m.previewDoc = msg.document
			m.previewLoading = false
			return m, m.fullRepaint()
		}
		return m, nil
	case tea.MouseMsg:
		return m, m.mouseWheel(msg)
	case tea.KeyMsg:
		if msg.String() == "pgup" || msg.String() == "pgdown" {
			delta := max(1, m.bodyHeight()-len(m.currentPreview().header)-1)
			if msg.String() == "pgup" {
				delta = -delta
			}
			if !m.confirming {
				m.scrollPreview(delta)
			}
			return m, nil
		}
		// Profile selection is a self-contained second stage. Esc returns to the
		// session list without closing the TUI; Enter applies the selected Codex
		// environment and completes the original selection.
		if m.choosingProfile {
			switch msg.String() {
			case "ctrl+c":
				m.chosen = -1
				return m, tea.Quit
			case "esc":
				m.choosingProfile = false
				return m, m.fullRepaint()
			case "enter":
				m.chooseProfile()
				return m, tea.Quit
			case "up", "ctrl+p", "ctrl+k":
				old := m.profileCursor
				m.moveProfile(-1)
				if m.profileCursor != old {
					return m, m.fullRepaint()
				}
			case "down", "ctrl+n", "ctrl+j":
				old := m.profileCursor
				m.moveProfile(1)
				if m.profileCursor != old {
					return m, m.fullRepaint()
				}
			}
			return m, nil
		}
		// While confirming a delete, keys answer y/N and nothing else.
		if m.confirming {
			switch msg.String() {
			case "y", "Y":
				m.doDelete()
				return m, m.fullRepaint()
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
				if m.beginProfileChoice() {
					return m, m.fullRepaint()
				}
				m.chosen = m.filtered[m.cursor]
			}
			return m, tea.Quit
		case "up", "ctrl+p", "ctrl+k":
			old := m.cursor
			m.move(-1)
			if m.cursor != old {
				return m, m.fullRepaint()
			}
			return m, nil
		case "down", "ctrl+n", "ctrl+j":
			old := m.cursor
			m.move(1)
			if m.cursor != old {
				return m, m.fullRepaint()
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
		return m, tea.Batch(cmd, m.fullRepaint())
	}
	return m, cmd
}

// topLine is the first row: the search input, or a delete confirm/status line.
func (m *picker) topLine() string {
	if m.choosingProfile {
		title := "codex profile ❯"
		hint := "↑/↓ choose · Enter resume · Esc back"
		gap := m.renderWidth() - lipgloss.Width(title) - lipgloss.Width(hint)
		if gap >= 2 {
			return title + strings.Repeat(" ", gap) + hintStyle.Render(hint)
		}
		return title
	}
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
		gap := m.renderWidth() - lipgloss.Width(in) - lipgloss.Width(hint)
		if gap >= 2 {
			return in + strings.Repeat(" ", gap) + hint
		}
	}
	return in
}

func (m *picker) profileRows(body, width int) []string {
	rows := make([]string, body)
	var selected session.Session
	if len(m.filtered) > 0 {
		selected = m.sessions[m.filtered[m.cursor]]
	}
	for i := 0; i < body; i++ {
		idx := m.profileOffset + i
		if idx >= len(m.profiles) {
			rows[i] = strings.Repeat(" ", width)
			continue
		}
		profile := m.profiles[idx]
		marker := ""
		if (selected.CodexHome != "" && selected.CodexHome == profile.Home) ||
			(selected.CodexHome == "" && selected.Profile != "" && selected.Profile == profile.Name) {
			marker = " (detected)"
		}
		label := fmt.Sprintf("%-12.12s%s %s", profile.Name, marker, session.Tilde(profile.Home))
		line := "  " + label
		if idx == m.profileCursor {
			line = "> " + label
		}
		line = padLine(line, width)
		if idx == m.profileCursor {
			line = cursorStyle.Render(line)
		}
		rows[i] = line
	}
	return rows
}

func padLine(line string, width int) string {
	line = truncate.String(line, uint(max(0, width)))
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func (m *picker) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	body := m.bodyHeight()

	leftW, rightW, showPreview := m.paneWidths()

	// --- list (left) ---
	var rows []string
	if m.choosingProfile {
		rows = m.profileRows(body, leftW)
	} else {
		rows = make([]string, body)
		for i := 0; i < body; i++ {
			idx := m.offset + i
			if idx >= len(m.filtered) {
				rows[i] = strings.Repeat(" ", leftW)
				continue
			}
			line := "  " + m.targets[m.filtered[idx]]
			if idx == m.cursor {
				line = "> " + m.targets[m.filtered[idx]]
			}
			line = padLine(line, leftW)
			if idx == m.cursor {
				line = cursorStyle.Render(line)
			}
			rows[i] = line
		}
	}

	if !showPreview {
		return m.topLine() + "\n" + strings.Join(rows, "\n")
	}

	// --- preview (right) ---
	doc := m.currentPreview()
	if m.previewLoading {
		doc = &previewDocument{lines: []string{hintStyle.Render("Loading preview…")}}
	}
	visible := max(0, body-len(doc.header))
	offset := min(m.previewOffset, max(0, len(doc.lines)-visible))
	divider := "│"
	for i := 0; i < body; i++ {
		line := ""
		if i < len(doc.header) {
			line = doc.header[i]
		} else if idx := i - len(doc.header) + offset; idx < len(doc.lines) {
			line = doc.lines[idx]
		}
		rows[i] += divider + padLine(line, rightW)
	}
	return m.topLine() + "\n" + strings.Join(rows, "\n")
}

// Pick shows the interactive fuzzy finder and returns the chosen session. The
// bool is false if the user aborted (Esc/Ctrl-C) or nothing matched. del, if
// non-nil, enables Ctrl-D to delete the highlighted session from disk. When
// multiple Codex profiles are supplied, Codex selection opens a second stage.
func Pick(sessions []session.Session, query string, profiles []session.CodexProfile, del func(session.Session) error) (session.Session, bool) {
	// Select the controlling terminal before creating any styles. In shell
	// widgets stdout is a captured pipe, while /dev/tty (or CONOUT$) is the real
	// interactive output and must drive Lipgloss capability detection.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	tuiOut := os.Stderr
	if in, out, closeConsole, ok := openConsole(); ok {
		defer closeConsole()
		tuiOut = out
		opts = append(opts, tea.WithInput(in), tea.WithOutput(out))
	} else {
		opts = append(opts, tea.WithOutput(tuiOut))
	}
	useRenderer(lipgloss.NewRenderer(tuiOut))

	ti := textinput.New()
	ti.Prompt = "ai-sessions ❯ "
	ti.SetValue(query)
	ti.Focus()
	ti.CursorEnd()

	m := &picker{
		sessions:     sessions,
		profiles:     profiles,
		input:        ti,
		chosen:       -1,
		del:          del,
		previewer:    preview.Preview,
		previewCache: make(map[previewKey]*previewDocument),
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
	// On Windows the picker often draws on a console handle bubbletea can't
	// deliver resize events for, so we poll the output size instead (no-op on
	// POSIX, which gets native resize events).
	if enablePolling && term.IsTerminal(tuiOut.Fd()) {
		m.pollFd, m.pollOn = tuiOut.Fd(), true
	}

	res, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return session.Session{}, false
	}
	fm := res.(*picker)
	if fm.chosen < 0 {
		return session.Session{}, false
	}
	return fm.sessions[fm.chosen], true
}
