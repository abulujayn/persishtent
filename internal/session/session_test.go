package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureDir(t *testing.T) {
	// We can't easily mock UserHomeDir without internal changes or env var hacking,
	// but we can check if it returns a path that exists.
	// Actually, we can assume os.UserHomeDir works on linux.
	
	path, err := EnsureDir()
	if err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}
	
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Directory does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("Path is not a directory: %s", path)
	}
	
	// Cleanup if it's a test environment? 
	// Ideally we don't want to clutter the real home dir, 
	// but since we are in a dev environment, it's acceptable to use the real path 
	// provided we don't destroy anything important.
}

func TestGetPaths(t *testing.T) {
	name := "testsession"
	
	sockPath, err := GetSocketPath(name)
	if err != nil {
		t.Fatalf("GetSocketPath failed: %v", err)
	}
	
	logPath, err := GetLogPath(name)
	if err != nil {
		t.Fatalf("GetLogPath failed: %v", err)
	}
	
	home, _ := os.UserHomeDir()
	expectedDir := filepath.Join(home, DirName)
	
	if filepath.Dir(sockPath) != expectedDir {
		t.Errorf("Socket path dir mismatch. Got %s, want %s", filepath.Dir(sockPath), expectedDir)
	}
	
	if filepath.Base(sockPath) != name + ".sock" {
		t.Errorf("Socket filename mismatch. Got %s, want %s.sock", filepath.Base(sockPath), name)
	}

	if filepath.Base(logPath) != name + ".log" {
		t.Errorf("Log filename mismatch. Got %s, want %s.log", filepath.Base(logPath), name)
	}
}

func TestSessionInfo(t *testing.T) {
	name := "infotest"
	now := time.Now().Round(time.Second)
	info := Info{
		Name:      name,
		PID:       12345,
		Command:   "/bin/bash",
		StartTime: now,
	}

	// Ensure dir exists
	if _, err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}
	
	// Cleanup
	path, _ := GetInfoPath(name)
	defer os.Remove(path)

	// Write
	if err := WriteInfo(info); err != nil {
		t.Fatalf("WriteInfo failed: %v", err)
	}

	// Read
	readInfo, err := ReadInfo(name)
	if err != nil {
		t.Fatalf("ReadInfo failed: %v", err)
	}

	if readInfo.Name != info.Name {
		t.Errorf("Name mismatch. Got %s, want %s", readInfo.Name, info.Name)
	}
	if readInfo.PID != info.PID {
		t.Errorf("PID mismatch. Got %d, want %d", readInfo.PID, info.PID)
	}
	if readInfo.Command != info.Command {
		t.Errorf("Command mismatch. Got %s, want %s", readInfo.Command, info.Command)
	}
	if !readInfo.StartTime.Equal(now) {
		t.Errorf("StartTime mismatch. Got %v, want %v", readInfo.StartTime, now)
	}
}

func TestValidateName(t *testing.T) {
	validNames := []string{"session1", "my_session", "test-session", "123", "S_1-2"}
	invalidNames := []string{"", "session 1", "session/1", "session!", "session$"}

	for _, name := range validNames {
		if err := ValidateName(name); err != nil {
			t.Errorf("Expected name '%s' to be valid, but got error: %v", name, err)
		}
	}

	for _, name := range invalidNames {
		if err := ValidateName(name); err == nil {
			t.Errorf("Expected name '%s' to be invalid, but got no error", name)
		}
	}
}
