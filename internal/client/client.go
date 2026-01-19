package client

import (
	"bytes"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

// Attach connects to an existing session
func Attach(name string) error {
	sockPath, err := session.GetSocketPath(name)
	if err != nil {
		return err
	}

	// 1. Replay Log
	// We do this before raw mode to keep it simple, 
	// assuming the log contains necessary escape codes.
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

	// 6. Stdin -> Socket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				// If Stdin fails, we likely want to exit
				return
			}
			data := buf[:n]
			
			// Check for Ctrl+D (0x04) to detach
			if idx := bytes.IndexByte(data, 0x04); idx >= 0 {
				// Send any data before the Ctrl+D
				if idx > 0 {
					_ = protocol.WritePacket(conn, protocol.TypeData, data[:idx])
				}
				// Close connection to trigger exit in main loop
				conn.Close()
				return
			}

			err = protocol.WritePacket(conn, protocol.TypeData, data)
			if err != nil {
				return
			}
		}
	}()

	// 7. Socket -> Stdout
	for {
		t, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			// Server disconnected or error
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

	// Send SIGTERM (15)
	payload := []byte{byte(syscall.SIGTERM)}
	return protocol.WritePacket(conn, protocol.TypeSignal, payload)
}
