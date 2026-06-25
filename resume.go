package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// resumePlan describes how to re-enter a session.
type resumePlan struct {
	dir  string   // directory to run in (created if missing)
	name string   // executable
	args []string // arguments
	note string   // human-facing note (e.g. gemini has no resume-by-id)
}

// planResume builds the command that re-enters a session in its original dir.
func planResume(s Session) (resumePlan, error) {
	dir := s.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	switch s.Provider {
	case "claude":
		return resumePlan{dir, "claude", []string{"--resume", s.ID}, ""}, nil
	case "codex":
		args := []string{"resume", s.ID}
		if model := codexModel(s.File); model != "" {
			args = append(args, "-m", model)
		}
		return resumePlan{dir, "codex", args, ""}, nil
	case "copilot":
		return resumePlan{dir, "copilot", []string{"--resume=" + s.ID}, ""}, nil
	case "gemini":
		return resumePlan{dir, "gemini", nil, "gemini-cli has no resume-by-id; opening in the directory"}, nil
	}
	return resumePlan{}, fmt.Errorf("unknown provider %q", s.Provider)
}

// codexModel reads the model the codex session was recorded with, so resume
// doesn't silently switch defaults.
func codexModel(file string) string {
	var model string
	eachLine(file, func(raw []byte) bool {
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
func (p resumePlan) Run() error {
	if _, err := exec.LookPath(p.name); err != nil {
		return fmt.Errorf("%s not found on PATH (install it to resume)", p.name)
	}
	if !dirExists(p.dir) {
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
func (p resumePlan) Shell() string {
	cmd := fmt.Sprintf("cd %s && %s", shellQuote(p.dir), p.name)
	for _, a := range p.args {
		cmd += " " + shellQuote(a)
	}
	return "( " + cmd + " )"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
