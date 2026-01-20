package server

import (
	"os"
	"path/filepath"
	"testing"

	"persishtent/internal/config"
	"persishtent/internal/session"
)

func TestLogRotator(t *testing.T) {
	// Setup temp dir
	tmpDir := t.TempDir()
	
	// Mock config
	// We want small size for testing
	config.Global.LogRotationSizeMB = 0 // Will fallback to 1MB logic in constructor...
	// Wait, constructor does: if maxSize <= 0 { maxSize = 1MB }
	// We want SMALLER for test.
	// But constructor uses config directly.
	// We can't easily mock "bytes" size via config which is MB.
	// 1MB is too large for unit test.
	
	// We should probably allow passing size to constructor or make it testable.
	// But sticking to the requested refactor:
	// We can set `LogRotationSizeMB` to 1, write 1MB?
	// That's 1024*1024 bytes. Fast enough.
	
	config.Global.LogRotationSizeMB = 1
	config.Global.MaxLogRotations = 3

	// Need to ensure session directory is mocked too because GetLogFiles uses EnsureDir uses HOME.
	t.Setenv("HOME", tmpDir)
	
	sessionName := "rotator_test"
	// We need to ensure the session directory exists
	if _, err := session.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	
	logPath := filepath.Join(tmpDir, ".persishtent", sessionName + ".log")
	
	logger, err := NewLogRotator(sessionName, logPath)
	if err != nil {
		t.Fatalf("NewLogRotator failed: %v", err)
	}
	defer func() { _ = logger.Close() }()

	// 1. Write data below limit
	data := make([]byte, 1024) // 1KB
	if _, err := logger.Write(data); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	
	stat, _ := os.Stat(logPath)
	if stat.Size() != 1024 {
		t.Errorf("Expected size 1024, got %d", stat.Size())
	}
	
	// 2. Trigger rotation
	// Default limit is 1MB. We need to write ~1MB.
	// We already wrote 1024.
	// Let's write 1MB more.
	bigChunk := make([]byte, 1024*1024)
	if _, err := logger.Write(bigChunk); err != nil {
		t.Fatalf("Write large chunk failed: %v", err)
	}
	
	// Should have rotated.
	// Check if .log.1 exists
	rotatedPath := logPath + ".1"
	if _, err := os.Stat(rotatedPath); os.IsNotExist(err) {
		t.Error("Rotation did not happen, .log.1 missing")
	}
	
	// Check if current log is small (just the remainder?)
	// 1024 (initial) + 1MB (new) > 1MB.
	// Rotation logic: if size + len > max -> rotate.
	// So 1024 + 1MB triggers rotation.
	// The 1MB chunk is written to NEW file.
	// So current file should be 1MB.
	stat, _ = os.Stat(logPath)
	if stat.Size() != 1024*1024 {
		t.Errorf("Expected current log size 1MB (new chunk), got %d", stat.Size())
	}
	
	// 3. Test Max Rotations
	// Max is 3.
	// We have: log, log.1. (Total 2)
	// Write more to trigger more rotations.
	
	// Rotate 2: log -> log.2, log.1 stays. New log.
	if _, err := logger.Write(make([]byte, 1)); err != nil { t.Fatal(err) } // Just bump size
	if _, err := logger.Write(bigChunk); err != nil { t.Fatal(err) } // Trigger
	
	// Rotate 3: log -> log.3.
	if _, err := logger.Write(make([]byte, 1)); err != nil { t.Fatal(err) }
	if _, err := logger.Write(bigChunk); err != nil { t.Fatal(err) }

	// Now we should have: log, log.3, log.2, log.1. (Total 4 > 3?)
	// Wait, logic says: if len(files) >= maxFiles { remove oldest }
	// Before Rotate 3: we had log, log.2, log.1 (count 3).
	// Rotate 3 happens. log -> log.3. Count becomes 4.
	// Pruning should happen. Oldest is log.1.
	
	// Check files
	files, _ := session.GetLogFiles(sessionName)
	if len(files) > 3 {
		t.Errorf("Expected max 3 files, got %d: %v", len(files), files)
	}
}
