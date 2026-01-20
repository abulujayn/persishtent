package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureDir(t *testing.T) {
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

	if _, err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}
	
	path, _ := GetInfoPath(name)
	defer func() { _ = os.Remove(path) }()

	if err := WriteInfo(info); err != nil {
		t.Fatalf("WriteInfo failed: %v", err)
	}

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

func TestSessionRename(t *testing.T) {
	oldName := "old-session"
	newName := "new-session"
	
	Cleanup(oldName)
	Cleanup(newName)
	defer Cleanup(newName)

	dir, _ := EnsureDir()
	_ = os.WriteFile(filepath.Join(dir, oldName+".sock"), []byte("sock"), 0600)
	_ = os.WriteFile(filepath.Join(dir, oldName+".log"), []byte("log"), 0600)
	info := Info{Name: oldName, PID: 123}
	_ = WriteInfo(info)

	if err := Rename(oldName, newName); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, oldName+".sock")); err == nil {
		t.Errorf("Old socket still exists")
	}

	if _, err := os.Stat(filepath.Join(dir, newName+".sock")); err != nil {
		t.Errorf("New socket missing")
	}
	
	newInfo, err := ReadInfo(newName)
	if err != nil {
		t.Fatalf("Failed to read new info: %v", err)
	}
	if newInfo.Name != newName {
		t.Errorf("Info name not updated. Got %s, want %s", newInfo.Name, newName)
	}
}

func TestIsAliveEdgeCases(t *testing.T) {
	info := Info{Name: "dead", PID: -1}
	if info.IsAlive() {
		t.Errorf("Expected IsAlive to be false for PID -1")
	}

		info = Info{Name: "nosock", PID: os.Getpid()}

		if info.IsAlive() {

			t.Errorf("Expected IsAlive to be false when socket is missing")

		}

	}

	

	func TestGetLogFiles(t *testing.T) {

		name := "logtest"

		Cleanup(name)

		defer Cleanup(name)

	

		dir, _ := EnsureDir()

		// Create some files out of order

		_ = os.WriteFile(filepath.Join(dir, name+".log.10"), []byte("10"), 0600)

		_ = os.WriteFile(filepath.Join(dir, name+".log.2"), []byte("2"), 0600)

		_ = os.WriteFile(filepath.Join(dir, name+".log"), []byte("active"), 0600)

	

		files, err := GetLogFiles(name)

		if err != nil {

			t.Fatal(err)

		}

	

		if len(files) != 3 {

			t.Fatalf("Expected 3 files, got %d", len(files))

		}

	

		// Expected order: .log.2, .log.10, .log

		if filepath.Base(files[0]) != name+".log.2" {

			t.Errorf("Expected oldest to be .log.2, got %s", filepath.Base(files[0]))

		}

		if filepath.Base(files[1]) != name+".log.10" {

			t.Errorf("Expected second oldest to be .log.10, got %s", filepath.Base(files[1]))

		}

		if filepath.Base(files[2]) != name+".log" {

			t.Errorf("Expected newest to be .log, got %s", filepath.Base(files[2]))

		}

	}

	