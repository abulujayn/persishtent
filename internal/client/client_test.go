package client

import (
	"bytes"
	"io"
	"net"
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

func TestProcessInput_Normal(t *testing.T) {
	conn := &mockConn{}
	var pendingCtrlD bool
	var detached int32

	input := []byte("h")
	err := processInput(conn, input, &pendingCtrlD, &detached, false)
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
	err := processInput(conn, input, &pendingCtrlD, &detached, false)
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
	err = processInput(conn, input, &pendingCtrlD, &detached, false)
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
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, false)
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, false)
	
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
	_ = processInput(conn, []byte{0x04, 'x'}, &pendingCtrlD, &detached, false)
	
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
	_ = processInput(conn, []byte("hello"), &pendingCtrlD, &detached, true)
	if conn.out.Len() != 0 {
		t.Errorf("Expected 0 bytes output in read-only mode, got %d", conn.out.Len())
	}

	// Detach sequence should STILL work
	_ = processInput(conn, []byte{0x04}, &pendingCtrlD, &detached, true)
	if !pendingCtrlD {
		t.Error("pendingCtrlD should be true in read-only mode")
	}
	err := processInput(conn, []byte{'d'}, &pendingCtrlD, &detached, true)
	if err != io.EOF {
		t.Errorf("Expected EOF on detach in read-only mode, got %v", err)
	}
	if atomic.LoadInt32(&detached) != 1 {
		t.Error("Detached flag not set in read-only mode")
	}
}
