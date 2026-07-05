// Package resume builds and runs the command that re-enters a session in its
// original working directory, and renders it for the shell key widgets.
package resume

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/jarvan1/aiss/internal/session"
)

// Plan describes how to re-enter a session.
type Plan struct {
	dir  string   // directory to run in (created if missing)
	name string   // executable
	args []string // arguments
	note string   // human-facing note (e.g. gemini has no resume-by-id)
}

// Note returns the human-facing note, if any (e.g. gemini has no resume-by-id).
func (p Plan) Note() string { return p.note }

// PlanResume builds the command that re-enters a session in its original dir.
func PlanResume(s session.Session) (Plan, error) {
	dir := s.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	switch s.Provider {
	case "claude":
		return Plan{dir, "claude", []string{"--resume", s.ID}, ""}, nil
	case "codex":
		args := []string{"resume", s.ID}
		if model := codexModel(s.File); model != "" {
			args = append(args, "-m", model)
		}
		return Plan{dir, "codex", args, ""}, nil
	case "copilot":
		return Plan{dir, "copilot", []string{"--resume=" + s.ID}, ""}, nil
	case "gemini":
		return Plan{dir, "gemini", nil, "gemini-cli has no resume-by-id; opening in the directory"}, nil
	}
	return Plan{}, fmt.Errorf("unknown provider %q", s.Provider)
}

// codexModel reads the model the codex session was recorded with, so resume
// doesn't silently switch defaults.
func codexModel(file string) string {
	var model string
	session.EachLine(file, func(raw []byte) bool {
		var l struct {
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(raw, &l) == nil && l.Payload.Model != "" {
			model = l.Payload.Model
			return false
		}
		return true
	})
	return model
}

// Run executes the plan, inheriting the terminal. The session's original dir is
// created if it was deleted, so resume-by-cwd lookups still match.
func (p Plan) Run() error {
	if _, err := exec.LookPath(p.name); err != nil {
		return fmt.Errorf("%s not found on PATH (install it to resume)", p.name)
	}
	if !session.DirExists(p.dir) {
		if err := os.MkdirAll(p.dir, 0o755); err != nil {
			return err
		}
	}
	cmd := exec.Command(p.name, p.args...)
	cmd.Dir = p.dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Shell renders the plan as a copy-pasteable shell command (for --print).
func (p Plan) Shell() string {
	cmd := fmt.Sprintf("cd %s && %s", shellQuote(p.dir), p.name)
	for _, a := range p.args {
		cmd += " " + shellQuote(a)
	}
	return "( " + cmd + " )"
}

// PowerShell renders the plan for PowerShell (--print --pwsh). PowerShell has no
// `( cd … && … )` subshell, so we Push-Location, run, then Pop-Location in a
// finally block so the caller's directory is always restored.
func (p Plan) PowerShell() string {
	var b strings.Builder
	b.WriteString("Push-Location " + pwshQuote(p.dir) + "; try { & " + pwshQuote(p.name))
	for _, a := range p.args {
		b.WriteString(" " + pwshQuote(a))
	}
	b.WriteString(" } finally { Pop-Location }")
	return b.String()
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pwshQuote single-quotes a string for PowerShell, where an embedded single
// quote is escaped by doubling it.
func pwshQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
