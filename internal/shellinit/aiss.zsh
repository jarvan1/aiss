# Optional shell integration for aiss — bind a key to pick & resume a session.
#
#   source /path/to/aiss/shell/aiss.zsh
#
# This is the ONLY shell-specific piece: a single binary can't inject a command
# into the parent shell's line editor by itself (fzf ships the same kind of
# snippet). Without it, just run `aiss` as a command — it resumes directly.
#
# Default keybinding: Ctrl-X Ctrl-W (set AISS_KEYBIND to override before sourcing).

_aiss_widget() {
  # `aiss --print` draws its UI on /dev/tty and prints the resume command to
  # stdout, which we capture and drop on the command line.
  local cmd
  cmd=$(command aiss --print -- "$LBUFFER" 2>/dev/null)
  if [[ -n $cmd ]]; then
    BUFFER=$cmd
    zle accept-line
  else
    zle reset-prompt
  fi
}

if [[ -o interactive ]] && (( $+commands[aiss] )); then
  zle -N _aiss_widget
  bindkey "${AISS_KEYBIND:-^X^W}" _aiss_widget
fi
