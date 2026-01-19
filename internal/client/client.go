package client

import (
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

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

	// 1. Replay Log
	logPath, err := session.GetLogPath(name)
	if err == nil {
		f, err := os.Open(logPath)
		if err == nil {
			_, _ = io.Copy(os.Stdout, f)
			f.Close()
		}
	}

	// 2. Connect
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	// 3. Raw Mode
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// 4. Initial Resize
	sendResize(conn)

	// 5. Handle Resize Signals
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			sendResize(conn)
		}
	}()

	var detached int32 // 0 = false, 1 = true

	// 6. Stdin -> Socket
	go func() {
		buf := make([]byte, 1024)
		var pendingCtrlD bool

		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				return
			}
			data := buf[:n]

			for _, b := range data {
				if pendingCtrlD {
					pendingCtrlD = false
					if b == 'd' {
						// Ctrl+D, d -> Detach
						atomic.StoreInt32(&detached, 1)
						conn.Close()
						return
					} else if b == 0x04 {
						// Ctrl+D, Ctrl+D -> Send single Ctrl+D
						if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04}); err != nil {
							return
						}
					} else {
						// Ctrl+D, <other> -> Send Ctrl+D then <other>
						if err := protocol.WritePacket(conn, protocol.TypeData, []byte{0x04, b}); err != nil {
							return
						}
					}
				} else {
					if b == 0x04 {
						pendingCtrlD = true
					} else {
						if err := protocol.WritePacket(conn, protocol.TypeData, []byte{b}); err != nil {
							return
						}
					}
				}
			}
		}
	}()

	// 7. Socket -> Stdout
	for {
		t, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			// Check if we detached
			if atomic.LoadInt32(&detached) == 1 {
				return ErrDetached
			}
			// Otherwise, assume server disconnected (process ended)
			return nil
		}
		if t == protocol.TypeData {
			_, _ = os.Stdout.Write(payload)
		}
	}
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
