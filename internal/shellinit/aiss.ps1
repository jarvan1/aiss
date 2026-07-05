# Optional PowerShell integration for aiss — bind a key to pick & resume a session.
#
#   Add to your profile ($PROFILE):
#     Invoke-Expression (& aiss init powershell | Out-String)
#   …or dot-source this file directly:
#     . /path/to/aiss/shell/aiss.ps1
#
# Works in Windows PowerShell 5.1 and PowerShell 7+ (both ship PSReadLine).
# Default keybinding: Ctrl-X Ctrl-W. Override with $env:AISS_KEYBIND_PWSH (a
# PSReadLine chord, e.g. 'Ctrl+g') before this is sourced.

if ((Get-Module -ListAvailable PSReadLine) -and (Get-Command aiss -ErrorAction SilentlyContinue)) {
    $__aiss_chord = if ($env:AISS_KEYBIND_PWSH) { $env:AISS_KEYBIND_PWSH } else { 'Ctrl+x,Ctrl+w' }
    Set-PSReadLineKeyHandler -Chord $__aiss_chord -BriefDescription 'aiss' -LongDescription 'Pick and resume an AI CLI session' -ScriptBlock {
        $line = $null
        $cursor = $null
        [Microsoft.PowerShell.PSConsoleReadLine]::GetBufferState([ref]$line, [ref]$cursor)
        # aiss draws its picker on stderr (the console) and prints the resume
        # command to stdout, which we capture. Don't redirect stderr — on Windows
        # that's where the UI is.
        $cmd = & aiss --print --pwsh -- $line
        # The picker runs on the alt-screen; when it tears down, PSReadLine's
        # render state is out of sync with the console. InvokePrompt resyncs it
        # first — without this, AcceptLine is swallowed and the command only runs
        # after a second Enter.
        [Microsoft.PowerShell.PSConsoleReadLine]::InvokePrompt()
        if ($cmd) {
            [Microsoft.PowerShell.PSConsoleReadLine]::RevertLine()
            [Microsoft.PowerShell.PSConsoleReadLine]::Insert(($cmd -join "`n"))
            [Microsoft.PowerShell.PSConsoleReadLine]::AcceptLine()
        }
    }
    Remove-Variable __aiss_chord
}
