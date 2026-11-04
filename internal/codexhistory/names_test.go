package codexhistory_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jarvan1/aiss/internal/codexhistory"
)

func TestNamesDefaultRenameAndLegacySchemas(t *testing.T) {
	for _, withName := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "named"}[withName], func(t *testing.T) {
			home := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			execSQL(t, db, `CREATE TABLE threads (id TEXT, title TEXT)`)
			execSQL(t, db, `INSERT INTO threads VALUES ('default', '默认会话'), ('renamed', 'Original title'), ('explicit', 'Generated title')`)
			if withName {
				execSQL(t, db, `ALTER TABLE threads ADD COLUMN name TEXT`)
				execSQL(t, db, `UPDATE threads SET name='Explicit /rename' WHERE id='explicit'`)
			}
			index := `{"id":"renamed","thread_name":"Latest rename","updated_at":"2026-11-04T10:00:00Z"}
{"id":"renamed","thread_name":"Stale rename","updated_at":"2026-11-03T10:00:00Z"}
broken record
{"id":"renamed","thread_name":"   ","updated_at":"2026-11-05T10:00:00Z"}
{"id":"explicit","thread_name":"Index name"}
`
			if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(index), 0o600); err != nil {
				t.Fatal(err)
			}
			names := codexhistory.Names(home)
			wantExplicit := "Index name"
			if withName {
				wantExplicit = "Explicit /rename"
			}
			for id, want := range map[string]string{"default": "默认会话", "renamed": "Latest rename", "explicit": wantExplicit} {
				if names[id] != want {
					t.Errorf("name[%s] = %q, want %q", id, names[id], want)
				}
			}
		})
	}
}

func TestNamesIndexWithoutDatabase(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte("{\"id\":\"old\",\"thread_name\":\"First\"}\n{\"id\":\"old\",\"thread_name\":\"Renamed\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if names := codexhistory.Names(home); names["old"] != "Renamed" {
		t.Fatalf("names = %#v", names)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 {
		t.Fatalf("optional DB read created files: %v, %v", entries, err)
	}
}
