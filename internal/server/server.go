package server

import (
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

type Server struct {
	Name    string
	Cmd     *exec.Cmd
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
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	// 2. Setup PTY
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}
	if _, err := exec.LookPath(shell); err != nil {
		shell = "sh"
	}

	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	// 2.5 Write Info
	_ = session.WriteInfo(session.Info{
		Name:    name,
		PID:     cmd.Process.Pid,
		Command: shell,
	})

	// 3. Setup Socket
	sockPath, err := session.GetSocketPath(name)
	if err != nil {
		return err
	}
	_ = os.Remove(sockPath)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	defer l.Close()

	srv := &Server{
		Name:    name,
		Cmd:     cmd,
		Clients: make(map[net.Conn]struct{}),
	}

	// 4. Output Loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				break
			}
			data := buf[:n]
			if _, err := logFile.Write(data); err != nil {
				// ignore
			}
			srv.broadcast(data)
		}
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

	// 6. Wait
	err = cmd.Wait()
	_ = os.Remove(sockPath)
	infoPath, _ := session.GetInfoPath(name)
	_ = os.Remove(infoPath)
	return err
}

func (s *Server) broadcast(data []byte) {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	for conn := range s.Clients {
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
		case protocol.TypeSignal:
			if len(payload) > 0 {
				sig := syscall.Signal(payload[0])
				if s.Cmd != nil && s.Cmd.Process != nil {
					_ = s.Cmd.Process.Signal(sig)
				}
			}
		}
	}
}