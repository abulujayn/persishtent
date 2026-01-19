package server

import (
	"io"
	"net"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

type Server struct {
	Name    string
	Clients map[net.Conn]struct{}
	Lock    sync.Mutex
}

// Run starts the session server. It blocks until the shell process exits.
func Run(name string) error {
	// 1. Setup Log
	logPath, err := session.GetLogPath(name)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	// 2. Setup PTY
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}
	// Fallback if bash not found?
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}

	cmd := exec.Command(shell)
	// Ensure TERM is set for proper shell behavior
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	// 3. Setup Socket
	sockPath, err := session.GetSocketPath(name)
	if err != nil {
		return err
	}
	// Attempt to remove existing socket.
	// If it's in use, Listen will fail (or we replace it).
	// Realistically, we should check liveness before calling Run.
	_ = os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	defer l.Close()

	srv := &Server{
		Name:    name,
		Clients: make(map[net.Conn]struct{}),
	}

	// 4. Output Loop: PTY -> Log + Clients
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				if err != io.EOF {
					// We might log this internal error somewhere
				}
				break
			}
			data := buf[:n]

			// Write to log
			if _, err := logFile.Write(data); err != nil {
				// Log write error
			}

			// Broadcast to clients
			srv.broadcast(data)
		}
		// When PTY closes, we close the listener to unblock Accept()
		l.Close()
	}()

	// 5. Accept Clients
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go srv.handleClient(conn, ptmx)
		}
	}()

	// 6. Wait for process to exit
	err = cmd.Wait()
	
	// Cleanup socket immediately
	_ = os.Remove(sockPath)
	
	return err
}

func (s *Server) broadcast(data []byte) {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	
	for conn := range s.Clients {
		// If write fails, we assume client is dead.
		// handleClient will likely detect this on Read as well, or we force close.
		err := protocol.WritePacket(conn, protocol.TypeData, data)
		if err != nil {
			conn.Close()
			delete(s.Clients, conn)
		}
	}
}

func (s *Server) handleClient(conn net.Conn, ptmx *os.File) {
	s.Lock.Lock()
	s.Clients[conn] = struct{}{}
	s.Lock.Unlock()

	defer func() {
		s.Lock.Lock()
		delete(s.Clients, conn)
		s.Lock.Unlock()
		conn.Close()
	}()

	// Read loop
	for {
		t, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			return
		}

		switch t {
		case protocol.TypeData:
			if _, err := ptmx.Write(payload); err != nil {
				return
			}
		case protocol.TypeResize:
			rows, cols := protocol.DecodeResizePayload(payload)
			ws := &pty.Winsize{Rows: rows, Cols: cols}
			_ = pty.Setsize(ptmx, ws)
		}
	}
}
