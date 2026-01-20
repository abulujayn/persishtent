package tests

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestIntegration(t *testing.T) {
	// Build binary
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "persishtent")
	
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/persishtent/main.go")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build: %v\nOutput: %s", err, output)
	}
	
	// Create a fake home directory for isolation
	fakeHome := t.TempDir()
	
	// Helper to run commands with fake HOME
	prepareCmd := func(name string, args ...string) *exec.Cmd {
		c := exec.Command(name, args...)
		c.Env = append(os.Environ(), "HOME="+fakeHome)
		return c
	}
	
	sessionName := "integration-test"
	
	// paths relative to fake home
	sockPath := filepath.Join(fakeHome, ".persishtent", sessionName+".sock")
	logPath := filepath.Join(fakeHome, ".persishtent", sessionName+".log")

	// Pre-fill log to test truncation
	garbage := []byte("OLD_SESSION_DATA_SHOULD_BE_GONE")
	// We need to ensure the dir exists first
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := os.WriteFile(logPath, garbage, 0600); err != nil {
		t.Fatalf("Failed to write garbage log: %v", err)
	}

	// Start Session (detached)
	startCmd := prepareCmd(binPath, "start", sessionName)
	
	// Use pty to start the client
	ptmx, err := pty.Start(startCmd)
	if err != nil {
		t.Fatalf("Failed to start client with PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	
	// Give it a moment to initialize
	time.Sleep(2 * time.Second)
	
	// Check if log was truncated
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	// Check if garbage is gone
	for i := 0; i < len(content)-len(garbage)+1; i++ {
		if string(content[i:i+len(garbage)]) == string(garbage) {
			t.Fatalf("Log file contains old data! Truncation failed.")
		}
	}
	
	// Send command
	markerFile := filepath.Join(tmpDir, "marker")
	_ = os.Remove(markerFile)
	
	// Command: echo 'hello persistent' > markerFile
	// This will now write to the history in fakeHome/.bash_history
	cmdStr := "echo 'hello persistent' > " + markerFile + "\n"
	if _, err := ptmx.Write([]byte(cmdStr)); err != nil {
		t.Fatalf("Failed to write to ptmx: %v", err)
	}
	
	time.Sleep(1 * time.Second)
	
	// Verify file exists
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Fatalf("Marker file was not created. Shell didn't execute command?")
	}
	
	// Now Detach (Kill the client process)
	if err := startCmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill client: %v", err)
	}
	
	time.Sleep(500 * time.Millisecond)
	
	// Verify socket still exists (daemon alive)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("Socket vanished after client detach. Daemon died?")
	}
	
	// Attach again
	attachCmd := prepareCmd(binPath, "attach", sessionName)
	
	attachPtmx, err := pty.Start(attachCmd)
	if err != nil {
		t.Fatalf("Failed to start attach with PTY: %v", err)
	}
	defer func() { _ = attachPtmx.Close() }()
	
	time.Sleep(1 * time.Second)
	// Exit the shell
	if _, err := attachPtmx.Write([]byte("exit\n")); err != nil {
		t.Logf("Failed to write exit: %v", err)
	}
	
	_ = attachCmd.Wait()
	
	time.Sleep(1 * time.Second)
	if _, err := os.Stat(sockPath); err == nil {
		t.Fatalf("Socket still exists after exit command.")
	}

	// --- Test Kill Subcommand ---
	killSessionName := "kill-test"
	killSockPath := filepath.Join(fakeHome, ".persishtent", killSessionName+".sock")
	
	startKillCmd := prepareCmd(binPath, "start", killSessionName)
	if err := startKillCmd.Start(); err != nil {
		t.Fatalf("Failed to start kill-test session: %v", err)
	}
	
	time.Sleep(2 * time.Second)
	
	if _, err := os.Stat(killSockPath); os.IsNotExist(err) {
		t.Fatalf("kill-test session failed to start")
	}
	
	killCmd := prepareCmd(binPath, "kill", killSessionName)
	if out, err := killCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to run kill command: %v, output: %s", err, out)
	}
	
	time.Sleep(1 * time.Second)
	
	if _, err := os.Stat(killSockPath); err == nil {
		t.Fatalf("Socket still exists after kill command for %s", killSessionName)
	}

	// --- Test Nesting Protection ---
	// 1. List should NOT fail
	listCmd := prepareCmd(binPath, "list")
	listCmd.Env = append(listCmd.Env, "PERSISHTENT_SESSION=fake")
	if out, err := listCmd.CombinedOutput(); err != nil {
		t.Fatalf("List command failed inside nested session: %v, out: %s", err, out)
	}

	// 2. Start should FAIL
	nestCmd := prepareCmd(binPath, "start", "nested-session")
	nestCmd.Env = append(nestCmd.Env, "PERSISHTENT_SESSION=fake")
	out, err := nestCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Expected error when nesting sessions (start), but got none. Output: %s", out)
	}
	if !bytes.Contains(out, []byte("already inside a persishtent session")) {
		t.Fatalf("Unexpected error message for nesting protection: %s", out)
	}
}