package tests

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"persishtent/internal/protocol"
	"persishtent/internal/server"
	"persishtent/internal/session"
)

// BenchmarkThroughput measures memory allocation during heavy data transfer
func BenchmarkThroughput(b *testing.B) {
	// Setup
	tmpDir := b.TempDir()
	b.Setenv("HOME", tmpDir)
	
	sessionName := "bench"
	sockPath := filepath.Join(tmpDir, "bench.sock")
	logPath := filepath.Join(tmpDir, "bench.log")
	
	// Create dummy files to satisfy session checks if needed
	_ = session.WriteInfo(session.Info{Name: sessionName, PID: os.Getpid(), StartTime: time.Now()})

	// Start Server
	// We use "cat" as a simple echo server essentially, or just a shell.
	// But we want to pump data. 
	// To minimize PTY overhead and test OUR overhead (protocol/server), 
	// we ideally want a predictable stream.
	// `yes` is good for generating output.
	// `cat` is good for echo.
	
	// Start server in background
	go func() {
		// Use a simple command that echoes input back or just stays alive
		// "cat" will echo what we write to PTY master.
		if err := server.Run(sessionName, sockPath, logPath, "cat"); err != nil {
			// b.Logf("Server exited: %v", err)
		}
	}()

	// Wait for socket
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Connect Client
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		b.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	// Handshake
	if err := protocol.WritePacket(conn, protocol.TypeMode, []byte{protocol.ModeMaster}); err != nil {
		b.Fatal(err)
	}

	// Prepare data chunk (e.g. 4KB)
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = 'a'
	}
	// Add newline to ensure cat flushes line buffered? 
	// PTY might buffer.
	chunk[4095] = '\n'

	b.ResetTimer()
	
	// Pump data
	go func() {
		for i := 0; i < b.N; i++ {
			// Write Data packet
			// Client -> Server -> PTY -> cat -> PTY -> Server -> Client
			if err := protocol.WritePacket(conn, protocol.TypeData, chunk); err != nil {
				return
			}
		}
	}()

	// Read loop
	received := 0
	target := b.N * 4096 // Roughly. 'cat' might buffer differently.
	
	// We just read until we get enough or timer ends.
	// Actually, strict synchronization in benchmarks is tricky with async PTY.
	// Instead, let's just measure the write/read loop speed.
	
	for received < target {
		t, payload, err := protocol.ReadPacket(conn)
		if err != nil {
			if err == io.EOF {
				break
			}
			b.Fatal(err)
		}
		if t == protocol.TypeData {
			received += len(payload)
		}
	}
	
b.StopTimer()
	b.SetBytes(4096)
}
