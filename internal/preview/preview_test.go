package preview

import (
	"strings"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
)

func TestSanitizeTranscriptText(t *testing.T) {
	input := "before\tcolumn\rrewrite\x1b[2Jafter\a\x00"
	want := "before    column\nrewriteafter"
	if got := sanitizeTranscriptText(input); got != want {
		t.Fatalf("sanitizeTranscriptText() = %q, want %q", got, want)
	}
}

func TestSanitizeInline(t *testing.T) {
	input := "codex\tauto\rreview"
	want := "codex    auto review"
	if got := sanitizeInline(input); got != want {
		t.Fatalf("sanitizeInline() = %q, want %q", got, want)
	}
}

func TestPreviewHeaderIncludesProfile(t *testing.T) {
	got := Preview(session.Session{Provider: "codex", Profile: "work"}, 60)
	if !strings.Contains(got, "codex:work") {
		t.Fatalf("profile missing from preview header: %q", got)
	}
}
