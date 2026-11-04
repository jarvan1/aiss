package codexhistory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/jarvan1/aiss/internal/session"
)

// Names reads optional naming metadata without depending on the paginated
// history schema. Explicit DB names win over the CLI's append-only name index;
// generated titles are the fallback. No transcript text is used as a name.
func Names(home string) map[string]string {
	names := make(map[string]string)
	times := make(map[string]time.Time)
	_ = session.EachLine(filepath.Join(home, "session_index.jsonl"), func(raw []byte) bool {
		var row struct {
			ID        string    `json:"id"`
			Name      string    `json:"thread_name"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		if json.Unmarshal(raw, &row) == nil && row.ID != "" && strings.TrimSpace(row.Name) != "" &&
			!row.UpdatedAt.Before(times[row.ID]) {
			names[row.ID], times[row.ID] = row.Name, row.UpdatedAt
		}
		return true
	})
	db, err := openReadOnly(newestDB(home, "state"))
	if err != nil {
		return names
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Older state DBs have title but no name. Inspect only column names so a
	// missing newer column never disables naming for legacy sessions.
	rows, err := db.QueryContext(ctx, `SELECT * FROM threads LIMIT 0`)
	if err != nil {
		return names
	}
	columns, err := rows.Columns()
	rows.Close()
	if err != nil {
		return names
	}
	has := make(map[string]bool)
	for _, column := range columns {
		has[column] = true
	}
	if !has["id"] {
		return names
	}
	nameExpr, titleExpr := "''", "''"
	if has["name"] {
		nameExpr = "COALESCE(name,'')"
	}
	if has["title"] {
		titleExpr = "COALESCE(title,'')"
	}
	rows, err = db.QueryContext(ctx, `SELECT id, `+nameExpr+`, `+titleExpr+` FROM threads`)
	if err != nil {
		return names
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, title string
		if rows.Scan(&id, &name, &title) != nil {
			continue
		}
		if strings.TrimSpace(name) != "" {
			names[id] = name
		} else if names[id] == "" && strings.TrimSpace(title) != "" {
			names[id] = title
		}
	}
	return names
}
