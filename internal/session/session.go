// Package session defines the core Session type shared across aiss and the
// small filesystem/text helpers the scanners and preview renderers both use.
package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Session is one resumable AI-CLI conversation discovered on disk.
type Session struct {
	Provider        string    // claude | codex | copilot | gemini
	ID              string    // resume key (uuid / dir name)
	Cwd             string    // original working directory
	File            string    // path to the session file on disk
	Preview         string    // first real user message, single-lined
	ModTime         time.Time // file mtime, used as the sort key (newest first)
	Profile         string    // optional account/config profile label (e.g. work)
	CodexHome       string    // CODEX_HOME needed to resume/delete this Codex session
	CodexSQLiteHome string    // shared Codex runtime-state home, when configured
}

// ProviderLabel returns the picker/scan label, including an originating
// profile when one was identified.
func (s Session) ProviderLabel() string {
	if s.Profile == "" {
		return s.Provider
	}
	return s.Provider + ":" + s.Profile
}

// CodexProfile identifies one Codex configuration/auth home. AccountID is read
// only to associate shared rollout files with the home that created them; it is
// never displayed or passed to child processes.
type CodexProfile struct {
	Name       string
	Home       string
	SQLiteHome string
	AccountID  string
}

// CodexProfileSelection records the profile a user chose for a session whose
// original rollout predates creator_account_id metadata.
type CodexProfileSelection struct {
	Name string `json:"name"`
	Home string `json:"home"`
}

// Dirs holds the per-provider scan roots, overridable via AISS_*_DIR env vars.
type Dirs struct {
	Claude        string
	Codex         string
	CodexProfiles []CodexProfile
	// CodexProfileSelections is aiss-owned compatibility metadata. Codex's own
	// rollout files and SQLite database are never modified.
	CodexProfileSelections map[string]CodexProfileSelection
	Copilot                string
	Gemini                 string
}

// DefaultDirs returns the scan roots, honoring the AISS_*_DIR overrides.
func DefaultDirs() Dirs {
	h := Home()
	codexDir := os.Getenv("AISS_CODEX_DIR")
	sqliteHome := EnvOr("AISS_CODEX_SQLITE_HOME", os.Getenv("CODEX_SQLITE_HOME"))
	if sqliteHome == "" {
		if codexDir != "" {
			sqliteHome = filepath.Dir(codexDir)
		} else {
			sqliteHome = filepath.Join(h, ".codex")
		}
	}
	if codexDir == "" {
		codexDir = filepath.Join(sqliteHome, "sessions")
	}
	return Dirs{
		Claude:                 EnvOr("AISS_CLAUDE_DIR", filepath.Join(h, ".claude", "projects")),
		Codex:                  codexDir,
		CodexProfiles:          discoverCodexProfiles(h, sqliteHome),
		CodexProfileSelections: LoadCodexProfileSelections(),
		Copilot:                EnvOr("AISS_COPILOT_DIR", filepath.Join(h, ".copilot", "session-state")),
		Gemini:                 EnvOr("AISS_GEMINI_DIR", filepath.Join(h, ".gemini", "tmp")),
	}
}

type codexProfileSelectionStore struct {
	Version  int                              `json:"version"`
	Sessions map[string]CodexProfileSelection `json:"sessions"`
}

func codexProfileSelectionPath() (string, error) {
	if configured := os.Getenv("AISS_CONFIG_DIR"); configured != "" {
		return filepath.Join(configured, "codex-profiles.json"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aiss", "codex-profiles.json"), nil
}

func readCodexProfileSelections() (map[string]CodexProfileSelection, error) {
	path, err := codexProfileSelectionPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return make(map[string]CodexProfileSelection), nil
	}
	if err != nil {
		return nil, err
	}
	var store codexProfileSelectionStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Sessions == nil {
		store.Sessions = make(map[string]CodexProfileSelection)
	}
	return store.Sessions, nil
}

// LoadCodexProfileSelections loads choices made for legacy Codex sessions. A
// missing or unreadable optional store must not prevent session discovery.
func LoadCodexProfileSelections() map[string]CodexProfileSelection {
	selections, err := readCodexProfileSelections()
	if err != nil {
		return nil
	}
	return selections
}

// RememberCodexProfile persists the selected profile without altering Codex's
// rollout or SQLite state. The next scan can then label and preselect legacy
// sessions that contain no creator_account_id.
func RememberCodexProfile(s Session) error {
	if s.Provider != "codex" || s.ID == "" || s.Profile == "" || s.CodexHome == "" {
		return nil
	}
	selections, err := readCodexProfileSelections()
	if err != nil {
		return err
	}
	selections[s.ID] = CodexProfileSelection{Name: s.Profile, Home: s.CodexHome}
	path, err := codexProfileSelectionPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(codexProfileSelectionStore{Version: 1, Sessions: selections}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// discoverCodexProfiles finds separate Codex config/auth homes while keeping
// runtime state in sqliteHome. AISS_CODEX_HOMES is an OS path-list override;
// otherwise CODEX_HOME, ~/.codex-* homes, and ~/.codex are considered in that
// order. Non-default homes intentionally precede ~/.codex so an account shared
// by ~/.codex-personal and ~/.codex is labelled "personal".
func discoverCodexProfiles(home, sqliteHome string) []CodexProfile {
	var homes []string
	if configured := os.Getenv("AISS_CODEX_HOMES"); configured != "" {
		homes = filepath.SplitList(configured)
	} else {
		if current := os.Getenv("CODEX_HOME"); current != "" {
			// Keep ~/.codex last so a named home wins when both use the same
			// account (the common personal + shared-runtime layout).
			if filepath.Clean(current) != filepath.Join(home, ".codex") {
				homes = append(homes, current)
			}
		}
		matches, _ := filepath.Glob(filepath.Join(home, ".codex-*"))
		homes = append(homes, matches...)
		homes = append(homes, filepath.Join(home, ".codex"))
	}

	seen := make(map[string]bool)
	profiles := make([]CodexProfile, 0, len(homes))
	for _, profileHome := range homes {
		if profileHome == "" {
			continue
		}
		profileHome = filepath.Clean(profileHome)
		if seen[profileHome] {
			continue
		}
		seen[profileHome] = true
		if !DirExists(profileHome) {
			continue
		}
		if _, authErr := os.Stat(filepath.Join(profileHome, "auth.json")); authErr != nil {
			if _, configErr := os.Stat(filepath.Join(profileHome, "config.toml")); configErr != nil {
				continue
			}
		}
		profiles = append(profiles, CodexProfile{
			Name:       codexProfileName(profileHome),
			Home:       profileHome,
			SQLiteHome: sqliteHome,
			AccountID:  codexAccountID(profileHome),
		})
	}
	return profiles
}

// SelectableCodexProfiles removes a redundant default home when a named home
// uses the same account. Shared SQLite layouts commonly keep a compatibility
// auth.json in ~/.codex as well as ~/.codex-personal; presenting both would be
// two labels for the same effective profile.
func SelectableCodexProfiles(profiles []CodexProfile) []CodexProfile {
	namedAccounts := make(map[string]bool)
	for _, profile := range profiles {
		if profile.Name != "default" && profile.AccountID != "" {
			namedAccounts[profile.AccountID] = true
		}
	}
	out := make([]CodexProfile, 0, len(profiles))
	for _, profile := range profiles {
		if profile.Name == "default" && profile.AccountID != "" && namedAccounts[profile.AccountID] {
			continue
		}
		out = append(out, profile)
	}
	return out
}

func codexProfileName(home string) string {
	base := filepath.Base(home)
	if base == ".codex" {
		return "default"
	}
	if name := strings.TrimPrefix(base, ".codex-"); name != base && name != "" {
		return name
	}
	return strings.TrimPrefix(base, ".")
}

func codexAccountID(home string) string {
	data, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return ""
	}
	var auth struct {
		Tokens struct {
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return ""
	}
	return auth.Tokens.AccountID
}

// ShowMissing reports whether sessions whose cwd no longer exists should be kept.
func ShowMissing() bool { return EnvOr("AISS_SHOW_MISSING", "") != "" }

// Shared regexes: a leading "<" marks an injected/system message, and the codex
// variant also catches its wrapper tags. Both scan and preview filter on these.
var (
	ReAngle         = regexp.MustCompile(`^<`)
	ReCaveat        = regexp.MustCompile(`^Caveat:`)
	ReCodexInjected = regexp.MustCompile(`^(<(environment_context|user_instructions|permissions|INSTRUCTIONS)|# AGENTS\.md)`)
)

// EnvOr returns the value of the environment variable key, or def if unset/empty.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Home is the user's home directory, or "" if it can't be determined.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// Tilde abbreviates $HOME to ~ for display.
func Tilde(p string) string {
	h := Home()
	if h != "" && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

// DirExists reports whether p is an existing directory.
func DirExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

var wsRun = regexp.MustCompile(`[\n\t\r]+`)

// CollapseWS replaces runs of newlines/tabs/CRs with a single space and trims.
func CollapseWS(s string) string {
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// TruncRunes truncates s to at most n runes (so we never cut a multibyte char).
func TruncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// EachLine streams a (possibly large) JSONL file line by line. The callback gets
// the raw bytes of each non-empty line; return false to stop early.
func EachLine(path string, fn func(raw []byte) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Session lines (esp. assistant turns with embedded tool output) can be huge.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		if !fn(b) {
			return nil
		}
	}
	return sc.Err()
}

// ParseTime parses the ISO-8601 timestamps the CLIs emit (RFC3339, with or
// without fractional seconds). Returns zero time on failure.
func ParseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
