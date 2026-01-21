package server

import (
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"persishtent/internal/protocol"
)

func TestServer_Broadcast(t *testing.T) {
	srv := &Server{
		Clients: make(map[net.Conn]struct{}),
	}

	s1, c1 := net.Pipe()
	s2, c2 := net.Pipe()
	defer func() {
		_ = c1.Close()
		_ = c2.Close()
		_ = s1.Close()
		_ = s2.Close()
	}()

	srv.Clients[s1] = struct{}{}
	srv.Clients[s2] = struct{}{}

	data := []byte("hello")
	
	var wg sync.WaitGroup
	wg.Add(2)
	
	for _, c := range []net.Conn{c1, c2} {
		go func(conn net.Conn) {
			defer wg.Done()
			_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			typ, payload, err := protocol.ReadPacket(conn)
			if err != nil {
				return
			}
			if typ != protocol.TypeData || string(payload) != "hello" {
				panic("Unexpected packet")
			}
		}(c)
	}

	time.Sleep(50 * time.Millisecond)
	srv.broadcast(data)
	
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Broadcast test timed out")
	}
}

func TestServer_HandleClient_MasterKick(t *testing.T) {
	pr, pw, _ := os.Pipe()
	defer func() {
		_ = pr.Close()
		_ = pw.Close()
	}()

	srv := &Server{
		Clients: make(map[net.Conn]struct{}),
	}

	// 1. First Master connects
	s1, c1 := net.Pipe()
	
	go func() {
		_ = protocol.WritePacket(c1, protocol.TypeMode, []byte{protocol.ModeMaster})
	}()
	
	go srv.handleClient(s1, pw)

	time.Sleep(100 * time.Millisecond)

	srv.Lock.Lock()
	if srv.Master != s1 {
		srv.Lock.Unlock()
		t.Fatalf("s1 should be Master")
	}
	srv.Lock.Unlock()

	// 2. Second Master connects
	s2, c2 := net.Pipe()
	defer func() { _ = c2.Close() }()
	
	go func() {
		_ = protocol.WritePacket(c2, protocol.TypeMode, []byte{protocol.ModeMaster})
	}()
	
	// We MUST read from c1 in background because handleClient(s2) will block writing TypeKick to s1
	kickReceived := make(chan protocol.Type, 1)
	go func() {
		_ = c1.SetReadDeadline(time.Now().Add(1 * time.Second))
		typ, _, _ := protocol.ReadPacket(c1)
		kickReceived <- typ
		_ = c1.Close()
	}()

	go srv.handleClient(s2, pw)

	time.Sleep(100 * time.Millisecond)

	srv.Lock.Lock()
	if srv.Master != s2 {
		srv.Lock.Unlock()
		t.Errorf("s2 should be Master")
	}
	srv.Lock.Unlock()

	select {
	case typ := <-kickReceived:
		if typ != protocol.TypeKick {
			t.Errorf("Expected TypeKick for s1, got %d", typ)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timed out waiting for kick on s1")
	}
}

func TestServer_HandleClient_ReadOnly(t *testing.T) {
	pr, pw, _ := os.Pipe()
	defer func() {
		_ = pr.Close()
		_ = pw.Close()
	}()

	srv := &Server{
		Clients: make(map[net.Conn]struct{}),
	}

	// Read-only client
	s1, c1 := net.Pipe()
	
	go func() {
		_ = protocol.WritePacket(c1, protocol.TypeMode, []byte{protocol.ModeReadOnly})
		_ = protocol.WritePacket(c1, protocol.TypeData, []byte("forbidden"))
		time.Sleep(50 * time.Millisecond)
		_ = c1.Close()
	}()
	
	done := make(chan struct{})
	go func() {
		srv.handleClient(s1, pw)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("handleClient didn't return after connection closed")
	}

	srv.Lock.Lock()
	if srv.Master != nil {
		t.Error("Master should be nil")
	}
	srv.Lock.Unlock()
}