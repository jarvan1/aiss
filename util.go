package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// envOr returns the value of the environment variable key, or def if unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// tilde abbreviates $HOME to ~ for display.
func tilde(p string) string {
	h := home()
	if h != "" && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

var wsRun = regexp.MustCompile(`[\n\t\r]+`)

// collapseWS replaces runs of newlines/tabs/CRs with a single space and trims.
func collapseWS(s string) string {
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// truncRunes truncates s to at most n runes (so we never cut a multibyte char).
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// eachLine streams a (possibly large) JSONL file line by line. The callback gets
// the raw bytes of each non-empty line; return false to stop early.
func eachLine(path string, fn func(raw []byte) bool) error {
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

// glob is a thin wrapper that swallows the (only-on-bad-pattern) error.
func glob(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}

// parseTime parses the ISO-8601 timestamps the CLIs emit (RFC3339, with or
// without fractional seconds). Returns zero time on failure.
func parseTime(s string) time.Time {
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
