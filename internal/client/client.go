package client

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
	"persishtent/internal/config"
	"persishtent/internal/protocol"
	"persishtent/internal/session"
)

var ErrDetached = errors.New("detached")
var ErrKicked = errors.New("kicked by another session")

// SessionClient handles the client-side session logic.
type SessionClient struct {
	Conn       net.Conn
	Name       string
	DetachKey  byte
	ReadOnly   bool
	
	stdinCh    chan []byte
	
pendingPrefix bool
detached      int32 // atomic
}

func NewSessionClient(name string, detachKey byte, readOnly bool) *SessionClient {
	return &SessionClient{
		Name:      name,
		DetachKey: detachKey,
		ReadOnly:  readOnly,
		stdinCh:   make(chan []byte),
	}
}

func (c *SessionClient) Connect(sockPath string) error {
	var err error
	if sockPath == "" {
		sockPath, err = session.GetSocketPath(c.Name)
		if err != nil {
			return err
		}
	}
	c.Conn, err = net.Dial("unix", sockPath)
	return err
}

func (c *SessionClient) Handshake() error {
	// Send Mode
	mode := []byte{protocol.ModeMaster}
	if c.ReadOnly {
		mode = []byte{protocol.ModeReadOnly}
	}
	if err := protocol.WritePacket(c.Conn, protocol.TypeMode, mode); err != nil {
		return err
	}

	// Sync Env
	currentSSH := os.Getenv("SSH_AUTH_SOCK")
	if currentSSH != "" {
		_ = protocol.WritePacket(c.Conn, protocol.TypeEnv, []byte("SSH_AUTH_SOCK="+currentSSH))
	}
	return nil
}

func (c *SessionClient) processInput(data []byte) error {
	for _, b := range data {
		if c.pendingPrefix {
			c.pendingPrefix = false
			switch b {
			case 'd':
				// Prefix, d -> Detach
				atomic.StoreInt32(&c.detached, 1)
				_ = c.Conn.Close()
				return io.EOF // signal stop
			case c.DetachKey:
				if c.ReadOnly {
					continue
				}
				// Prefix, Prefix -> Send single Prefix
				if err := protocol.WritePacket(c.Conn, protocol.TypeData, []byte{c.DetachKey}); err != nil {
					return err
				}
			default:
				if c.ReadOnly {
					continue
				}
				// Prefix, <other> -> Send Prefix then <other>
				if err := protocol.WritePacket(c.Conn, protocol.TypeData, []byte{c.DetachKey, b}); err != nil {
					return err
				}
			}
		} else {
			if b == c.DetachKey {
				c.pendingPrefix = true
			} else {
				if c.ReadOnly {
					continue
				}
				if err := protocol.WritePacket(c.Conn, protocol.TypeData, []byte{b}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (c *SessionClient) DrainInput() error {
	// Send Device Status Report (DSR) request.
	_, _ = os.Stdout.Write([]byte("\x1b[6n"))

	// Start Stdin reader
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				tmp := make([]byte, n)
				copy(tmp, buf[:n])
				c.stdinCh <- tmp
			}
			if err != nil {
				close(c.stdinCh)
				return
			}
		}
	}()

	// Drain Phase
	var drainBuf []byte
	deadline := time.After(1000 * time.Millisecond)
	inactivity := time.NewTimer(250 * time.Millisecond)
	defer inactivity.Stop()

DrainLoop:
	for {
		select {
		case chunk, ok := <-c.stdinCh:
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
				// 1. Forward anything BEFORE the sequence
				escIdx := bytes.Index(drainBuf, []byte("\x1b"))
				if escIdx > 0 {
					if err := c.processInput(drainBuf[:escIdx]); err != nil {
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

			// Forward non-escape data
			if len(drainBuf) > 0 && !bytes.Contains(drainBuf, []byte("\x1b")) {
				if err := c.processInput(drainBuf); err != nil {
					return nil
				}
				drainBuf = nil
			}

			// Safety limit
			if len(drainBuf) > 4096 {
				if err := c.processInput(drainBuf); err != nil {
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
		if err := c.processInput(drainBuf); err != nil {
			return nil
		}
	}
	return nil
}

func (c *SessionClient) Stream() error {
	// 5. Initial Resize
	if !c.ReadOnly {
		sendResize(c.Conn)
	}

	// 6. Handle Resize Signals
	if !c.ReadOnly {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGWINCH)
		go func() {
			for range sigCh {
				sendResize(c.Conn)
			}
		}()
	}

	// 7. Stdin -> Socket (Main Loop)
	// We continue reading from stdinCh
	go func() {
		for chunk := range c.stdinCh {
			if err := c.processInput(chunk); err != nil {
				return
			}
		}
	}()

	// 8. Socket -> Stdout
	for {
		t, payload, err := protocol.ReadPacket(c.Conn)
		if err != nil {
			if atomic.LoadInt32(&c.detached) == 1 {
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

// Attach connects to an existing session
func Attach(name string, sockPath string, replay bool, readOnly bool, tail int) error {
	detachByte := parseDetachKey(config.Global.DetachKey)
	client := NewSessionClient(name, detachByte, readOnly)

	if err := client.Connect(sockPath); err != nil {
		return err
	}
	defer func() { _ = client.Conn.Close() }()

	if err := client.Handshake(); err != nil {
		return err
	}

	// Raw Mode
	// We enter raw mode early to handle log replay correctly and drain input
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	// Replay Log
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

	if err := client.DrainInput(); err != nil {
		return err
	}

	return client.Stream()
}

// restoreTerminal sends escape sequences to reset terminal modes
func restoreTerminal() {
	_, _ = os.Stdout.Write([]byte("\x1b[m\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?25h\x1b[H\x1b[2J"))
}

func replayTail(w io.Writer, f *os.File, n int) {
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

func parseDetachKey(key string) byte {
	key = strings.ToLower(key)
	if len(key) >= 6 && key[:5] == "ctrl-" {
		c := key[5]
		if c >= 'a' && c <= 'z' {
			return byte(c - 'a' + 1)
		}
		switch c {
		case '[':
			return 27
		case '\\':
			return 28
		case ']':
			return 29
		case '^':
			return 30
		case '_':
			return 31
		}
	}
	return 0x04 // default ctrl-d
}

// matchTerminalResponse returns the length of the first terminal response sequence
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
		for i := 2; i < len(remaining); i++ {
			if remaining[i] == 0x07 {
				return i + 1
			}
			if remaining[i] == '\\' && remaining[i-1] == 0x1b {
				return i + 1
			}
		}
	case 'P', '_', '^', 'k': // DCS, APC, PM, Title
		for i := 2; i < len(remaining); i++ {
			if remaining[i] == '\\' && remaining[i-1] == 0x1b {
				return i + 1
			}
		}
	default:
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
	if err := protocol.WritePacket(conn, protocol.TypeMode, []byte{protocol.ModeMaster}); err != nil {
		return err
	}

	// Send SIGKILL (9) to ensure immediate termination
	payload := []byte{byte(syscall.SIGKILL)}
	return protocol.WritePacket(conn, protocol.TypeSignal, payload)
}
