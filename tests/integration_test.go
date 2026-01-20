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
	
	// Helper to run commands with fake HOME and without nesting blocks
	prepareCmd := func(name string, args ...string) *exec.Cmd {
		c := exec.Command(name, args...)
		for _, env := range os.Environ() {
			if !bytes.HasPrefix([]byte(env), []byte("PERSISHTENT_SESSION=")) &&
				!bytes.HasPrefix([]byte(env), []byte("HOME=")) {
				c.Env = append(c.Env, env)
			}
		}
		c.Env = append(c.Env, "HOME="+fakeHome)
		return c
	}
	
	sessionName := "integration-test"
	
	// paths relative to fake home
	sockPath := filepath.Join(fakeHome, ".persishtent", sessionName+".sock")
	logPath := filepath.Join(fakeHome, ".persishtent", sessionName+".log")

	// Pre-fill log to test truncation
	garbage := []byte("OLD_SESSION_DATA_SHOULD_BE_GONE")
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	if err := os.WriteFile(logPath, garbage, 0600); err != nil {
		t.Fatalf("Failed to write garbage log: %v", err)
	}

	// Start Session (detached)
	startCmd := prepareCmd(binPath, "start", "-d", sessionName)
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to start session: %v, out: %s", err, out)
	}
	
	// Give it a moment to initialize
	time.Sleep(2 * time.Second)
	
	// Check if log was truncated
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if bytes.Contains(content, garbage) {
		t.Fatalf("Log file contains old data! Truncation failed. Content: %s", string(content))
	}
	
	// Attach
	attachCmd := prepareCmd(binPath, "attach", sessionName)
	ptmx, err := pty.Start(attachCmd)
	if err != nil {
		t.Fatalf("Failed to attach with PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	
	time.Sleep(1 * time.Second)
	
	// Send command
	markerFile := filepath.Join(tmpDir, "marker")
	_ = os.Remove(markerFile)
	
	cmdStr := "echo 'hello persistent' > " + markerFile + "\n"
	if _, err := ptmx.Write([]byte(cmdStr)); err != nil {
		t.Fatalf("Failed to write to ptmx: %v", err)
	}
	
time.Sleep(1 * time.Second)
	
	// Verify file exists
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Fatalf("Marker file was not created. Shell didn't execute command?")
	}
	
	// Detach (Kill the attach command)
	if err := attachCmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill attach process: %v", err)
	}
	
time.Sleep(500 * time.Millisecond)
	
	// Verify socket still exists (daemon alive)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("Socket vanished after client detach. Daemon died?")
	}
	
	// Attach again
	attachCmd2 := prepareCmd(binPath, "attach", sessionName)
	ptmx2, err := pty.Start(attachCmd2)
	if err != nil {
		t.Fatalf("Failed to re-attach with PTY: %v", err)
	}
	defer func() { _ = ptmx2.Close() }()
	
	time.Sleep(1 * time.Second)
	// Exit the shell
	if _, err := ptmx2.Write([]byte("exit\n")); err != nil {
		t.Logf("Failed to write exit: %v", err)
	}
	
	_ = attachCmd2.Wait()
	
	// Check if socket is gone (with retry)
	gone := false
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(sockPath); os.IsNotExist(err) {
			gone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("Socket still exists after exit command.")
	}

	// --- Test Kill Subcommand ---
	killSessionName := "kill-test"
	killSockPath := filepath.Join(fakeHome, ".persishtent", killSessionName+".sock")
	
	startKillCmd := prepareCmd(binPath, "start", "-d", killSessionName)
	if out, err := startKillCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to start kill-test session: %v, out: %s", err, out)
	}
	
	time.Sleep(2 * time.Second)
	
	if _, err := os.Stat(killSockPath); os.IsNotExist(err) {
		t.Fatalf("kill-test session failed to start")
	}
	
	killCmd := prepareCmd(binPath, "kill", killSessionName)
	if out, err := killCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to run kill command: %v, output: %s", err, out)
	}
	
	// Verify it is gone (with retry)
	gone = false
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(killSockPath); os.IsNotExist(err) {
			gone = true
			break
		}
		// Run list to trigger lazy cleanup
		_, _ = prepareCmd(binPath, "list").CombinedOutput()
		time.Sleep(100 * time.Millisecond)
	}
	if !gone {
		t.Fatalf("Socket still exists after kill command for %s", killSessionName)
	}

	// --- Test Start-as-Attach ---
	startName := "start-as-attach"
	// 1. Start detached
	if out, err := prepareCmd(binPath, "start", "-d", startName).CombinedOutput(); err != nil {
		t.Fatalf("Failed to start initial session: %v, out: %s", err, out)
	}
	
	// 2. Start again (should attach)
	// We'll use pty to verify we are attached
	startAttachCmd := prepareCmd(binPath, "start", startName)
	ptmx3, err := pty.Start(startAttachCmd)
	if err != nil {
		t.Fatalf("Failed to start-attach with PTY: %v", err)
	}
	defer func() { _ = ptmx3.Close() }()
	
	time.Sleep(1 * time.Second)
	// If we are attached, we can send exit
	if _, err := ptmx3.Write([]byte("exit\n")); err != nil {
		t.Logf("Failed to write exit to start-attach: %v", err)
	}
	
	_ = startAttachCmd.Wait()
}
