# Optional fish integration for aiss — bind a key to pick & resume a session.
#
#   source /path/to/aiss/shell/aiss.fish
#   # …or drop this file in ~/.config/fish/conf.d/ to load it automatically.
#
# Default keybinding: Ctrl-X Ctrl-W. Override with $AISS_KEYBIND_FISH (a fish
# key sequence, e.g. \cg) before this is sourced.

function _aiss_widget
    # `aiss --print` draws its UI on /dev/tty and prints the resume command to
    # stdout, which we capture and run.
    set -l cmd (command aiss --print -- (commandline) 2>/dev/null)
    if test -n "$cmd"
        commandline -r -- "$cmd"
        commandline -f execute
    else
        commandline -f repaint
    end
end

if status is-interactive; and command -q aiss
    set -q AISS_KEYBIND_FISH; or set -l AISS_KEYBIND_FISH \cx\cw
    bind $AISS_KEYBIND_FISH _aiss_widget
end
