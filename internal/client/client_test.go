package client

import (
	"bytes"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"persishtent/internal/protocol"
)

type mockConn struct {
	out    bytes.Buffer
	closed bool
}

func (m *mockConn) Read(b []byte) (n int, err error)         { return 0, io.EOF }
func (m *mockConn) Write(b []byte) (n int, err error)        { return m.out.Write(b) }
func (m *mockConn) Close() error                             { m.closed = true; return nil }
func (m *mockConn) LocalAddr() net.Addr                      { return nil }
func (m *mockConn) RemoteAddr() net.Addr                     { return nil }
func (m *mockConn) SetDeadline(t time.Time) error            { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error        { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error       { return nil }

const defaultDetachByte = 0x04

func TestProcessInput_Normal(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	input := []byte("h")
	err := processInput(conn, input, &pendingCtrlD, &detached, false, defaultDetachByte)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Header: Type(1) + Len(4) + Data(1)
	// TypeData = 1
	// Len = 1
	expectedHeader := []byte{byte(protocol.TypeData), 0, 0, 0, 1}
	if !bytes.HasPrefix(conn.out.Bytes(), expectedHeader) {
		t.Fatalf("Header mismatch. Got %x, want %x", conn.out.Bytes()[:5], expectedHeader)
	}
	if conn.out.Len() != 6 {
		t.Errorf("Expected 6 bytes output, got %d", conn.out.Len())
	}
}

func TestProcessInput_Detach(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	// Ctrl+D (0x04) then 'd'
	input := []byte{0x04}
	err := processInput(conn, input, &pendingCtrlD, &detached, false, defaultDetachByte)
	if err != nil {
		t.Fatalf("Unexpected error on Ctrl+D: %v", err)
	}
	if !pendingCtrlD {
		t.Error("pendingCtrlD should be true")
	}
	if conn.out.Len() != 0 {
		t.Error("Should not send Ctrl+D yet")
	}

	input = []byte{'d'}
	err = processInput(conn, input, &pendingCtrlD, &detached, false, defaultDetachByte)
	if err != io.EOF {
		t.Errorf("Expected EOF (stop signal), got %v", err)
	}
	if atomic.LoadInt32(&detached) != 1 {
		t.Error("Detached flag not set")
	}
	if !conn.closed {
		t.Error("Connection not closed")
	}
}

func TestProcessInput_LiteralCtrlD(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	// Ctrl+D, Ctrl+D -> Send single Ctrl+D
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, false, defaultDetachByte)
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, false, defaultDetachByte)
	
	// Should have sent 1 packet with 0x04
	// Header(5) + Data(1) = 6 bytes
	if conn.out.Len() != 6 {
		t.Errorf("Expected 6 bytes, got %d", conn.out.Len())
	}
	data := conn.out.Bytes()
	if data[5] != 0x04 {
		t.Errorf("Expected 0x04 data, got %x", data[5])
	}
}

func TestProcessInput_Passthrough(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	// Ctrl+D, 'x' -> Send Ctrl+D then 'x' in ONE packet
	_ = processInput(conn, []byte{0x04, 'x'}, &pendingCtrlD, &detached, false, defaultDetachByte)
	
	// Header(5) + Data(2) = 7 bytes
	if conn.out.Len() != 7 {
		t.Errorf("Expected 7 bytes, got %d", conn.out.Len())
	}
	
data := conn.out.Bytes()
	// Data starts at 5
	if data[5] != 0x04 {
		t.Errorf("Expected 0x04, got %x", data[5])
	}
	if data[6] != 'x' {
		t.Errorf("Expected 'x', got %x", data[6])
	}
}

func TestProcessInput_ReadOnly(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	// Normal input should be ignored
	_ = processInput(conn, []byte("hello"), &pendingCtrlD, &detached, true, defaultDetachByte)
	if conn.out.Len() != 0 {
		t.Errorf("Expected 0 bytes output in read-only mode, got %d", conn.out.Len())
	}

	// Detach sequence should STILL work
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, true, defaultDetachByte)
	if !pendingCtrlD {
		t.Error("pendingCtrlD should be true in read-only mode")
	}
	err := processInput(conn, []byte{'d'}, &pendingCtrlD, &detached, true, defaultDetachByte)
	if err != io.EOF {
		t.Errorf("Expected EOF on detach in read-only mode, got %v", err)
	}
	if atomic.LoadInt32(&detached) != 1 {
		t.Error("Detached flag not set in read-only mode")
	}
}

func TestProcessInput_CustomKey(t *testing.T) {
	conn := &mockConn{}
	var pendingPrefix bool
	var detached int32
	
	// Use Ctrl+A (0x01) as detach key
	detachByte := byte(0x01)

	// Ctrl+A, d -> Detach
	err := processInput(conn, []byte{0x01}, &pendingPrefix, &detached, false, detachByte)
	if err != nil {
		t.Fatal(err)
	}
	if !pendingPrefix {
		t.Error("Pending prefix should be set for 0x01")
	}
	
	err = processInput(conn, []byte{'d'}, &pendingPrefix, &detached, false, detachByte)
	if err != io.EOF {
		t.Error("Should detach with Ctrl+A, d")
	}
	if atomic.LoadInt32(&detached) != 1 {
		t.Error("Detached flag not set")
	}
}

func TestReplayTail(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		n        int
		expected string
	}{
		{"Empty", "", 5, ""},
		{"SingleLine", "hello", 1, "hello"},
		{"ExactLines", "1\n2\n3\n", 3, "1\n2\n3\n"},
		{"MoreLinesThanAvailable", "1\n2\n", 5, "1\n2\n"},
		{"FewerLinesThanAvailable", "1\n2\n3\n4\n5\n", 2, "4\n5\n"},
		{"LargeContent", func() string {
			var s string
			for i := 0; i < 100; i++ {
				s += "line\n"
			}
			return s
		}(), 5, "line\nline\nline\nline\nline\n"},
		{"NoTrailingNewline", "1\n2\n3", 2, "2\n3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp(t.TempDir(), "tail-test")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tmpFile.WriteString(tt.content); err != nil {
				t.Fatal(err)
			}
			
			var out bytes.Buffer
			replayTail(&out, tmpFile, tt.n)
			if out.String() != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, out.String())
			}
		})
	}
}

func TestParseDetachKey(t *testing.T) {
	tests := []struct {
		input    string
		expected byte
	}{
		{"ctrl-a", 0x01},
		{"ctrl-z", 0x1A},
		{"ctrl-d", 0x04},
		{"ctrl-[", 0x1B},
		{"ctrl-\\", 0x1C},
		{"ctrl-]", 0x1D},
		{"ctrl-^", 0x1E},
		{"ctrl-_", 0x1F},
		{"invalid", 0x04}, // default
		{"", 0x04},        // default
		{"ctrl-A", 0x01}, // case insensitive
	}
	
	for _, tt := range tests {
		got := parseDetachKey(tt.input)
		if got != tt.expected {
			t.Errorf("parseDetachKey(%q) = 0x%x, want 0x%x", tt.input, got, tt.expected)
		}
	}
}