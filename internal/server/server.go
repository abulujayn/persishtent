package server

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"persishtent/internal/config"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

type Server struct {
	Name    string
	Cmd     *exec.Cmd
	Master  net.Conn
	Clients map[net.Conn]struct{}
	Lock    sync.Mutex
}

// Run starts the session server. It blocks until the shell process exits.
func Run(name string, sockPath string, logPath string, customCmd string) error {
	// 1. Setup Log
	if logPath == "" {
		var err error
		logPath, err = session.GetLogPath(name)
		if err != nil {
			return err
		}
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	// 1.5 Setup SSH Agent symlink
	sshSymlink, _ := session.GetSSHSockPath(name)
	currentSSH := os.Getenv("SSH_AUTH_SOCK")
	if currentSSH != "" {
		_ = os.Remove(sshSymlink)
		_ = os.Symlink(currentSSH, sshSymlink)
	}

	// 2. Setup PTY
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}
	
	var cmd *exec.Cmd
	if customCmd != "" {
		shellPath := "/bin/sh"
		if _, err := exec.LookPath("bash"); err == nil {
			shellPath = "bash"
		}
		cmd = exec.Command(shellPath, "-c", customCmd)
	} else {
		cmd = exec.Command(shell)
	}
	
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "PERSISHTENT_SESSION="+name)
	
	// Inject prompt prefix
	promptPrefix := fmt.Sprintf("%s:%s ", config.Global.PromptPrefix, name)
	ps1 := os.Getenv("PS1")
	if ps1 == "" {
		// Default prompts often look like this
		ps1 = "[\\u@\\h \\W]\\$ "
	}
	cmd.Env = append(cmd.Env, "PS1="+promptPrefix+ps1)

	if currentSSH != "" {
		// Point the child to the stable symlink
		cmd.Env = append(cmd.Env, "SSH_AUTH_SOCK="+sshSymlink)
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = ptmx.Close() }()

	// 2.5 Write Info
	infoCmd := customCmd
	if infoCmd == "" {
		infoCmd = shell
	}
	_ = session.WriteInfo(session.Info{
		Name:      name,
		PID:       cmd.Process.Pid,
		Command:   infoCmd,
		LogPath:   logPath,
		StartTime: time.Now(),
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
	defer func() {
		_ = l.Close()
		_ = os.Remove(sockPath)
		infoPath, _ := session.GetInfoPath(name)
		_ = os.Remove(infoPath)
	}()
	_ = os.Chmod(sockPath, 0600)

	srv := &Server{
		Name:    name,
		Cmd:     cmd,
		Clients: make(map[net.Conn]struct{}),
	}

	maxLogSize := int64(config.Global.LogRotationSizeMB) * 1024 * 1024
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
			
			if logSize > maxLogSize {
				_ = logFile.Close()
				
				// Find highest index
				files, _ := session.GetLogFiles(name)
				maxIdx := 0
				prefix := logPath + "."
				for _, f := range files {
					if len(f) > len(prefix) {
						idx, _ := strconv.Atoi(f[len(prefix):])
						if idx > maxIdx {
							maxIdx = idx
						}
					}
				}

				nextIdx := maxIdx + 1
				_ = os.Rename(logPath, fmt.Sprintf("%s.%d", logPath, nextIdx))
				
				// Cleanup old rotations if limit exceeded
				if len(files) >= config.Global.MaxLogRotations {
					// files[0] is the oldest
					_ = os.Remove(files[0])
				}

				newFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
				if err == nil {
					logFile = newFile
					logSize = 0
				} else {
					// Fallback: try to reopen original if rename failed or something
					logFile, _ = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
				}
			}

			wn, err := logFile.Write(data)
			if err == nil {
				logSize += int64(wn)
			}
			srv.broadcast(data)
		}
		_ = l.Close()
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
	return err
}

func (s *Server) broadcast(data []byte) {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	for conn := range s.Clients {
		err := protocol.WritePacket(conn, protocol.TypeData, data)
		if err != nil {
			_ = conn.Close()
			delete(s.Clients, conn)
		}
	}
}

func (s *Server) handleClient(conn net.Conn, ptmx *os.File) {

	// First packet MUST be TypeMode

	t, payload, err := protocol.ReadPacket(conn)

	if err != nil || t != protocol.TypeMode || len(payload) < 1 {

		_ = conn.Close()

		return

	}



	isReadOnly := payload[0] == 0x01



	s.Lock.Lock()

		if !isReadOnly {

			// New Master client: kick existing Master

			if s.Master != nil {

				_ = protocol.WritePacket(s.Master, protocol.TypeKick, nil)

				_ = s.Master.Close()

			}

			s.Master = conn

		}

	

	s.Clients[conn] = struct{}{}

	s.Lock.Unlock()



	defer func() {

		s.Lock.Lock()

		delete(s.Clients, conn)

		if s.Master == conn {

			s.Master = nil

		}

		s.Lock.Unlock()

		_ = conn.Close()

	}()



	for {

		t, payload, err := protocol.ReadPacket(conn)

		if err != nil {

			return

		}



		// Only Master can send Data, Resize, or Signal

		if isReadOnly {

			continue

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

				case protocol.TypeEnv:

					// payload contains key=value

					if bytes.HasPrefix(payload, []byte("SSH_AUTH_SOCK=")) {

						newSock := string(payload[len("SSH_AUTH_SOCK="):])

						sshSymlink, _ := session.GetSSHSockPath(s.Name)

						_ = os.Remove(sshSymlink)

						_ = os.Symlink(newSock, sshSymlink)

					}

				}

			}

		}

		
