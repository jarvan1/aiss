//go:build windows

package picker

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// enablePolling turns on the picker's size poller. Windows consoles don't
// reliably deliver resize events to bubbletea (especially on the CONIN$/CONOUT$
// handles the hotkey path uses), so we poll the size instead.
const enablePolling = true

// openConsole picks the input/output the picker uses on Windows. Two facts pull
// in opposite directions, so we branch on how aiss was invoked:
//
//   - bubbletea only emits live resize events (WindowSizeMsg after startup) when
//     its input is exactly os.Stdin — its Windows reader wires up the
//     ENABLE_WINDOW_INPUT console reader only in that case (see bubbletea
//     inputreader_windows.go). A freshly opened CONIN$ handle takes the generic
//     fallback reader, which never sees resizes.
//   - but when the pwsh key handler runs us as `$cmd = & aiss --print --pwsh`,
//     stdout is captured to a pipe and reading the inherited os.Stdin delivers
//     no key events and hangs; a fresh CONIN$ read through that fallback reader
//     is what makes keys work at all.
//
// So a fully interactive run — both stdin and stdout are real consoles — keeps
// os.Stdin and gets resize tracking. Any redirected run (the widget captures
// stdout) opens CONIN$/CONOUT$ instead: keys work, no live resize, which is fine
// for the transient picker. Each branch mirrors a configuration already known to
// work. ok is false only if a needed console device can't be opened.
func openConsole() (in, out *os.File, closeFn func(), ok bool) {
	if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
		// stderr is the console here too; using it keeps the UI off stdout for
		// `aiss --print` runs.
		return os.Stdin, os.Stderr, func() {}, true
	}

	ci, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	co, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		ci.Close()
		return nil, nil, nil, false
	}
	return ci, co, func() {
		co.Close()
		// Deliberately do NOT close ci. bubbletea's fallback input reader
		// leaves an uncancellable blocked read pending on it (cancelreader
		// can't cancel console reads), and os.File.Close waits for in-flight
		// reads to finish — so closing here would block the whole process
		// until the user pressed one more key. That stall is what made the
		// pwsh widget need a second Enter: `$cmd = & aiss ...` only returned
		// after the extra keypress released this read. The handle is torn
		// down by process exit a few milliseconds later anyway.
	}, true
}
