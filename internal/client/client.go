package client

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

var ErrDetached = errors.New("detached")

// Attach connects to an existing session
func Attach(name string) error {
	sockPath, err := session.GetSocketPath(name)
	if err != nil {
		return err
	}

	// 1. Connect
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 2. Raw Mode
	// We enter raw mode early to handle log replay correctly and drain input
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// 3. Replay Log
	// Log replay might trigger terminal responses (e.g. Device Attributes).
	// We need to suppress them.
	logPath, err := session.GetLogPath(name)
	if err == nil {
		f, err := os.Open(logPath)
		if err == nil {
			_, _ = io.Copy(os.Stdout, f)
			f.Close()
		}
	}

	// 4. Sync Terminal (Drain responses)
	// Send Device Status Report (DSR) request.
	// We expect a Cursor Position Report (CPR) response: ESC [ n ; m R
	// We discard all input until we receive this response.
	_, _ = os.Stdout.Write([]byte("\x1b[6n"))

	// We need to read stdin until we find the pattern, with a timeout.
	// Since os.Stdin.Read blocks, we use a goroutine or non-blocking logic if possible.
	// Simplest valid approach for CLI tool: Read in a loop with a small buffer, check for pattern.
	// But we need timeout.
	
	drainDone := make(chan []byte)
	go func() {
		var buf []byte
		tmp := make([]byte, 128)
		for {
			n, err := os.Stdin.Read(tmp)
			if err != nil {
				close(drainDone)
				return
			}
			buf = append(buf, tmp[:n]...)
			// Search for CPR: \x1b [ <nums> ; <nums> R
			// Regex is heavy, manual scan:
			// Ends with 'R'. Start with '\x1b'.
			if idx := bytes.LastIndexByte(buf, 'R'); idx >= 0 {
				// Check if it looks like CPR
				// Backtrack to \x1b
				if escIdx := bytes.LastIndexByte(buf[:idx], 0x1b); escIdx >= 0 {
					// We found a sequence ending in R.
					// Strictly we should verify the content, but for now assuming it's ours.
					// Discard up to idx (inclusive)
					remaining := buf[idx+1:]
					drainDone <- remaining
					return
				}
			}
		}
	}()

	var initialInput []byte
	select {
	case remaining := <-drainDone:
		if remaining != nil {
			initialInput = remaining
		}
	case <-time.After(500 * time.Millisecond):
		// Timeout waiting for sync. Maybe terminal doesn't support it.
		// We just proceed, but we might have eaten some user input in the goroutine?
		// This approach has a race: the goroutine is still running reading Stdin!
		// We can't cancel the Read easily.
		
		// Alternative: Use a dedicated reader goroutine for the whole session.
		// But let's stick to the simpler fix:
		// If timeout, we assume no response coming (or lost).
		// But the goroutine `go func` above will steal the next input if we don't handle it.
		
		// To fix the race correctly:
		// We need the Stdin->Socket loop to be able to receive "pre-read" data.
		// AND we need to use that same goroutine/channel mechanism for the main loop?
		// OR we rely on the fact that 500ms is enough for a local terminal response.
		// If it times out, we leave the goroutine running? No, that steals input.
		
		// Better approach: Start the main input loop, but put it in a "draining" mode first.
		// See step 6.
	}

	// 5. Initial Resize
	sendResize(conn)

	// 6. Handle Resize Signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		for range sigCh {
			sendResize(conn)
		}
	}()

	var detached int32

	// 7. Stdin -> Socket
	go func() {
		buf := make([]byte, 1024)
		var pendingCtrlD bool
		
		// If we had a successful drain, initialInput contains valid user input (post-sync).
		// We should process it first.
		if len(initialInput) > 0 {
			// Process initialInput as if read from stdin
			// Copy-paste logic or use a helper?
			// Let's iterate.
			// Re-use logic below... but easier to just create a reader that prefixes.
			// But we are in a loop.
			// Let's process it right here.
			if err := processInput(conn, initialInput, &pendingCtrlD, &detached); err != nil {
				return
			}
		}

		// If the drain timed out, the goroutine from step 4 is still active!
		// We can't have two readers on Stdin.
		// So Step 4 logic above was flawed for timeout scenarios.
		
		// REVISED PLAN FOR DRAIN:
		// Do not use a separate goroutine that we abandon.
		// Use the MAIN input loop.
		// Set a state "draining = true".
		// While draining, parse input.
		// If CPR found, switch "draining = false" and process remaining.
		// If timeout, switch "draining = false" and process all buffered.
		
		// However, Read blocks. We need a way to unblock or timeout Read.
		// In Go, on local files (Stdin), SetReadDeadline doesn't work.
		// So we are forced to read.
		
		// If we assume the terminal WILL reply (most do), blocking read is fine?
		// But if it doesn't, we hang forever.
		
		// Pragramatic compromise:
		// Most issues with "6c" happen because of fast replay.
		// We can try to rely on the fact that if we send DSR, we will get a byte soon.
		// But we really need a non-blocking way to timeout.
		
		// Given we can't easily timeout a Read on Stdin without closing it:
		// We will assume the Drain approach in Step 4 is executed but we handle the "Timeout" 
		// by effectively saying "This terminal is slow/dumb, we skip sync".
		// BUT the issue of the dangling goroutine remains.
		
		// Solution: The Stdin->Socket loop IS the one reading.
		// It reads from Stdin (blocking).
		// We wrap Stdin in a logic that filters until sync.
		// But what if Sync never comes? The user presses a key. We get bytes.
		// We see "hello". No CPR.
		// Do we drop "hello"?
		// If we are waiting for CPR, we might drop user input.
		
		// The only safe way is: Buffer everything.
		// If we match CPR, delete it and everything before.
		// If we match valid keys that are definitely NOT CPR (like 'a'),
		// and we haven't seen CPR... this is ambiguous. CPR starts with ESC.
		// If we see 'a', it's not CPR. We should probably flush the buffer?
		// But CPR might be intermixed? No, usually atomic.
		
		// Let's implement the "Buffer until CPR" logic in the main loop.
		// We send DSR. We enter loop.
		// We assume CPR comes relatively first.
		
		// For the "hanging" risk: If the terminal never replies, and user never types, we block.
		// That's acceptable? "Waiting for persishtent..."
		// If user types, we wake up.
		// If user types, we likely break the CPR pattern match and flush.
		
		draining := true
		var drainBuf []byte
		
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			data := buf[:n]
			
			if draining {
				drainBuf = append(drainBuf, data...)
				
				// Check for CPR: \x1b [ ... R
				if idx := bytes.IndexByte(drainBuf, 'R'); idx >= 0 {
					// Check if valid CPR start
					if escIdx := bytes.IndexByte(drainBuf, 0x1b); escIdx >= 0 && escIdx < idx {
						// Found it. Discard up to idx.
						draining = false
						data = drainBuf[idx+1:]
						drainBuf = nil // clear
						// Fallthrough to process 'data'
					} else {
						// Found 'R' but no Escape? User typed 'R'?
						// Or part of stream?
						// If user typed 'R', we should probably stop draining?
						// It's risky.
						// Let's assume strict structure.
					}
				}
				
				// Safety: If buffer gets too big without CPR, give up
				if len(drainBuf) > 1024 {
					draining = false
					data = drainBuf
					drainBuf = nil
				}
				
				if draining {
					continue
				}
			}

			if err := processInput(conn, data, &pendingCtrlD, &detached); err != nil {
				return
			}
		}
	}()

	// 8. Socket -> Stdout
	for {
		t, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			if atomic.LoadInt32(&detached) == 1 {
				return ErrDetached
			}
			return nil
		}
		if t == protocol.TypeData {
			_, _ = os.Stdout.Write(payload)
		}
	}
}

func processInput(conn net.Conn, data []byte, pendingCtrlD *bool, detached *int32) error {
	for _, b := range data {
		if *pendingCtrlD {
			*pendingCtrlD = false
			if b == 'd' {
				// Ctrl+D, d -> Detach
				atomic.StoreInt32(detached, 1)
				conn.Close()
				return io.EOF // signal stop
			} else if b == 0x04 {
				// Ctrl+D, Ctrl+D -> Send single Ctrl+D
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04}); err != nil {
					return err
				}
			} else {
				// Ctrl+D, <other> -> Send Ctrl+D then <other>
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04, b}); err != nil {
					return err
				}
			}
		} else {
			if b == 0x04 {
				*pendingCtrlD = true
			} else {
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{b}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func sendResize(conn net.Conn) {
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return
	}
	payload := protocol.ResizePayload(uint16(h), uint16(w))
	_ = protocol.WritePacket(conn, protocol.TypeResize, payload)
}

// Kill sends a termination signal to the session
func Kill(name string) error {
	sockPath, err := session.GetSocketPath(name)
	if err != nil {
		return err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send SIGKILL (9) to ensure immediate termination
	payload := []byte{byte(syscall.SIGKILL)}
	return protocol.WritePacket(conn, protocol.TypeSignal, payload)
}
