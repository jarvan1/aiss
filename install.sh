#!/bin/sh
# aiss installer for Linux and macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/jarvan1/aiss/main/install.sh | sh
#
# Behind the Great Firewall? Route downloads through a GitHub mirror:
#   curl -fsSL https://raw.githubusercontent.com/jarvan1/aiss/main/install.sh \
#     | sh -s -- --proxy https://gh-proxy.org/
#
# Options / env overrides:
#   --proxy <url> | AISS_PROXY   prefix GitHub download URLs with this mirror
#                                (e.g. https://gh-proxy.org/); default: none
#   AISS_VERSION                 version to install (e.g. v0.2.0); default: latest
#   AISS_INSTALL_DIR             install directory; default: ~/.local/bin
#
# POSIX sh — no bashisms, so it runs under dash/ash/busybox too.
set -eu

REPO="jarvan1/aiss"
BIN="aiss"

err() { printf 'aiss-install: %s\n' "$1" >&2; exit 1; }
info() { printf 'aiss-install: %s\n' "$1" >&2; }

# --- parse args -------------------------------------------------------------
PROXY="${AISS_PROXY:-}"
while [ $# -gt 0 ]; do
  case "$1" in
    --proxy) PROXY="${2:-}"; shift 2 ;;
    --proxy=*) PROXY="${1#--proxy=}"; shift ;;
    -h | --help)
      sed -n '2,18p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) err "unknown argument: $1" ;;
  esac
done
# Normalize: ensure a single trailing slash so concatenation is clean.
if [ -n "$PROXY" ]; then
  PROXY="${PROXY%/}/"
fi

# mirror wraps a GitHub download URL with the proxy (github.com / raw / release
# assets). The GitHub API is intentionally NOT proxied — mirrors typically 403
# api.github.com, so version resolution always goes direct (see below).
mirror() {
  if [ -n "$PROXY" ]; then
    printf '%s%s' "$PROXY" "$1"
  else
    printf '%s' "$1"
  fi
}

# --- pick a downloader ------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  dl() { curl -fsSL "$1"; }               # to stdout
  dlo() { curl -fsSL -o "$2" "$1"; }      # to file
elif command -v wget >/dev/null 2>&1; then
  dl() { wget -qO- "$1"; }
  dlo() { wget -qO "$2" "$1"; }
else
  err "need curl or wget"
fi

# --- detect os/arch, matching goreleaser's asset names ----------------------
os=$(uname -s)
case "$os" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      err "unsupported OS: $os (use install.ps1 on Windows)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64 | amd64)          arch=amd64 ;;
  aarch64 | arm64)         arch=arm64 ;;
  *)                       err "unsupported arch: $arch" ;;
esac

# --- resolve version --------------------------------------------------------
# Version resolution hits the GitHub API directly (not the proxy: mirrors 403
# api.github.com). If that fails behind a firewall, tell the user to pin one.
ver="${AISS_VERSION:-}"
if [ -z "$ver" ]; then
  info "resolving latest release…"
  # Parse tag_name out of the GitHub API without needing jq.
  ver=$(dl "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  if [ -z "$ver" ]; then
    err "could not reach the GitHub API to find the latest version.
  Pin one explicitly, e.g.:  AISS_VERSION=v0.2.0 (see the releases page)"
  fi
fi
num=${ver#v} # asset names carry the version without the leading v

asset="${BIN}_${num}_${os}_${arch}.tar.gz"
url=$(mirror "https://github.com/$REPO/releases/download/$ver/$asset")

# --- download + extract -----------------------------------------------------
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t aiss)
trap 'rm -rf "$tmp"' EXIT INT TERM

info "downloading $asset ($ver)…"
dlo "$url" "$tmp/$asset" || err "download failed: $url"

# Best-effort checksum verification when a sha256 tool is available.
# checksums.txt lines are "<sha256>  <filename>", so match field 2 exactly and
# take field 1.
if sums=$(dl "$(mirror "https://github.com/$REPO/releases/download/$ver/checksums.txt")" 2>/dev/null); then
  want=$(printf '%s\n' "$sums" | awk -v f="$asset" '$2==f {print $1}' | head -n1)
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
      got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
    else
      got=""
    fi
    if [ -n "$got" ] && [ "$got" != "$want" ]; then
      err "checksum mismatch for $asset (want $want, got $got)"
    fi
    [ -n "$got" ] && info "checksum ok"
  fi
fi

tar -xzf "$tmp/$asset" -C "$tmp" || err "extract failed"
[ -f "$tmp/$BIN" ] || err "archive did not contain $BIN"

# --- install ----------------------------------------------------------------
dir="${AISS_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$dir" || err "cannot create $dir"
install -m 0755 "$tmp/$BIN" "$dir/$BIN" 2>/dev/null || {
  cp "$tmp/$BIN" "$dir/$BIN" && chmod 0755 "$dir/$BIN"
} || err "cannot install to $dir"

# macOS: the released binary is unsigned; strip the Gatekeeper quarantine flag
# so it isn't blocked on first run. It's a single file, so no -r (which older
# xattr builds don't accept anyway).
if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$dir/$BIN" 2>/dev/null || true
fi

info "installed $BIN $ver -> $dir/$BIN"

# --- post-install hints -----------------------------------------------------
case ":$PATH:" in
  *":$dir:"*) : ;;
  *) info "note: $dir is not on your PATH — add it, e.g.:"
     printf '  export PATH="%s:$PATH"\n' "$dir" >&2 ;;
esac

cat >&2 <<'EOF'
aiss-install: done. Run `aiss` to start.
  Optional Ctrl-X Ctrl-W hotkey — add one line to your shell rc:
    eval "$(aiss init zsh)"     # or: aiss init bash | aiss init fish
EOF
