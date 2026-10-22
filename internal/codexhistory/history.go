// Package codexhistory reads Codex's local paginated history databases. All
// connections are read-only; Codex remains responsible for schema migrations.
package codexhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jarvan1/aiss/internal/session"
	_ "modernc.org/sqlite"
)

type Thread struct {
	ID, File, Cwd, Preview, Model, AccountID string
	Version, ModelProvider, HistoryDB        string
	CreatedAt, UpdatedAt                     time.Time
	InternalReview                           bool
}

type Message struct {
	Role, Text string
}

func newestDB(home, prefix string) string {
	files, _ := filepath.Glob(filepath.Join(home, prefix+"_*.sqlite"))
	var newest string
	version := -1
	for _, file := range files {
		base := strings.TrimSuffix(filepath.Base(file), ".sqlite")
		n, err := strconv.Atoi(strings.TrimPrefix(base, prefix+"_"))
		if err == nil && n > version {
			newest, version = file, n
		}
	}
	return newest
}

func openReadOnly(path string) (*sql.DB, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	if filepath.VolumeName(abs) != "" && !strings.HasPrefix(u.Path, "/") {
		u.Path = "/" + u.Path // file:///C:/... on Windows
	}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("_pragma", "busy_timeout(1000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// List returns active paginated threads, including ones whose rollout no longer
// exists. Unsupported older schemas return an error so callers can retain their
// file-based discovery path.
func List(home string) ([]Thread, error) { return list(home, "") }

func Lookup(home, id string) (Thread, error) {
	threads, err := list(home, id)
	if err != nil {
		return Thread{}, err
	}
	if len(threads) == 0 {
		return Thread{}, sql.ErrNoRows
	}
	return threads[0], nil
}

func list(home, id string) ([]Thread, error) {
	db, err := openReadOnly(newestDB(home, "state"))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	query := `SELECT id, rollout_path, cwd,
		COALESCE(NULLIF(preview,''), NULLIF(first_user_message,''), title, ''),
		COALESCE(model,''), COALESCE(creator_account_id,''),
		COALESCE(cli_version,''), COALESCE(model_provider,''),
		COALESCE(created_at_ms, created_at*1000), COALESCE(updated_at_ms, updated_at*1000),
		COALESCE(thread_source,''), COALESCE(source,'')
		FROM threads WHERE archived=0 AND history_mode='paginated'`
	var args []any
	if id != "" {
		query += " AND id=?"
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	historyDB := newestDB(home, "thread_history")
	var out []Thread
	for rows.Next() {
		var t Thread
		var created, updated int64
		var threadSource, source string
		if err := rows.Scan(&t.ID, &t.File, &t.Cwd, &t.Preview, &t.Model, &t.AccountID,
			&t.Version, &t.ModelProvider, &created, &updated, &threadSource, &source); err != nil {
			return nil, err
		}
		t.CreatedAt, t.UpdatedAt = time.UnixMilli(created), time.UnixMilli(updated)
		t.HistoryDB = historyDB
		t.InternalReview = session.IsCodexInternalReview(threadSource, json.RawMessage(source))
		out = append(out, t)
	}
	return out, rows.Err()
}

// Messages loads the current user/assistant projection in rollout order. This
// also sees turns recorded after migration that aren't in an old .jsonl.zst.
func Messages(path, id string) ([]Message, error) {
	db, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT item_json FROM thread_items
		WHERE thread_id=? AND item_type IN ('userMessage','agentMessage')
		ORDER BY rollout_ordinal`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item struct {
			Type, Text string
			Content    []struct{ Type, Text string }
		}
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			return nil, fmt.Errorf("decode Codex history item: %w", err)
		}
		message := Message{Role: "assistant", Text: item.Text}
		if item.Type == "userMessage" {
			message.Role = "user"
			var parts []string
			for _, content := range item.Content {
				if content.Type == "text" && content.Text != "" {
					parts = append(parts, content.Text)
				}
			}
			message.Text = strings.Join(parts, "\n")
		}
		out = append(out, message)
	}
	return out, rows.Err()
}
