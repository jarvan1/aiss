package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarvan1/aiss/internal/session"
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
