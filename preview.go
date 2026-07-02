package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/reflow/truncate"
)

// ANSI helpers.
const (
	cReset = "\x1b[0m"
	cBold  = "\x1b[1m"
	cDim   = "\x1b[2m"
	cCyan  = "\x1b[36m"
	cUser  = "\x1b[1;36m"
	cAsst  = "\x1b[1;32m"
)

// meta is the header info shown above the transcript.
type meta struct {
	provider string
	model    string
	verLine  string
	branch   string
	cwd      string
	t0, t1   time.Time
	nUser    int
	nAsst    int
}

// turn is one rendered conversation message.
type turn struct {
	role string // "user" | "assistant"
	text string
}

// Preview returns the full preview text (header + transcript) for a session.
// width is the column budget for the header box rules; <=0 uses a default.
func Preview(s Session, width int) string {
	m, turns := readTranscript(s)
	var b strings.Builder
	b.WriteString(renderHeader(m, width))
	for _, t := range turns {
		label := cUser + "▶ USER" + cReset
		if t.role == "assistant" {
			label = cAsst + "◀ ASSISTANT" + cReset
		}
		b.WriteString(label + "\n" + t.text + "\n\n")
	}
	return b.String()
}

func renderHeader(m meta, width int) string {
	cwd := tilde(m.cwd)
	d0, d1, dur := timeRange(m.t0, m.t1)

	// Total box width, capped so a tiny pane doesn't produce negative padding.
	w := width
	if w < 10 {
		w = 44
	}
	inner := w - 2 // columns between the left "│" and right "│"

	// boxRow draws "│ <styled> <pad>│", padding by the *display* width of the
	// plain text so ANSI codes and wide runes (emoji, ·) don't skew alignment.
	// Content wider than the row is truncated so the right border always shows.
	boxRow := func(styled, plain string) string {
		avail := inner - 1 // 1 leading space
		if runewidth.StringWidth(plain) > avail {
			styled = truncate.String(styled, uint(avail))
			plain = truncate.String(plain, uint(avail))
		}
		pad := avail - runewidth.StringWidth(plain)
		if pad < 0 {
			pad = 0
		}
		return fmt.Sprintf("%s│%s %s%s%s│%s\n", cCyan, cReset, styled, strings.Repeat(" ", pad), cCyan, cReset)
	}

	// Title lives on the top border: ╭─ provider · model ──…──╮
	titlePlain := fmt.Sprintf("─ %s · %s ", m.provider, m.model)
	titleStyled := fmt.Sprintf("─ %s%s%s%s · %s ", cBold, m.provider, cReset, cCyan, m.model)
	if runewidth.StringWidth(titlePlain) > inner {
		titleStyled = truncate.String(titleStyled, uint(inner))
		titlePlain = truncate.String(titlePlain, uint(inner))
	}
	topFill := inner - runewidth.StringWidth(titlePlain)
	if topFill < 0 {
		topFill = 0
	}

	// 📁 cwd (+ branch)
	cwdPlain := "📁 " + cwd
	cwdStyled := "📁 " + cwd
	if m.branch != "" && m.branch != "HEAD" {
		cwdPlain += "  ⎇ " + m.branch
		cwdStyled += fmt.Sprintf("  %s⎇ %s%s", cDim, m.branch, cReset)
	}

	// 🕐 time range
	timePlain := fmt.Sprintf("🕐 %s → %s  (%s)", d0, d1, dur)
	timeStyled := fmt.Sprintf("🕐 %s → %s  %s(%s)%s", d0, d1, cDim, dur, cReset)

	// 💬 counts (+ version)
	msgPlain := fmt.Sprintf("💬 %d user · %d assistant", m.nUser, m.nAsst)
	msgStyled := msgPlain
	if m.verLine != "" {
		msgPlain += "   " + m.verLine
		msgStyled += fmt.Sprintf("   %s%s%s", cDim, m.verLine, cReset)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s╭%s%s%s╮%s\n", cCyan, titleStyled, cCyan, strings.Repeat("─", topFill), cReset)
	b.WriteString(boxRow(cwdStyled, cwdPlain))
	b.WriteString(boxRow(timeStyled, timePlain))
	b.WriteString(boxRow(msgStyled, msgPlain))
	fmt.Fprintf(&b, "%s╰%s╯%s\n\n", cCyan, strings.Repeat("─", inner), cReset)
	return b.String()
}

func timeRange(t0, t1 time.Time) (string, string, string) {
	if t0.IsZero() {
		return "?", "?", "?"
	}
	d0 := t0.Format("2006-01-02 15:04")
	if t1.IsZero() {
		return d0, "?", "?"
	}
	var d1 string
	if t0.Format("2006-01-02") == t1.Format("2006-01-02") {
		d1 = t1.Format("15:04")
	} else {
		d1 = t1.Format("01-02 15:04")
	}
	dur := "?"
	if s := int(t1.Sub(t0).Seconds()); s >= 0 {
		switch {
		case s >= 86400:
			dur = fmt.Sprintf("%dd%dh", s/86400, (s%86400)/3600)
		case s >= 3600:
			dur = fmt.Sprintf("%dh%dm", s/3600, (s%3600)/60)
		case s >= 60:
			dur = fmt.Sprintf("%dm", s/60)
		default:
			dur = fmt.Sprintf("%ds", s)
		}
	}
	return d0, d1, dur
}

// readTranscript parses a session file into header meta + ordered turns.
func readTranscript(s Session) (meta, []turn) {
	switch s.Provider {
	case "claude":
		return readClaude(s.File)
	case "codex":
		return readCodex(s.File)
	case "copilot":
		return readCopilot(s.File)
	case "gemini":
		return readGemini(s.File)
	}
	return meta{provider: s.Provider, cwd: s.Cwd}, nil
}

func readClaude(file string) (meta, []turn) {
	m := meta{provider: "claude"}
	var turns []turn
	type line struct {
		Type      string `json:"type"`
		Cwd       string `json:"cwd"`
		Version   string `json:"version"`
		GitBranch string `json:"gitBranch"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Model   string          `json:"model"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	eachLine(file, func(raw []byte) bool {
		var l line
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		if ts := parseTime(l.Timestamp); !ts.IsZero() {
			if m.t0.IsZero() {
				m.t0 = ts
			}
			m.t1 = ts
		}
		if m.cwd == "" {
			m.cwd = l.Cwd
		}
		if m.verLine == "" && l.Version != "" {
			m.verLine = "Claude Code " + l.Version
		}
		if m.branch == "" && l.GitBranch != "" {
			m.branch = l.GitBranch
		}
		if m.model == "" && l.Type == "assistant" && l.Message.Model != "" && !reAngle.MatchString(l.Message.Model) {
			m.model = l.Message.Model
		}
		switch l.Type {
		case "user":
			m.nUser++
			if t := claudeBlocks(l.Message.Content); t != "" {
				turns = append(turns, turn{"user", t})
			}
		case "assistant":
			m.nAsst++
			if t := claudeBlocks(l.Message.Content); t != "" {
				turns = append(turns, turn{"assistant", t})
			}
		}
		return true
	})
	if m.model == "" {
		m.model = "?"
	}
	return m, turns
}

// claudeBlocks renders content (string or block array) into preview text,
// summarizing tool_use / tool_result blocks.
func claudeBlocks(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		switch {
		case b.Text != "":
			parts = append(parts, b.Text)
		case b.Type == "tool_use":
			name := b.Name
			if name == "" {
				name = "tool"
			}
			parts = append(parts, "🔧 "+name)
		case b.Type == "tool_result":
			parts = append(parts, "↩  [tool result]")
		}
	}
	return strings.Join(parts, "\n")
}

func readCodex(file string) (meta, []turn) {
	m := meta{provider: "codex"}
	var turns []turn
	type line struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type          string `json:"type"`
			Role          string `json:"role"`
			Model         string `json:"model"`
			ModelProvider string `json:"model_provider"`
			CliVersion    string `json:"cli_version"`
			Cwd           string `json:"cwd"`
			Content       []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"payload"`
	}
	eachLine(file, func(raw []byte) bool {
		var l line
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		if ts := parseTime(l.Timestamp); !ts.IsZero() {
			if m.t0.IsZero() {
				m.t0 = ts
			}
			m.t1 = ts
		}
		if l.Type == "session_meta" {
			if m.cwd == "" {
				m.cwd = l.Payload.Cwd
			}
			if l.Payload.CliVersion != "" {
				m.verLine = "codex " + l.Payload.CliVersion
				if l.Payload.ModelProvider != "" {
					m.verLine += " · " + l.Payload.ModelProvider
				}
			}
		}
		if m.model == "" && l.Payload.Model != "" {
			m.model = l.Payload.Model
		}
		if l.Payload.Type == "message" {
			var sb []string
			for _, c := range l.Payload.Content {
				if c.Text != "" {
					sb = append(sb, c.Text)
				}
			}
			text := strings.Join(sb, "\n")
			switch l.Payload.Role {
			case "user":
				m.nUser++
				if text != "" && !reCodexInjected.MatchString(text) {
					turns = append(turns, turn{"user", text})
				}
			case "assistant":
				m.nAsst++
				if text != "" {
					turns = append(turns, turn{"assistant", text})
				}
			}
		}
		return true
	})
	if m.model == "" {
		m.model = "?"
	}
	return m, turns
}

func readCopilot(file string) (meta, []turn) {
	m := meta{provider: "copilot"}
	var turns []turn
	type line struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Data      struct {
			Content        string `json:"content"`
			Model          string `json:"model"`
			NewModel       string `json:"newModel"`
			CopilotVersion string `json:"copilotVersion"`
			Context        struct {
				Cwd string `json:"cwd"`
			} `json:"context"`
			ToolRequests []struct {
				Name string `json:"name"`
			} `json:"toolRequests"`
		} `json:"data"`
	}
	eachLine(file, func(raw []byte) bool {
		var l line
		if json.Unmarshal(raw, &l) != nil {
			return true
		}
		if ts := parseTime(l.Timestamp); !ts.IsZero() {
			if m.t0.IsZero() {
				m.t0 = ts
			}
			m.t1 = ts
		}
		switch l.Type {
		case "session.start":
			if m.cwd == "" {
				m.cwd = l.Data.Context.Cwd
			}
			if l.Data.CopilotVersion != "" {
				m.verLine = "Copilot CLI " + l.Data.CopilotVersion
			}
		case "session.model_change":
			if l.Data.NewModel != "" {
				m.model = l.Data.NewModel
			}
		case "user.message":
			m.nUser++
			if l.Data.Content != "" {
				turns = append(turns, turn{"user", l.Data.Content})
			}
		case "assistant.message":
			m.nAsst++
			if l.Data.Model != "" {
				m.model = l.Data.Model
			}
			text := l.Data.Content
			for _, tr := range l.Data.ToolRequests {
				name := tr.Name
				if name == "" {
					name = "tool"
				}
				text += "\n🔧 " + name
			}
			if strings.TrimSpace(text) != "" {
				turns = append(turns, turn{"assistant", text})
			}
		}
		return true
	})
	if m.model == "" {
		m.model = "?"
	}
	return m, turns
}

func readGemini(file string) (meta, []turn) {
	m := meta{provider: "gemini", model: "?"}
	data, err := os.ReadFile(file)
	if err != nil {
		return m, nil
	}
	var entries []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(data, &entries)
	var turns []turn
	for _, e := range entries {
		role := "assistant"
		if e.Type == "user" {
			role = "user"
			m.nUser++
		} else {
			m.nAsst++
		}
		if e.Message != "" {
			turns = append(turns, turn{role, e.Message})
		}
	}
	return m, turns
}
