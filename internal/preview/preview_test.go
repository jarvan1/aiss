package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
	"github.com/klauspost/compress/zstd"
)

func TestCompressedCodexPreviewMatchesPlainTranscript(t *testing.T) {
	data := []byte(`{"type":"session_meta","timestamp":"2026-10-10T01:00:00Z","payload":{"cwd":"/tmp/project","cli_version":"0.163.0","model_provider":"openai","history_mode":"paginated"}}
{"type":"turn_context","payload":{"model":"fixture-model"}}
{"type":"response_item","timestamp":"2026-10-10T01:00:01Z","payload":{"type":"message","role":"user","content":[{"text":"检查中文历史"}]}}
{"type":"response_item","timestamp":"2026-10-10T01:00:02Z","payload":{"type":"message","role":"assistant","content":[{"text":"历史仍然完整"}]}}
`)
	path := filepath.Join(t.TempDir(), "rollout-preview.jsonl")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	if err := os.WriteFile(path+".zst", encoder.EncodeAll(data, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Preview(session.Session{Provider: "codex", File: path, Profile: "work"}, 80)
	for _, text := range []string{"检查中文历史", "历史仍然完整", "fixture-model", "codex:work", "1 user", "1 assistant"} {
		if !strings.Contains(want, text) {
			t.Fatalf("preview missing %q: %q", text, want)
		}
	}
	if got := Preview(session.Session{Provider: "codex", File: path + ".zst", Profile: "work"}, 80); got != want {
		t.Fatal("compressed and plain previews differ")
	}
	// A picker can retain the plain path while Codex compresses it in the
	// background; that same selection must still produce its complete preview.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := Preview(session.Session{Provider: "codex", File: path, Profile: "work"}, 80); got != want {
		t.Fatal("preview stopped working after background compression")
	}
}

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
