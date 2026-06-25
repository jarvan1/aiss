# Optional bash integration for aiss — bind a key to pick a session.
#
#   source /path/to/aiss/shell/aiss.bash      # add to ~/.bashrc
#
# Default keybinding: Ctrl-X Ctrl-W. Override the readline key sequence with
# AISS_KEYBIND_BASH (e.g. '\C-g') before sourcing.
#
# Note: unlike the zsh/fish bindings, bash places the resume command on the
# command line and you press Enter to run it — this is the same convention
# fzf's Ctrl-R uses (readline's `bind -x` can't reliably auto-accept a line).

__aiss_widget() {
  local cmd
  cmd=$(aiss --print -- "$READLINE_LINE" 2>/dev/null) || return
  [[ -z $cmd ]] && return
  READLINE_LINE=$cmd
  READLINE_POINT=${#READLINE_LINE}
}

if [[ $- == *i* ]] && command -v aiss >/dev/null 2>&1; then
  __aiss_keyseq="${AISS_KEYBIND_BASH:-\C-x\C-w}"
  bind -x "\"${__aiss_keyseq}\": __aiss_widget" 2>/dev/null
  unset __aiss_keyseq
fi
