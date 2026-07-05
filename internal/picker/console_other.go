//go:build !windows

package picker

import "os"

// enablePolling is false on POSIX: the terminal delivers native resize events
// (SIGWINCH), so the picker's size poller isn't needed.
const enablePolling = false

// openConsole tries to open the controlling terminal (/dev/tty) so the picker
// can draw and read keys even when stdin/stdout are redirected — the case when
// a shell key widget runs us as `cmd=$(aiss --print ...)`. ok is false when
// there's no /dev/tty (e.g. a pipe with no controlling terminal), in which case
// the caller falls back to stderr.
func openConsole() (in, out *os.File, closeFn func(), ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, nil, nil, false
	}
	return tty, tty, func() { tty.Close() }, true
}
