# aiss — AI session search

A small, **cross-platform, dependency-free** binary that fuzzy-searches the
session histories of multiple AI CLIs — **Claude Code / Codex / Copilot CLI /
Gemini CLI** — and resumes the one you pick, right in its original working
directory.

It's a single Go binary: no `fzf`, no `jq`, no Oh My Zsh required. (Those are
what the [zsh-plugin version](../ai-session-search) depends on; this reimplements
the scanner and the picker natively.)

```
╭─ claude · claude-opus-4-8
│ 📁 ~/plugins
│ 🕐 2026-06-22 09:39 → 06-25 01:16  (2d15h)
│ 💬 248 user · 507 assistant   Claude Code 2.1.170
╰──────────────────────────────────────────

▶ USER
I want to build a zsh plugin that searches AI CLI session history…
```

## Install

**Homebrew** (macOS / Linux):

```sh
brew install jarvan1/tap/aiss
```

**Pre-built binary** (no toolchain needed) — grab the archive for your OS/arch
from the [latest release](https://github.com/jarvan1/aiss/releases/latest),
unpack it, and put `aiss` on your `PATH`. Each release is built automatically by
GitHub Actions for macOS / Linux / Windows (amd64 + arm64).

**With Go:**

```sh
go install github.com/jarvan1/aiss@latest        # needs Go 1.21+
```

**Enable the hotkey** (optional) — add one line to your shell rc:

```sh
eval "$(aiss init zsh)"     # or: aiss init bash | aiss init fish
```

This binds **Ctrl-X Ctrl-W**. Without it, just run `aiss`.

## Usage

```sh
aiss                 # open the picker; Enter resumes the selected session
aiss codex           # open with an initial filter term
aiss --print         # print the resume command instead of running it
aiss scan            # list sessions: provider, id, cwd, preview, file (for scripts)
aiss preview <p> <f> # render one session's preview (debugging)
```

In the picker, type to fuzzy-filter (the provider name is part of each row, so
typing `claude` narrows to Claude), use the arrow keys to move, watch the live
preview pane on the right, and press Enter to resume.

### Key binding

A binary can't, by itself, put a command on your shell's command line (this is
exactly why `fzf` also ships shell snippets). `aiss` carries the integration for
each shell embedded inside it — source it from your rc with one line:

```sh
eval "$(aiss init zsh)"     # zsh   → ~/.zshrc
eval "$(aiss init bash)"    # bash  → ~/.bashrc
eval "$(aiss init fish)"    # fish  → ~/.config/fish/config.fish
```

All three bind **Ctrl-X Ctrl-W**. zsh and fish run the selected session
immediately; bash places the resume command on the line for you to press Enter
(readline's `bind -x` can't reliably auto-accept — the same reason fzf's Ctrl-R
behaves this way). Override the key with `AISS_KEYBIND` (zsh),
`AISS_KEYBIND_BASH`, or `AISS_KEYBIND_FISH`.

(Working from a source checkout instead? The same snippets live in `shell/` and
can be `source`d directly.)

Without any of these, just run `aiss` — it resumes the session directly.

## How it works

For each provider, `aiss` reads the session files straight off disk (no external
tools) and dispatches resume to the matching CLI:

| Provider | Scanned path | Resume |
|----------|--------------|--------|
| Claude   | `~/.claude/projects/<dir>/<uuid>.jsonl` | `claude --resume <uuid>` |
| Codex    | `~/.codex/sessions/**/rollout-*.jsonl`  | `codex resume <uuid> [-m model]` |
| Copilot  | `~/.copilot/session-state/<uuid>/events.jsonl` | `copilot --resume=<uuid>` |
| Gemini   | `~/.gemini/tmp/<hash>/logs.json` (experimental) | opens in the dir (no resume-by-id) |

Resume always runs in the session's **original** directory (recreated if it was
deleted, so resume-by-cwd lookups still match). The matching CLI only needs to be
on `PATH` to *resume*; browsing and previewing work without any of them.

Sessions whose original directory no longer exists are hidden — set
`AISS_SHOW_MISSING=1` to keep them.

## Configuration

| Variable | Default |
|----------|---------|
| `AISS_CLAUDE_DIR`  | `~/.claude/projects` |
| `AISS_CODEX_DIR`   | `~/.codex/sessions` |
| `AISS_COPILOT_DIR` | `~/.copilot/session-state` |
| `AISS_GEMINI_DIR`  | `~/.gemini/tmp` |
| `AISS_SHOW_MISSING`| unset (set to `1` to keep deleted-dir sessions) |
| `AISS_KEYBIND`     | `^X^W` (only used by the optional zsh integration) |

## Cross-platform builds

```sh
GOOS=linux   GOARCH=amd64 go build -o dist/aiss-linux-amd64 .
GOOS=darwin  GOARCH=arm64 go build -o dist/aiss-darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -o dist/aiss-windows-amd64.exe .
```

The picker (via `tcell`) works on Windows terminals too.

## Development

```sh
go test ./...   # unit tests (resume command building, quoting)
go vet ./...
```
