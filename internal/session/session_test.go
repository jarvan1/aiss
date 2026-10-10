package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCodexProfilesFromExplicitHomes(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, ".codex-work")
	personal := filepath.Join(root, ".codex-personal")
	for home, account := range map[string]string{work: "acct-work", personal: "acct-personal"} {
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		auth := []byte(`{"tokens":{"account_id":"` + account + `"}}`)
		if err := os.WriteFile(filepath.Join(home, "auth.json"), auth, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AISS_CODEX_HOMES", work+string(os.PathListSeparator)+personal+string(os.PathListSeparator)+work)

	sqliteHome := filepath.Join(root, ".codex")
	profiles := discoverCodexProfiles(root, sqliteHome)
	if len(profiles) != 2 {
		t.Fatalf("profiles = %#v, want two unique homes", profiles)
	}
	if profiles[0].Name != "work" || profiles[0].AccountID != "acct-work" || profiles[0].SQLiteHome != sqliteHome {
		t.Fatalf("work profile = %#v", profiles[0])
	}
	if profiles[1].Name != "personal" || profiles[1].AccountID != "acct-personal" {
		t.Fatalf("personal profile = %#v", profiles[1])
	}
}

func TestDefaultDirsUsesSharedCodexSQLiteHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AISS_CODEX_SQLITE_HOME", root)
	t.Setenv("AISS_CODEX_DIR", "")
	t.Setenv("AISS_CODEX_HOMES", string(os.PathListSeparator))

	dirs := DefaultDirs()
	if dirs.Codex != filepath.Join(root, "sessions") {
		t.Fatalf("Codex dir = %q, want %q", dirs.Codex, filepath.Join(root, "sessions"))
	}
}

func TestDefaultDirsInfersSQLiteHomeFromCodexDirOverride(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "runtime", "sessions")
	work := filepath.Join(root, ".codex-work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "config.toml"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AISS_CODEX_DIR", sessions)
	t.Setenv("AISS_CODEX_SQLITE_HOME", "")
	t.Setenv("CODEX_SQLITE_HOME", "")
	t.Setenv("AISS_CODEX_HOMES", work)

	dirs := DefaultDirs()
	if dirs.Codex != sessions {
		t.Fatalf("Codex dir = %q, want %q", dirs.Codex, sessions)
	}
	if len(dirs.CodexProfiles) != 1 {
		t.Fatalf("profiles = %#v", dirs.CodexProfiles)
	}
	if got := dirs.CodexProfiles[0].SQLiteHome; got != filepath.Join(root, "runtime") {
		t.Fatalf("inferred SQLite home = %q", got)
	}
}

func TestSelectableCodexProfilesDropsDuplicateDefaultAccount(t *testing.T) {
	profiles := []CodexProfile{
		{Name: "personal", AccountID: "acct-personal"},
		{Name: "work", AccountID: "acct-work"},
		{Name: "default", AccountID: "acct-personal"},
	}
	got := SelectableCodexProfiles(profiles)
	if len(got) != 2 || got[0].Name != "personal" || got[1].Name != "work" {
		t.Fatalf("selectable profiles = %#v", got)
	}
}

func TestRememberCodexProfileRoundTrip(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AISS_CONFIG_DIR", configDir)

	want := Session{
		Provider:  "codex",
		ID:        "legacy-session",
		Profile:   "work",
		CodexHome: filepath.Join(t.TempDir(), ".codex-work"),
	}
	if err := RememberCodexProfile(want); err != nil {
		t.Fatal(err)
	}
	got := LoadCodexProfileSelections()[want.ID]
	if got.Name != want.Profile || got.Home != want.CodexHome {
		t.Fatalf("remembered profile = %#v, want name=%q home=%q", got, want.Profile, want.CodexHome)
	}

	path := filepath.Join(configDir, "codex-profiles.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("selection store permissions = %o, want private", info.Mode().Perm())
	}
}

func TestRememberCodexProfilePreservesExistingChoices(t *testing.T) {
	t.Setenv("AISS_CONFIG_DIR", t.TempDir())
	for _, item := range []Session{
		{Provider: "codex", ID: "old-personal", Profile: "personal", CodexHome: "/profiles/personal"},
		{Provider: "codex", ID: "old-work", Profile: "work", CodexHome: "/profiles/work"},
	} {
		if err := RememberCodexProfile(item); err != nil {
			t.Fatal(err)
		}
	}
	got := LoadCodexProfileSelections()
	if len(got) != 2 || got["old-personal"].Name != "personal" || got["old-work"].Name != "work" {
		t.Fatalf("remembered profiles = %#v", got)
	}
}
