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
var ErrKicked = errors.New("kicked by another session")

// Attach connects to an existing session
func Attach(name string, sockPath string, replay bool, readOnly bool, tail int) error {
	var err error
	if sockPath == "" {
		sockPath, err = session.GetSocketPath(name)
		if err != nil {
			return err
		}
	}

	// 1. Connect
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// 1.5 Send Mode
	mode := []byte{0x00} // Master
	if readOnly {
		mode = []byte{0x01} // Read-only
	}
	if err := protocol.WritePacket(conn, protocol.TypeMode, mode); err != nil {
		return err
	}

	// 1.6 Sync Env
	currentSSH := os.Getenv("SSH_AUTH_SOCK")
	if currentSSH != "" {
		_ = protocol.WritePacket(conn, protocol.TypeEnv, []byte("SSH_AUTH_SOCK="+currentSSH))
	}

	// 2. Raw Mode
	// We enter raw mode early to handle log replay correctly and drain input
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

			// 3. Replay Log

			if replay {

				logFiles, _ := session.GetLogFiles(name)

				for _, lp := range logFiles {

					f, err := os.Open(lp)

					if err == nil {

						if tail > 0 {

							replayTail(os.Stdout, f, tail)

						} else {

							_, _ = io.Copy(os.Stdout, f)

						}

						_ = f.Close()

					}

				}

			}

		
			    // 4. Sync Terminal (Drain responses)
		    // Send Device Status Report (DSR) request.
		    _, _ = os.Stdout.Write([]byte("\x1b[6n"))
		
		    // We use a dedicated channel for Stdin to allow select with timeout
		    stdinCh := make(chan []byte)
		    go func() {
		        buf := make([]byte, 1024)
		        for {
		            n, err := os.Stdin.Read(buf)
		            if n > 0 {
		                tmp := make([]byte, n)
		                copy(tmp, buf[:n])
		                stdinCh <- tmp
		            }
		            if err != nil {
		                close(stdinCh)
		                return
		            }
		        }
		    }()
		
		    // Drain Phase
		    var drainBuf []byte
		    deadline := time.After(1000 * time.Millisecond)
		    inactivity := time.NewTimer(250 * time.Millisecond)
		    defer inactivity.Stop()
		
		    var pendingCtrlD bool
		    var detached int32
		
		DrainLoop:
		    for {
		        select {
		        case chunk, ok := <-stdinCh:
		            if !ok {
		                return nil // Stdin closed
		            }
		            drainBuf = append(drainBuf, chunk...)
		
		            for {
		                seqLen := matchTerminalResponse(drainBuf)
		                if seqLen <= 0 {
		                    break
		                }
		
		                // Found a response!
		                // 1. Forward anything BEFORE the sequence (unlikely but possible)
		                escIdx := bytes.Index(drainBuf, []byte("\x1b"))
		                if escIdx > 0 {
		                    if err := processInput(conn, drainBuf[:escIdx], &pendingCtrlD, &detached, readOnly); err != nil {
		                        return nil
		                    }
		                }
		
		                // 2. Swallow the sequence
		                drainBuf = drainBuf[escIdx+seqLen:]
		
		                // Reset inactivity timer
		                if !inactivity.Stop() {
		                    select {
		                    case <-inactivity.C:
		                    default:
		                    }
		                }
		                inactivity.Reset(100 * time.Millisecond)
		            }
		
		            // If we have data that is definitely NOT part of an escape sequence,
		            // we can forward it.
		            if len(drainBuf) > 0 && !bytes.Contains(drainBuf, []byte("\x1b")) {
		                if err := processInput(conn, drainBuf, &pendingCtrlD, &detached, readOnly); err != nil {
		                    return nil
		                }
		                drainBuf = nil
		            }
		
		            // Safety limit
		            if len(drainBuf) > 4096 {
		                if err := processInput(conn, drainBuf, &pendingCtrlD, &detached, readOnly); err != nil {
		                    return nil
		                }
		                drainBuf = nil
		                break DrainLoop
		            }
		        case <-inactivity.C:
		            break DrainLoop
		        case <-deadline:
		            break DrainLoop
		        }
		    }
		
		    // Flush remaining
		    if len(drainBuf) > 0 {
		        if err := processInput(conn, drainBuf, &pendingCtrlD, &detached, readOnly); err != nil {
		            return nil
		        }
		    }
		
		    // 5. Initial Resize
		    if !readOnly {
		        sendResize(conn)
		    }
		
		    // 6. Handle Resize Signals
		    if !readOnly {
		        sigCh := make(chan os.Signal, 1)
		        signal.Notify(sigCh, syscall.SIGWINCH)
		        go func() {
		            for range sigCh {
		                sendResize(conn)
		            }
		        }()
		    }
		
		    // 7. Stdin -> Socket (Main Loop)
		    // We continue reading from stdinCh
		    go func() {
		        for chunk := range stdinCh {
		            if err := processInput(conn, chunk, &pendingCtrlD, &detached, readOnly); err != nil {
		                return
		            }
		        }
		    }()
		
		    // 8. Socket -> Stdout
		    for {
		        t, payload, err := protocol.ReadPacket(conn)
		        if err != nil {
		            if atomic.LoadInt32(&detached) == 1 {
		                restoreTerminal()
		                return ErrDetached
		            }
		            return nil
		        }
		        switch t {
		        case protocol.TypeData:
		            _, _ = os.Stdout.Write(payload)
		        case protocol.TypeKick:
		            restoreTerminal()
		            return ErrKicked
		        }
		    }
		}
		
		// restoreTerminal sends escape sequences to reset terminal modes that might have been
		// enabled by applications inside the session (e.g. alternate buffer, mouse tracking).
		func restoreTerminal() {
		    // \x1b[m       : Reset colors/attributes
		    // \x1b[?1049l : Exit alternate buffer
		    // \x1b[?1000l... : Disable mouse tracking
		    // \x1b[?2004l : Disable bracketed paste
		    // \x1b[?25h   : Show cursor
		    // \x1b[H\x1b[2J : Clear screen
		    _, _ = os.Stdout.Write([]byte("\x1b[m\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?25h\x1b[H\x1b[2J"))
		}
		
		func replayTail(w io.Writer, f *os.File, n int) {
		
			// Minimal backward scanning tail
		
			stat, _ := f.Stat()
		
			size := stat.Size()
		
			if size == 0 {
		
				return
		
			}
		
		
		
			bufSize := int64(4096)
		
			if bufSize > size {
		
				bufSize = size
		
			}
		
		
		
			buf := make([]byte, bufSize)
		
			offset := size - bufSize
		
			lines := 0
		
			var finalData []byte
		
		
		
			for offset >= 0 {
		
				_, _ = f.Seek(offset, 0)
		
				_, _ = io.ReadFull(f, buf)
		
				
		
				for i := len(buf) - 1; i >= 0; i-- {
		
					if buf[i] == '\n' {
		
						// Skip the very last character if it's a newline
		
						if offset+int64(i) == size-1 {
		
							continue
		
						}
		
						lines++
		
						if lines >= n {
		
							finalData = append(buf[i+1:], finalData...)
		
							_, _ = w.Write(finalData)
		
							return
		
						}
		
					}
		
				}
		
				finalData = append(buf, finalData...)
		
				if offset == 0 {
		
					break
		
				}
		
				offset -= bufSize
		
				if offset < 0 {
		
					bufSize += offset
		
					offset = 0
		
				}
		
			}
		
			_, _ = w.Write(finalData)
		
		}
		
		
		
		
func processInput(conn net.Conn, data []byte, pendingCtrlD *bool, detached *int32, readOnly bool) error {
	for _, b := range data {
		if *pendingCtrlD {
			*pendingCtrlD = false
			switch b {
			case 'd':
				// Ctrl+D, d -> Detach
				atomic.StoreInt32(detached, 1)
				_ = conn.Close()
				return io.EOF // signal stop
			case 0x04:
				if readOnly {
					continue
				}
				// Ctrl+D, Ctrl+D -> Send single Ctrl+D
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04}); err != nil {
					return err
				}
			default:
				if readOnly {
					continue
				}
				// Ctrl+D, <other> -> Send Ctrl+D then <other>
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04, b}); err != nil {
					return err
				}
			}
		} else {
			if b == 0x04 {
				*pendingCtrlD = true
			} else {
				if readOnly {
					continue
				}
				if err := protocol.WritePacket(conn, protocol.TypeData, []byte{b}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// matchTerminalResponse returns the length of the first terminal response sequence
// starting at the first ESC in data. Returns 0 if no complete response is found.
func matchTerminalResponse(data []byte) int {
	escIdx := bytes.Index(data, []byte("\x1b"))
	if escIdx < 0 {
		return 0
	}
	remaining := data[escIdx:]
	if len(remaining) < 2 {
		return 0
	}

	switch remaining[1] {
	case '[': // CSI
		for i := 2; i < len(remaining); i++ {
			b := remaining[i]
			if b >= 0x40 && b <= 0x7E {
				return i + 1
			}
		}
	case ']': // OSC
		// OSC sequences end with BEL (0x07) or ST (ESC \)
		for i := 2; i < len(remaining); i++ {
			if remaining[i] == 0x07 {
				return i + 1
			}
			if remaining[i] == '\\' && remaining[i-1] == 0x1b {
				return i + 1
			}
		}
	case 'P', '_', '^', 'k': // DCS, APC, PM, Title
		// These typically end with ST (ESC \)
		for i := 2; i < len(remaining); i++ {
			if remaining[i] == '\\' && remaining[i-1] == 0x1b {
				return i + 1
			}
		}
	default:
		// Other ESC sequences are usually 2 bytes
		return 2
	}
	return 0
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
func Kill(name string, sockPath string) error {
	var err error
	if sockPath == "" {
		sockPath, err = session.GetSocketPath(name)
		if err != nil {
			return err
		}
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// Send Mode (Master mode to ensure signal is processed)
	if err := protocol.WritePacket(conn, protocol.TypeMode, []byte{0x00}); err != nil {
		return err
	}

	// Send SIGKILL (9) to ensure immediate termination
	payload := []byte{byte(syscall.SIGKILL)}
	return protocol.WritePacket(conn, protocol.TypeSignal, payload)
}
