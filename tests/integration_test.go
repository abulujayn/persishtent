package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestIntegration(t *testing.T) {
	// Build binary
	// Assuming we are running from root or tests dir.
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "persishtent")
	
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/persishtent/main.go")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build: %v\nOutput: %s", err, output)
	}
	
	sessionName := "integration-test"
	
	// Cleanup any previous run
	home, _ := os.UserHomeDir()
	sockPath := filepath.Join(home, ".persishtent", sessionName+".sock")
	logPath := filepath.Join(home, ".persishtent", sessionName+".log")
	_ = os.Remove(sockPath)
	_ = os.Remove(logPath)

	// Start Session (detached)
	startCmd := exec.Command(binPath, "start", sessionName)
	
	// Use pty to start the client, so it thinks it has a terminal
	ptmx, err := pty.Start(startCmd)
	if err != nil {
		t.Fatalf("Failed to start client with PTY: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	
	// Give it a moment to initialize
	time.Sleep(2 * time.Second)
	
	// Send command
	markerFile := filepath.Join(tmpDir, "marker")
	_ = os.Remove(markerFile)
	
	// Command: echo 'hello persistent' > markerFile
	cmdStr := "echo 'hello persistent' > " + markerFile + "\n"
	if _, err := ptmx.Write([]byte(cmdStr)); err != nil {
		t.Fatalf("Failed to write to ptmx: %v", err)
	}
	
	time.Sleep(1 * time.Second)
	
	// Verify file exists (Persistence check 1: It works)
	if _, err := os.Stat(markerFile); os.IsNotExist(err) {
		t.Fatalf("Marker file was not created. Shell didn't execute command?")
	}
	
	// Now Detach (Kill the client process 'persishtent start')
	// The daemon should survive.
	if err := startCmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill client: %v", err)
	}
	
	// Wait a bit
	time.Sleep(500 * time.Millisecond)
	
	// Verify socket still exists (daemon alive)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatalf("Socket vanished after client detach. Daemon died?")
	}
	
	// Attach again (Persistence check 2: Re-attach)
	attachCmd := exec.Command(binPath, "attach", sessionName)
	
	// We need another PTY for the attached session
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
	
	// Wait for attach to finish (daemon exits)
	// We can't wait on attachCmd directly because pty.Start doesn't return the same control
	// Actually attachCmd.Wait() works if we used pty.Start(attachCmd)
	attachCmd.Wait()
	
	// Check if socket is gone
	time.Sleep(1 * time.Second)
	if _, err := os.Stat(sockPath); err == nil {
		t.Fatalf("Socket still exists after exit command.")
	}
}

