package main

import (
	"fmt"

	"github.com/ktr0731/go-fuzzyfinder"
)

// rowLabel is the single-line list entry: provider, cwd, preview.
func rowLabel(s Session) string {
	preview := s.Preview
	if preview == "" {
		preview = "(no prompt)"
	}
	return fmt.Sprintf("%-7s %-34.34s %s", s.Provider, tilde(s.Cwd), preview)
}

// Pick shows the interactive fuzzy finder with a live preview pane and returns
// the chosen session. The bool is false if the user aborted (Esc/Ctrl-C).
func Pick(sessions []Session, query string) (Session, bool) {
	idx, err := fuzzyfinder.Find(
		sessions,
		func(i int) string { return rowLabel(sessions[i]) },
		fuzzyfinder.WithQuery(query),
		fuzzyfinder.WithPreviewWindow(func(i, _, _ int) string {
			if i < 0 {
				return ""
			}
			return Preview(sessions[i])
		}),
	)
	if err != nil {
		return Session{}, false
	}
	return sessions[idx], true
}
