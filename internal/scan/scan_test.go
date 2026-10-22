package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
	"github.com/klauspost/compress/zstd"
)

func TestScanCodexHomesAssociatesSharedRolloutsWithProfiles(t *testing.T) {
	root := t.TempDir()
	sqliteHome := filepath.Join(root, ".codex")
	sharedSessions := filepath.Join(sqliteHome, "sessions")
	workHome := filepath.Join(root, ".codex-work")
	personalHome := filepath.Join(root, ".codex-personal")
	cwd := filepath.Join(root, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	writeCodexRollout(t, sharedSessions, "work", cwd, "acct-work")
	writeCodexRollout(t, sharedSessions, "personal", cwd, "acct-personal")
	writeCodexRollout(t, sharedSessions, "unknown", cwd, "")
	writeCodexRollout(t, sharedSessions, "legacy", cwd, "")
	writeCodexRollout(t, filepath.Join(personalHome, "sessions"), "local", cwd, "")

	dirs := session.Dirs{
		Codex: sharedSessions,
		CodexProfiles: []session.CodexProfile{
			{Name: "work", Home: workHome, SQLiteHome: sqliteHome, AccountID: "acct-work"},
			{Name: "personal", Home: personalHome, SQLiteHome: sqliteHome, AccountID: "acct-personal"},
		},
		CodexProfileSelections: map[string]session.CodexProfileSelection{
			// Embedded creator_account_id remains authoritative over a stale
			// remembered choice.
			"work":   {Name: "personal", Home: personalHome},
			"legacy": {Name: "work", Home: workHome},
		},
	}
	got := scanCodexHomes(dirs, false)
	if len(got) != 5 {
		t.Fatalf("sessions = %d, want 5: %#v", len(got), got)
	}

	byID := make(map[string]session.Session)
	for _, item := range got {
		byID[item.ID] = item
	}
	for id, profile := range map[string]string{"work": "work", "personal": "personal", "local": "personal", "legacy": "work"} {
		item := byID[id]
		if item.Profile != profile || item.CodexHome == "" || item.CodexSQLiteHome != sqliteHome {
			t.Errorf("%s session = %#v", id, item)
		}
	}
	if item := byID["unknown"]; item.Profile != "" || item.CodexHome != "" || item.CodexSQLiteHome != sqliteHome {
		t.Errorf("unknown session = %#v", item)
	}
}

func TestScanCodexCompressedRollouts(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "runtime", "sessions")
	writeCodexRollout(t, sessions, "compressed", root, "acct-work")
	writeCodexRollout(t, sessions, "both", root, "")
	dir := filepath.Join(sessions, "2026", "10", "10")
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	for _, id := range []string{"compressed", "both"} {
		path := filepath.Join(dir, "rollout-test-"+id+".jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".zst", encoder.EncodeAll(data, nil), 0o600); err != nil {
			t.Fatal(err)
		}
		if id == "compressed" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Compression leftovers must not appear as extra sessions.
	for _, suffix := range []string{".zst.tmp", ".backup", ".zst"} {
		if err := os.WriteFile(filepath.Join(dir, "rollout-corrupt.jsonl"+suffix), []byte("broken"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := scanCodexHomes(session.Dirs{
		Codex:         sessions,
		CodexProfiles: []session.CodexProfile{{Name: "work", Home: filepath.Join(root, ".codex-work"), SQLiteHome: filepath.Dir(sessions), AccountID: "acct-work"}},
	}, false)
	if len(got) != 2 {
		t.Fatalf("sessions = %#v, want compressed + one plain copy", got)
	}
	for _, item := range got {
		if item.Preview != "hello" || item.Cwd != root {
			t.Fatalf("metadata not decoded: %#v", item)
		}
		if item.ID == "compressed" && (item.Profile != "work" || filepath.Ext(item.File) != ".zst") {
			t.Fatalf("compressed profile/path = %#v", item)
		}
		if item.ID == "both" && filepath.Ext(item.File) != ".jsonl" {
			t.Fatalf("plain rollout should take precedence: %#v", item)
		}
	}
}

func TestScanCodexCompressedFilenameUUIDFallback(t *testing.T) {
	root := t.TempDir()
	id := "01a1244c-57e6-7660-9f1f-b18f18798936"
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	path := filepath.Join(root, "rollout-2026-10-10T13-32-26-"+id+".jsonl.zst")
	if err := os.WriteFile(path, encoder.EncodeAll([]byte("{}\n"), nil), 0o600); err != nil {
		t.Fatal(err)
	}
	got := scanCodexHomes(session.Dirs{Codex: root}, true)
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("filename UUID fallback = %#v, want full UUID %s", got, id)
	}
}

func TestScanCodexHidesGuardianReviewsFromPlainAndCompressedRollouts(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("compressed=%v", compressed), func(t *testing.T) {
			t.Setenv("AISS_SHOW_INTERNAL", "")
			root := t.TempDir()
			encoder, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer encoder.Close()
			cases := []struct{ id, kind, source string }{
				{"parent", "user", `"vscode"`},
				{"review-by-kind", "guardian_review", `"vscode"`},
				{"review-by-source", "", `{"subagent": {"other": "guardian"}}`},
				{"normal-subagent", "", `{"subagent":{"thread_spawn":{"parent_thread_id":"parent"}}}`},
				{"user-with-same-title", "user", `"cli"`},
			}
			for _, item := range cases {
				path := filepath.Join(root, "rollout-test-"+item.id+".jsonl")
				data := []byte(fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"session_id":"parent","cwd":%q,"thread_source":%q,"source":%s}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"Guardian review"}]}}
`, item.id, root, item.kind, item.source))
				if compressed {
					path += ".zst"
					data = encoder.EncodeAll(data, nil)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			dirs := session.Dirs{Codex: root}
			got := scanCodexHomes(dirs, false)
			if len(got) != 3 {
				t.Fatalf("visible sessions = %#v, want parent + ordinary agent + user chat", got)
			}
			for _, item := range got {
				if item.ID != "parent" && item.ID != "normal-subagent" && item.ID != "user-with-same-title" {
					t.Fatalf("internal review leaked into list: %#v", item)
				}
			}
			t.Setenv("AISS_SHOW_INTERNAL", "1")
			if got := scanCodexHomes(dirs, false); len(got) != 5 {
				t.Fatalf("diagnostic list = %d entries, want 5", len(got))
			}
		})
	}
}

func writeCodexRollout(t *testing.T, root, id, cwd, account string) {
	t.Helper()
	dir := filepath.Join(root, "2026", "10", "10")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "rollout-test-"+id+".jsonl")
	meta := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q,"creator_account_id":%q}}`, id, cwd, account)
	message := `{"type":"event_msg","payload":{"type":"message","role":"user","content":[{"text":"hello"}]}}`
	if err := os.WriteFile(file, []byte(meta+"\n"+message+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
