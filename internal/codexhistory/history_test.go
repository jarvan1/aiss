package codexhistory_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jarvan1/aiss/internal/codexhistory"
	"github.com/jarvan1/aiss/internal/preview"
	"github.com/jarvan1/aiss/internal/resume"
	"github.com/jarvan1/aiss/internal/scan"
	"github.com/jarvan1/aiss/internal/session"
)

func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func createState(t *testing.T, home, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(home, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	execSQL(t, db, `CREATE TABLE threads (
		id TEXT PRIMARY KEY, rollout_path TEXT, cwd TEXT, preview TEXT,
		first_user_message TEXT, title TEXT, model TEXT, creator_account_id TEXT,
		cli_version TEXT, model_provider TEXT, created_at_ms INTEGER,
		created_at INTEGER, updated_at_ms INTEGER, updated_at INTEGER,
		archived INTEGER, history_mode TEXT, thread_source TEXT DEFAULT '', source TEXT DEFAULT '')`)
	return db
}

func insertThread(t *testing.T, db *sql.DB, id, path, cwd string, archived int) {
	t.Helper()
	execSQL(t, db, `INSERT INTO threads (id, rollout_path, cwd, preview, first_user_message,
		title, model, creator_account_id, cli_version, model_provider, created_at_ms,
		created_at, updated_at_ms, updated_at, archived, history_mode)
		VALUES (?, ?, ?, '', '查询旧记录', '',
		'db-model', 'acct-work', '0.163.0', 'openai', 1000, 1, 5000, 5, ?, 'paginated')`,
		id, path, cwd, archived)
}

func TestPaginatedDiscoveryPreviewAndResumeWithoutRollout(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := createState(t, home, "state_5.sqlite")
	rollout := filepath.Join(sessionsDir, "rollout-old.jsonl")
	insertThread(t, db, "db-only", rollout, home, 0)
	insertThread(t, db, "archived", rollout, home, 1)
	insertThread(t, db, "missing-cwd", rollout, filepath.Join(home, "gone"), 0)
	historyPath := filepath.Join(home, "thread_history_1.sqlite")
	history, err := sql.Open("sqlite", historyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	execSQL(t, history, `CREATE TABLE thread_items (thread_id TEXT, item_json TEXT, item_type TEXT, rollout_ordinal INTEGER)`)
	// Insert out of order to ensure the projection, not row insertion order,
	// determines the transcript. Tool items should not become chat messages.
	execSQL(t, history, `INSERT INTO thread_items VALUES ('db-only',
		'{"type":"agentMessage","text":"分页中最新的回答"}', 'agentMessage', 20)`)
	execSQL(t, history, `INSERT INTO thread_items VALUES ('db-only',
		'{"type":"userMessage","content":[{"type":"text","text":"查询旧记录"}]}', 'userMessage', 10)`)
	execSQL(t, history, `INSERT INTO thread_items VALUES ('db-only', '{}', 'commandExecution', 15)`)

	dirs := session.Dirs{Codex: sessionsDir, CodexProfiles: []session.CodexProfile{
		{Name: "work", Home: filepath.Join(home, ".codex-work"), SQLiteHome: home, AccountID: "acct-work"},
	}}
	got := scan.Scan(dirs, false)
	if len(got) != 1 {
		t.Fatalf("sessions = %#v, want only visible db-only thread", got)
	}
	s := got[0]
	if s.ID != "db-only" || s.Profile != "work" || s.Preview != "查询旧记录" ||
		s.Model != "db-model" || s.CodexHistoryDB != historyPath {
		t.Fatalf("SQLite metadata = %#v", s)
	}
	view := preview.Preview(s, 80)
	for _, want := range []string{"codex:work", "db-model", "查询旧记录", "分页中最新的回答", "1 user", "1 assistant"} {
		if !strings.Contains(view, want) {
			t.Fatalf("preview missing %q: %q", want, view)
		}
	}
	if strings.Index(view, "查询旧记录") > strings.Index(view, "分页中最新的回答") {
		t.Fatal("preview messages are out of order")
	}
	p, err := resume.PlanResume(s)
	if err != nil {
		t.Fatal(err)
	}
	if cmd := p.Shell(); !strings.Contains(cmd, "'--no-daemon' 'resume' 'db-only' '-m' 'db-model'") {
		t.Fatalf("DB-only resume = %s", cmd)
	}
	if all := scan.Scan(dirs, true); len(all) != 2 {
		t.Fatalf("keepMissing = %d sessions, want 2", len(all))
	}
}

func TestPaginatedMetadataMergesWithRollout(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(sessionsDir, "rollout-existing.jsonl")
	data := `{"type":"session_meta","payload":{"id":"existing","cwd":"` + filepath.ToSlash(home) + `"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"old prompt"}]}}
`
	if err := os.WriteFile(rollout, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	db := createState(t, home, "state_5.sqlite")
	insertThread(t, db, "existing", rollout, home, 0)
	got := scan.Scan(session.Dirs{Codex: sessionsDir}, false)
	resolved, err := filepath.EvalSymlinks(rollout)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].File != resolved || got[0].Model != "db-model" || got[0].ModTime.UnixMilli() != 5000 {
		t.Fatalf("merged metadata = %#v", got)
	}
}

func TestNewestDatabaseVersionAndReadOnlyOpen(t *testing.T) {
	home := t.TempDir()
	older := createState(t, home, "state_9.sqlite")
	insertThread(t, older, "older", "rollout.jsonl", home, 0)
	latest := createState(t, home, "state_10.sqlite")
	insertThread(t, latest, "latest", "rollout.jsonl", home, 0)
	threads, err := codexhistory.List(home)
	if err != nil || len(threads) != 1 || threads[0].ID != "latest" {
		t.Fatalf("versioned DB selection = %#v, %v", threads, err)
	}
	empty := t.TempDir()
	if _, err := codexhistory.List(empty); err == nil {
		t.Fatal("missing database should return an error")
	}
	files, err := os.ReadDir(empty)
	if err != nil || len(files) != 0 {
		t.Fatalf("read-only discovery created files: %v, %v", files, err)
	}
}

func TestUnsupportedDatabaseFallsBackToRolloutPreview(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionsDir, "rollout-legacy.jsonl")
	data := `{"type":"session_meta","payload":{"id":"legacy","cwd":"` + filepath.ToSlash(home) + `"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"legacy transcript"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	execSQL(t, db, "CREATE TABLE threads (id TEXT PRIMARY KEY)")
	sessions := scan.Scan(session.Dirs{Codex: sessionsDir}, false)
	if len(sessions) != 1 || sessions[0].ID != "legacy" {
		t.Fatalf("unsupported database hid legacy rollout: %#v", sessions)
	}
	// A database that disappears or has an unsupported schema must also leave
	// the existing file-based preview available to a cached selection.
	s := sessions[0]
	s.CodexHistoryDB = filepath.Join(home, "missing-history.sqlite")
	if view := preview.Preview(s, 80); !strings.Contains(view, "legacy transcript") {
		t.Fatalf("legacy preview fallback missing: %q", view)
	}
}

func TestGuardianFilteringMergesBothSourcesWithoutHidingParent(t *testing.T) {
	t.Setenv("AISS_SHOW_INTERNAL", "")
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := createState(t, home, "state_5.sqlite")
	for _, id := range []string{"parent", "review-db-only", "review-db-source", "review-file", "ordinary-subagent"} {
		path := filepath.Join(sessionsDir, "rollout-"+id+".jsonl")
		insertThread(t, db, id, path, home, 0)
		if id == "parent" || id == "review-file" || id == "review-db-source" {
			kind := "user"
			if id == "review-file" {
				kind = "guardian_review"
			}
			data := fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q,"thread_source":%q}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"text":"user prompt"}]}}
`, id, home, kind)
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	execSQL(t, db, `UPDATE threads SET thread_source='guardian_review' WHERE id='review-db-only'`)
	execSQL(t, db, `UPDATE threads SET source='{"subagent":{"other":"guardian"}}' WHERE id='review-db-source'`)
	execSQL(t, db, `UPDATE threads SET source='{"subagent":{"thread_spawn":{"parent_thread_id":"parent"}}}' WHERE id='ordinary-subagent'`)
	// A user title alone must never be taken as an internal-thread marker.
	execSQL(t, db, `UPDATE threads SET preview='Guardian review', title='Guardian review' WHERE id='parent'`)
	dirs := session.Dirs{Codex: sessionsDir}
	got := scan.Scan(dirs, false)
	if len(got) != 2 {
		t.Fatalf("filtered mixed-source sessions = %#v, want parent + ordinary agent", got)
	}
	for _, item := range got {
		if item.ID != "parent" && item.ID != "ordinary-subagent" {
			t.Fatalf("review was reintroduced by another source: %#v", item)
		}
	}
	t.Setenv("AISS_SHOW_INTERNAL", "1")
	if got := scan.Scan(dirs, false); len(got) != 5 {
		t.Fatalf("diagnostic list = %#v, want all 5 original records", got)
	}
}
