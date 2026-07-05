# Optional PowerShell integration for aiss — bind a key to pick & resume a session.
#
#   Add to your profile ($PROFILE):
#     Invoke-Expression (& aiss init powershell | Out-String)
#   …or dot-source this file directly:
#     . /path/to/aiss/internal/shellinit/aiss.ps1
#
# Works in Windows PowerShell 5.1 and PowerShell 7+ (both ship PSReadLine).
# Default keybinding: Ctrl-X Ctrl-W. Override with $env:AISS_KEYBIND_PWSH (a
# PSReadLine chord, e.g. 'Ctrl+g') before this is sourced.

if ((Get-Module -ListAvailable PSReadLine) -and (Get-Command aiss -ErrorAction SilentlyContinue)) {
    $__aiss_chord = if ($env:AISS_KEYBIND_PWSH) { $env:AISS_KEYBIND_PWSH } else { 'Ctrl+x,Ctrl+w' }
    Set-PSReadLineKeyHandler -Chord $__aiss_chord -BriefDescription 'aiss' -LongDescription 'Pick and resume an AI CLI session' -ScriptBlock {
        param($key, $arg)

        $line = $null
        $cursor = $null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)

        # aiss draws its picker on the console (CONIN$/CONOUT$) and prints the
        # resume command to stdout, which we capture. AcceptLine right after is
        # safe: PSReadLine processes it as soon as this handler returns.
        $cmd = & aiss --print --pwsh -- $line
        if ($cmd) {
            [Microsoft.PowerShell.PSConsoleReadLine]::Replace(0, $line.Length, ($cmd -join ' '))
            [Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
        }
        else {
            # Aborted (Esc): nothing to run; repaint the prompt after the
            # alt-screen teardown.
            [Microsoft.PowerShell.PSConsoleReadLine]::InvokePrompt()
        }
    }
    Remove-Variable __aiss_chord
}
