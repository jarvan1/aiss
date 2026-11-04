package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jarvan1/aiss/internal/preview"
)

func TestScanClaudeSessionNames(t *testing.T) {
	for _, tt := range []struct {
		name, events, sidecar, want string
	}{
		{name: "unnamed"},
		{name: "default", events: `{"type":"ai-title","aiTitle":"默认会话标题","sessionId":"chat"}`, want: "默认会话标题"},
		{name: "latest default", events: "{\"type\":\"ai-title\",\"aiTitle\":\"First\"}\n{\"type\":\"ai-title\",\"aiTitle\":\"Latest\"}", want: "Latest"},
		{name: "rename after messages", events: `{"type":"custom-title","customTitle":"Renamed session","sessionId":"chat"}`, want: "Renamed session"},
		{name: "latest rename wins over later default", events: "{\"type\":\"custom-title\",\"customTitle\":\"Old\"}\n{\"type\":\"custom-title\",\"customTitle\":\"新名称\"}\n{\"type\":\"ai-title\",\"aiTitle\":\"Generated later\"}", want: "新名称"},
		{name: "sidecar", sidecar: `{"customTitle":"Sidecar name"}`, want: "Sidecar name"},
		{name: "sidecar takes precedence", events: `{"type":"custom-title","customTitle":"Old inline name"}`, sidecar: `{"customTitle":"Current rename"}`, want: "Current rename"},
		{name: "broken sidecar", events: `{"type":"ai-title","aiTitle":"Default"}`, sidecar: `not json`, want: "Default"},
		{name: "blank sidecar", events: `{"type":"custom-title","customTitle":"Inline rename"}`, sidecar: `{"customTitle":"  "}`, want: "Inline rename"},
		{name: "ignore malformed and foreign events", events: "broken\n{\"type\":\"ai-title\",\"aiTitle\":\"Own\",\"sessionId\":\"chat\"}\n{\"type\":\"custom-title\",\"customTitle\":\"Other\",\"sessionId\":\"other\"}\n{\"type\":\"ai-title\",\"aiTitle\":\" \"}", want: "Own"},
		{name: "spaced JSON", events: `{ "type" : "custom-title", "customTitle" : "Spaced name" }`, want: "Spaced name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project")
			if err := os.MkdirAll(project, 0o700); err != nil {
				t.Fatal(err)
			}
			cwd, _ := json.Marshal(root)
			// Titles are deliberately after enough metadata to stop the old
			// scanner. Message text mentioning a naming event is not metadata.
			data := `{"type":"user","cwd":` + string(cwd) + `,"message":{"content":"Original prompt"}}` + "\n" +
				`{"type":"assistant","message":{"content":"custom-title"},"customTitle":"Not a rename"}` + "\n" + tt.events + "\n"
			file := filepath.Join(project, "chat.jsonl")
			if err := os.WriteFile(file, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.sidecar != "" {
				if err := os.MkdirAll(filepath.Join(project, "chat"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, "chat", "custom-title.json"), []byte(tt.sidecar), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := scanClaude(root, false)
			if len(got) != 1 {
				t.Fatalf("sessions = %#v", got)
			}
			s := got[0]
			if s.Name != tt.want || s.ID != "chat" || s.Preview != "Original prompt" || s.File != file || s.Cwd != root {
				t.Fatalf("session = %#v, want name %q and original discovery metadata", s, tt.want)
			}
			view := ansi.Strip(preview.Preview(s, 80))
			if tt.want != "" && !strings.Contains(view, "Name: "+tt.want) {
				t.Fatalf("name missing from preview: %q", view)
			}
			if tt.want == "" && strings.Contains(view, "Name: ") {
				t.Fatalf("unexpected name in preview: %q", view)
			}
			if !strings.Contains(view, "Original prompt") {
				t.Fatalf("transcript missing: %q", view)
			}
		})
	}
}
