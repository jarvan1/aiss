//go:build windows

// Command pwshhotkey is an end-to-end check for the PowerShell key widget: it
// hosts a real powershell.exe (with PSReadLine) inside a pseudo console,
// presses Ctrl-X Ctrl-W, selects the single fixture session in the picker, and
// asserts the resume command executes after ONE Enter — the "press Enter
// twice" regression this exists to catch. A fake claude.bat stands in for the
// real CLI, and the fixture session dirs isolate the run from the machine's
// real session history.
//
// Usage (from the repo root, Windows only):
//
//	go build -o <tmp>/aiss.exe ./cmd/aiss
//	go run ./e2e/pwshhotkey <tmp>/aiss.exe
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                          = windows.NewLazySystemDLL("kernel32.dll")
	procInitializeProcThreadAttrList  = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute     = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList = kernel32.NewProc("DeleteProcThreadAttributeList")
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: pwshhotkey <path-to-aiss.exe>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

// console collects the pseudo console's output stream.
type console struct {
	mu  sync.Mutex
	buf strings.Builder
	in  *os.File
}

func (c *console) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// waitFor polls until marker appears in the output beyond offset `from`, so
// repeated markers (two picker runs, two prompts) don't satisfy each other.
func (c *console) waitFor(marker string, from int, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s := c.String()
		if len(s) > from {
			if i := strings.Index(s[from:], marker); i >= 0 {
				return from + i + len(marker), true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return from, false
}

func (c *console) send(s string) {
	c.in.Write([]byte(s))
}

func run(aissExe string) error {
	tmp, err := os.MkdirTemp("", "aiss-e2e-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	// --- fixtures ---------------------------------------------------------
	bin := filepath.Join(tmp, "bin")
	claudeDir := filepath.Join(tmp, "claude", "proj")
	empty := filepath.Join(tmp, "empty")
	for _, d := range []string{bin, claudeDir, empty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// One fake claude session whose cwd (tmp) exists.
	jsonl := fmt.Sprintf("{\"type\":\"user\",\"cwd\":%q,\"message\":{\"content\":\"hello from e2e\"}}\n", tmp)
	if err := os.WriteFile(filepath.Join(claudeDir, "11111111-2222-3333-4444-555555555555.jsonl"), []byte(jsonl), 0o644); err != nil {
		return err
	}
	// Fake claude on PATH: prints a marker and appends to a run log. The log is
	// the ground truth for "did a resume execute" — the output stream can't be,
	// because ConPTY repaints scrollback (containing old marker text) whenever
	// the alt screen closes.
	runLog := filepath.Join(tmp, "runs.txt")
	if err := os.WriteFile(filepath.Join(bin, "claude.bat"),
		[]byte("@echo AISS_RESUMED %*\r\n@echo ran>>\"%AISS_E2E_RUNLOG%\"\r\n"), 0o644); err != nil {
		return err
	}
	os.Setenv("AISS_E2E_RUNLOG", runLog)
	countRuns := func() int {
		b, err := os.ReadFile(runLog)
		if err != nil {
			return 0
		}
		return strings.Count(string(b), "ran")
	}
	exe, err := os.ReadFile(aissExe)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "aiss.exe"), exe, 0o755); err != nil {
		return err
	}
	// The child inherits our environment (CreateProcess env == nil).
	os.Setenv("PATH", bin+";"+os.Getenv("PATH"))
	os.Setenv("AISS_CLAUDE_DIR", filepath.Join(tmp, "claude"))
	for _, v := range []string{"AISS_CODEX_DIR", "AISS_COPILOT_DIR", "AISS_GEMINI_DIR"} {
		os.Setenv(v, empty)
	}
	setup := filepath.Join(tmp, "setup.ps1")
	if err := os.WriteFile(setup, []byte("Invoke-Expression (& aiss init powershell | Out-String)\nWrite-Host READY_E2E\n"), 0o644); err != nil {
		return err
	}

	// --- pseudo console + powershell ---------------------------------------
	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		return err
	}
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		return err
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 32}, inR, outW, 0, &hpc); err != nil {
		return fmt.Errorf("CreatePseudoConsole: %w", err)
	}

	// The attribute list is managed via raw kernel32 calls: x/sys's typed
	// Update wants an unsafe.Pointer value, but PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
	// takes the HPCON *by value*, and converting a handle to unsafe.Pointer
	// trips go vet. Raw uintptr args sidestep that cleanly.
	var alSize uintptr
	procInitializeProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&alSize)))
	alBuf := make([]byte, alSize)
	alPtr := unsafe.Pointer(&alBuf[0])
	if r1, _, e := procInitializeProcThreadAttrList.Call(uintptr(alPtr), 1, 0, uintptr(unsafe.Pointer(&alSize))); r1 == 0 {
		return fmt.Errorf("InitializeProcThreadAttributeList: %v", e)
	}
	defer procDeleteProcThreadAttributeList.Call(uintptr(alPtr))
	const procThreadAttributePseudoconsole = 0x20016
	if r1, _, e := procUpdateProcThreadAttribute.Call(uintptr(alPtr), 0, procThreadAttributePseudoconsole,
		uintptr(hpc), unsafe.Sizeof(hpc), 0, 0); r1 == 0 {
		return fmt.Errorf("attach pseudoconsole: %v", e)
	}

	si := new(windows.StartupInfoEx)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ProcThreadAttributeList = (*windows.ProcThreadAttributeList)(alPtr)
	// Explicit NULL std handles (the Windows Terminal trick): without this,
	// CreateProcess duplicates the harness's pipe std handles into the console
	// child, and its output bypasses the pseudo console entirely.
	si.Flags |= windows.STARTF_USESTDHANDLES
	cmdline, err := windows.UTF16PtrFromString(`powershell.exe -NoProfile -NoExit -ExecutionPolicy Bypass -File "` + setup + `"`)
	if err != nil {
		return err
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmdline, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		nil, nil, &si.StartupInfo, &pi); err != nil {
		return fmt.Errorf("CreateProcess powershell: %w", err)
	}
	defer func() {
		windows.TerminateProcess(pi.Process, 0)
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
	}()
	// Child-side pipe ends belong to conhost now.
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)

	c := &console{in: os.NewFile(uintptr(inW), "conin")}
	out := os.NewFile(uintptr(outR), "conout")
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := out.Read(b)
			if n > 0 {
				c.mu.Lock()
				c.buf.Write(b[:n])
				c.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	dump := func(label string) string {
		p := filepath.Join(os.TempDir(), "aiss-e2e-output.log")
		os.WriteFile(p, []byte(c.String()), 0o644)
		return fmt.Sprintf("%s (raw output saved to %s)", label, p)
	}

	// --- scenario 1: pick a session, count the Enters -----------------------
	pos, ok := c.waitFor("READY_E2E", 0, 30*time.Second)
	if !ok {
		return fmt.Errorf("%s", dump("powershell never became ready"))
	}
	time.Sleep(500 * time.Millisecond) // let PSReadLine take over the prompt

	c.send("\x18") // Ctrl-X
	time.Sleep(150 * time.Millisecond)
	c.send("\x17") // Ctrl-W
	pos, ok = c.waitFor("ai-sessions", pos, 15*time.Second)
	if !ok {
		return fmt.Errorf("%s", dump("picker did not open on Ctrl-X Ctrl-W"))
	}
	time.Sleep(600 * time.Millisecond) // let the picker finish its first paint

	c.send("\r") // Enter #1: select the only session
	if next, ok := c.waitFor("AISS_RESUMED", pos, 5*time.Second); ok {
		pos = next
		for i := 0; countRuns() != 1 && i < 40; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		if n := countRuns(); n != 1 {
			return fmt.Errorf("%s", dump(fmt.Sprintf("run log has %d executions after scenario 1, want 1", n)))
		}
		fmt.Println("PASS: resume command executed after a SINGLE Enter")
	} else {
		// Document the failure mode precisely: does a second Enter release it?
		c.send("\r")
		if _, ok := c.waitFor("AISS_RESUMED", pos, 5*time.Second); ok {
			return fmt.Errorf("%s", dump("BUG REPRODUCED: resume needed a SECOND Enter"))
		}
		return fmt.Errorf("%s", dump("resume never executed even after two Enters"))
	}

	// --- scenario 2: abort with Esc — nothing must execute ------------------
	c.send("\x18")
	time.Sleep(150 * time.Millisecond)
	c.send("\x17")
	pos, ok = c.waitFor("ai-sessions", pos, 15*time.Second)
	if !ok {
		return fmt.Errorf("%s", dump("picker did not open for the abort scenario"))
	}
	time.Sleep(400 * time.Millisecond)
	c.send("\x1b") // Esc: abort
	time.Sleep(1500 * time.Millisecond)
	// The shell must still be alive and accept a normal command.
	c.send("echo CANARY_$('OK')\r")
	if _, ok := c.waitFor("CANARY_OK", pos, 10*time.Second); !ok {
		return fmt.Errorf("%s", dump("shell unresponsive after abort"))
	}
	if n := countRuns(); n != 1 {
		return fmt.Errorf("%s", dump(fmt.Sprintf("run log has %d resume executions, want exactly 1 (abort must not run)", n)))
	}
	fmt.Println("PASS: abort leaves the prompt clean and responsive")
	return nil
}
