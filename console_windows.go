//go:build windows

package main

import "os"

// openConsole opens the attached Windows console for the picker's input and
// output. The special files CONIN$/CONOUT$ always refer to the real console,
// even when stdin/stdout are redirected to pipes — which is exactly the case
// when the PowerShell key handler runs us as `$cmd = & aiss --print --pwsh`
// (stdout captured). Reading the inherited os.Stdin there yields no key events,
// so the picker would hang; the console handles fix that. ok is false if either
// handle can't be opened (e.g. no console attached), in which case the caller
// falls back to stderr.
func openConsole() (in, out *os.File, closeFn func(), ok bool) {
	ci, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	co, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		ci.Close()
		return nil, nil, nil, false
	}
	return ci, co, func() { ci.Close(); co.Close() }, true
}
