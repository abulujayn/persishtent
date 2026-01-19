package server

import (
	"net"
	"os"
	"os/exec"
	"os/signal"
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
func Run(name string, sockPath string, customCmd string) error {
	// 1. Setup Log
	logPath, err := session.GetLogPath(name)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	// 2. Setup PTY
	execCmd := customCmd
	if execCmd == "" {
		execCmd = os.Getenv("SHELL")
		if execCmd == "" {
			execCmd = "bash"
		}
	}
	
	// Split custom command into args for exec
	// Simple split by space for now. For complex commands, user should use a shell wrapper.
	cmdArgs := []string{"-c", execCmd}
	shellPath := "/bin/sh"
	if _, err := exec.LookPath("bash"); err == nil {
		shellPath = "bash"
	}

	cmd := exec.Command(shellPath, cmdArgs...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "PERSISHTENT_SESSION="+name)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	// 2.5 Write Info
	_ = session.WriteInfo(session.Info{
		Name:    name,
		PID:     cmd.Process.Pid,
		Command: execCmd,
	})

	// 3. Setup Socket
	if sockPath == "" {
		sockPath, err = session.GetSocketPath(name)
		if err != nil {
			return err
		}
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

	const maxLogSize = 1024 * 1024 // 1MB
	var logSize int64

	// 4. Output Loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				break
			}
			data := buf[:n]
			
			// Simple log truncation logic: 
			// If we hit maxLogSize, we reset and keep only the last chunk? 
			// No, that loses context. 
			// Better: if file > maxLogSize, truncate the beginning.
			// But Go doesn't have an easy "truncate head" for files.
			// Minimal approach: If > maxLogSize, wipe it. 
			// (User specified "clean and minimal", let's keep it simple for now).
			if logSize > maxLogSize {
				_ = logFile.Truncate(0)
				_, _ = logFile.Seek(0, 0)
				logSize = 0
			}

			wn, err := logFile.Write(data)
			if err == nil {
				logSize += int64(wn)
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

	// 5.5 Handle Signals for graceful cleanup
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		_ = cmd.Process.Kill()
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