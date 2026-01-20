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
func Attach(name string, sockPath string, replay bool, readOnly bool) error {
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

	// 2. Raw Mode
	// We enter raw mode early to handle log replay correctly and drain input
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// 3. Replay Log
	if replay {
		logPath, err := session.GetLogPath(name)
		if err == nil {
			f, err := os.Open(logPath)
			if err == nil {
				_, _ = io.Copy(os.Stdout, f)
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
	timeout := time.After(250 * time.Millisecond)

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
			
			// Check for CPR: \x1b [ ... R
			if idx := bytes.IndexByte(drainBuf, 'R'); idx >= 0 {
				if escIdx := bytes.LastIndexByte(drainBuf[:idx], 0x1b); escIdx >= 0 {
					// Found CPR, discard it and everything before
					remainder := drainBuf[idx+1:]
					if len(remainder) > 0 {
						if err := processInput(conn, remainder, &pendingCtrlD, &detached, readOnly); err != nil {
							return nil
						}
					}
					break DrainLoop
				}
			}
			// Safety limit
			if len(drainBuf) > 2048 {
				if err := processInput(conn, drainBuf, &pendingCtrlD, &detached, readOnly); err != nil {
					return nil
				}
				break DrainLoop
			}
		case <-timeout:
			// Timeout: Assume no CPR coming, process everything buffered
			if len(drainBuf) > 0 {
				if err := processInput(conn, drainBuf, &pendingCtrlD, &detached, readOnly); err != nil {
					return nil
				}
			}
			break DrainLoop
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
				// Exit alternate buffer and clear screen
				_, _ = os.Stdout.Write([]byte("\x1b[?1049l\x1b[H\x1b[2J"))
				return ErrDetached
			}
			return nil
		}
		switch t {
		case protocol.TypeData:
			_, _ = os.Stdout.Write(payload)
		case protocol.TypeKick:
			// Restore terminal state
			_, _ = os.Stdout.Write([]byte("\x1b[?1049l\x1b[H\x1b[2J"))
			return ErrKicked
		}
	}
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
